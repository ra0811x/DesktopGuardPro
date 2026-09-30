package agent

import (
	"context"
	"errors"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

const (
	inputShieldPolicyPollInterval      = time.Second
	inputShieldInventoryReportInterval = 5 * time.Second
	inputShieldInventorySessionID      = "device-inventory"
)

type inputShieldSessionConfiguration struct {
	UnlockRequested bool
	SessionID       string
	Enabled         bool
	Policy          domain.InputShieldPolicy
}

type inputShieldRuntimeClient interface {
	Current(context.Context) (inputShieldSessionConfiguration, error)
	Report(context.Context, coreservice.AgentInputShieldReportRequest) error
	Verify(context.Context, coreservice.InputShieldCredentialVerifyRequest) error
}

type inputShieldEngine interface {
	Start(domain.InputShieldPolicy) error
	Stop()
	Events() <-chan BlockedInputEvent
	UnlockRequests() <-chan struct{}
	DroppedEvents() uint64
	State() inputShieldState
	Healthy() bool
	CompleteVerification(bool)
	RequestUnlock()
}

type inputShieldOverlay interface {
	Start() error
	Show(string, time.Duration)
	Stop()
}

type inputShieldDeviceTracker interface {
	Start() error
	Stop()
	Events() <-chan InputDeviceChange
	DroppedEvents() uint64
	Snapshot() []InputDeviceIdentity
}

type inputShieldPrompt func(context.Context, domain.InputShieldPolicy, inputShieldVerify) error

type inputShieldVerificationResult struct {
	controlID  string
	generation uint64
	err        error
}

type windowsInputShieldRuntime struct {
	client       inputShieldRuntimeClient
	newEngine    func() inputShieldEngine
	newOverlay   func() inputShieldOverlay
	newTracker   func() inputShieldDeviceTracker
	prompt       inputShieldPrompt
	pollInterval time.Duration

	engine             inputShieldEngine
	overlay            inputShieldOverlay
	tracker            inputShieldDeviceTracker
	active             bool
	session            inputShieldSessionConfiguration
	generation         uint64
	verificationCancel context.CancelFunc
	verifying          bool
}

func newWindowsInputShieldRuntime() *windowsInputShieldRuntime {
	return &windowsInputShieldRuntime{
		client:       pipeInputShieldClient{},
		newEngine:    func() inputShieldEngine { return newWindowsInputShield() },
		newOverlay:   func() inputShieldOverlay { return newInputWarningOverlay() },
		newTracker:   func() inputShieldDeviceTracker { return newInputDeviceTracker() },
		pollInterval: inputShieldPolicyPollInterval,
		prompt:       promptInputShieldCredentials,
	}
}

func (runtime *windowsInputShieldRuntime) Run(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if runtime.pollInterval <= 0 {
		runtime.pollInterval = inputShieldPolicyPollInterval
	}
	poll := time.NewTicker(runtime.pollInterval)
	heartbeat := time.NewTicker(time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	verificationResults := make(chan inputShieldVerificationResult, 2)
	lastHeartbeat := time.Time{}
	lastInventoryReport := time.Time{}
	runtime.startDeviceTracker(ctx)
	if runtime.tracker != nil {
		runtime.reportDeviceInventory(ctx)
		lastInventoryReport = time.Now()
	}
	runtime.reconcile(ctx)
	for {
		var blockedEvents <-chan BlockedInputEvent
		var unlockRequests <-chan struct{}
		var deviceEvents <-chan InputDeviceChange
		if runtime.active {
			blockedEvents = runtime.engine.Events()
			unlockRequests = runtime.engine.UnlockRequests()
		}
		if runtime.tracker != nil {
			deviceEvents = runtime.tracker.Events()
		}
		select {
		case <-ctx.Done():
			stopContext, cancel := context.WithTimeout(context.Background(), agentRequestTimeout)
			runtime.stop(stopContext)
			runtime.stopDeviceTracker()
			cancel()
			return nil
		case <-poll.C:
			runtime.reconcile(ctx)
		case <-heartbeat.C:
			if runtime.tracker != nil && (lastInventoryReport.IsZero() || time.Since(lastInventoryReport) >= inputShieldInventoryReportInterval) {
				runtime.reportDeviceInventory(ctx)
				lastInventoryReport = time.Now()
			}
			if runtime.active && !runtime.engine.Healthy() {
				runtime.reportState(ctx, "hook_degraded", "degraded", false)
				runtime.stopComponents(ctx, false)
				runtime.reconcile(ctx)
				lastHeartbeat = time.Time{}
				continue
			}
			if runtime.active && (lastHeartbeat.IsZero() || time.Since(lastHeartbeat) >= time.Duration(runtime.session.Policy.HookHeartbeatSeconds)*time.Second) {
				runtime.report(ctx, "heartbeat", nil, nil)
				lastHeartbeat = time.Now()
			}
		case event := <-blockedEvents:
			runtime.handleBlockedInput(ctx, event)
		case <-unlockRequests:
			if runtime.verifying {
				continue
			}
			promptContext, cancel := context.WithCancel(ctx)
			runtime.verificationCancel = cancel
			runtime.verifying = true
			runtime.report(ctx, "unlock_requested", nil, nil)
			go runtime.verifyUnlock(promptContext, verificationResults, runtime.session.SessionID, runtime.generation,
				runtime.session.Policy)
		case result := <-verificationResults:
			if !runtime.active || runtime.engine == nil || runtime.generation != result.generation ||
				runtime.session.SessionID != result.controlID {
				// The task was stopped or replaced while its prompt was open. Its
				// result must never change the replacement task's hook state.
				continue
			}
			success := result.err == nil
			runtime.verifying = false
			if runtime.verificationCancel != nil {
				runtime.verificationCancel()
				runtime.verificationCancel = nil
			}
			runtime.engine.CompleteVerification(success)
			if success {
				// Credential verification moves the temporary control to stopping.
				// Remove the hooks now and acknowledge that transition before the
				// service clears the task.
				runtime.stopComponents(ctx, true)
				continue
			}
			if runtime.overlay != nil {
				runtime.overlay.Show("本地输入防护验证失败，保护仍然有效。", 5*time.Second)
			}
			runtime.report(ctx, "unlock_failed", nil, nil)
		case event := <-deviceEvents:
			if runtime.active {
				runtime.handleDeviceChange(ctx, event)
			}
		}
	}
}

func (runtime *windowsInputShieldRuntime) reconcile(ctx context.Context) {
	configuration, err := runtime.client.Current(ctx)
	if err != nil {
		return
	}
	if configuration.Enabled {
		if runtime.active && runtime.session.SessionID == configuration.SessionID {
			if configuration.UnlockRequested && !runtime.verifying {
				runtime.engine.RequestUnlock()
			}
			return
		}
		if runtime.active {
			runtime.stop(ctx)
		}
		runtime.start(ctx, configuration)
		return
	}
	if runtime.active {
		runtime.stop(ctx)
		return
	}
	if configuration.SessionID != "" {
		// The service keeps the control ID until it receives a stop acknowledgement.
		// Retry after a failed report, including when the hook never started.
		runtime.session = configuration
		runtime.reportState(ctx, "stopped", "disabled", false)
	}
	runtime.engine = nil
	runtime.session = inputShieldSessionConfiguration{}
}

func (runtime *windowsInputShieldRuntime) start(ctx context.Context, configuration inputShieldSessionConfiguration) {
	runtime.session = configuration
	runtime.generation++
	runtime.engine = runtime.newEngine()
	if configuration.Policy.ShowWarningOverlay {
		runtime.overlay = runtime.newOverlay()
		if err := runtime.overlay.Start(); err != nil {
			runtime.overlay = nil
			runtime.reportState(ctx, "hook_degraded", "degraded", false)
			return
		}
	}
	if (configuration.Policy.TrackActiveDevices || configuration.Policy.WarnOnDeviceArrival || configuration.Policy.RecordDeviceRemoval) && runtime.tracker == nil {
		if runtime.overlay != nil {
			runtime.overlay.Stop()
			runtime.overlay = nil
		}
		runtime.reportState(ctx, "hook_degraded", "degraded", false)
		return
	}
	if err := runtime.engine.Start(configuration.Policy); err != nil {
		if runtime.overlay != nil {
			runtime.overlay.Stop()
			runtime.overlay = nil
		}
		runtime.reportState(ctx, "hook_degraded", "degraded", false)
		return
	}
	runtime.active = true
	runtime.report(ctx, "started", nil, nil)
}

func (runtime *windowsInputShieldRuntime) stop(ctx context.Context) {
	runtime.stopComponents(ctx, true)
}

func (runtime *windowsInputShieldRuntime) stopComponents(ctx context.Context, reportStopped bool) {
	if !runtime.active {
		return
	}
	if runtime.verificationCancel != nil {
		runtime.verificationCancel()
		runtime.verificationCancel = nil
	}
	runtime.verifying = false
	runtime.engine.Stop()
	runtime.active = false
	runtime.generation++
	if reportStopped {
		runtime.reportState(ctx, "stopped", "disabled", false)
	}
	if runtime.overlay != nil {
		runtime.overlay.Stop()
		runtime.overlay = nil
	}
	runtime.engine = nil
	runtime.session = inputShieldSessionConfiguration{}
}

func (runtime *windowsInputShieldRuntime) startDeviceTracker(ctx context.Context) {
	runtime.tracker = runtime.newTracker()
	if runtime.tracker == nil || runtime.tracker.Start() == nil {
		return
	}
	runtime.tracker = nil
	runtime.reportState(ctx, "hook_degraded", "degraded", false)
}

func (runtime *windowsInputShieldRuntime) stopDeviceTracker() {
	if runtime.tracker != nil {
		runtime.tracker.Stop()
		runtime.tracker = nil
	}
}

func (runtime *windowsInputShieldRuntime) reportDeviceInventory(ctx context.Context) {
	if runtime.tracker == nil {
		return
	}
	devices := runtime.tracker.Snapshot()
	if len(devices) > 64 {
		devices = devices[:64]
	}
	report := coreservice.AgentInputShieldReportRequest{
		SessionID: inputShieldInventorySessionID, Action: "inventory", State: "disabled",
		ObservedUTC: time.Now().UTC(), Devices: make([]coreservice.AgentInputShieldDevice, 0, len(devices)),
	}
	for _, inputDevice := range devices {
		report.Devices = append(report.Devices, coreservice.AgentInputShieldDevice{
			Kind: inputDevice.Kind, InterfacePath: inputDevice.InterfacePath, InstanceID: inputDevice.InstanceID,
			VendorID: inputDevice.VendorID, ProductID: inputDevice.ProductID,
			Active: inputDevice.Active, LastActiveUTC: inputDevice.LastActiveUTC,
		})
	}
	reportContext, cancel := context.WithTimeout(ctx, agentRequestTimeout)
	defer cancel()
	_ = runtime.client.Report(reportContext, report)
}

func (runtime *windowsInputShieldRuntime) handleBlockedInput(ctx context.Context, event BlockedInputEvent) {
	if runtime.overlay != nil {
		runtime.overlay.Show(runtime.session.Policy.WarningMessage,
			time.Duration(runtime.session.Policy.WarningDurationSeconds)*time.Second)
	}
	if !event.Record {
		return
	}
	report := &coreservice.AgentInputShieldReportRequest{
		InputKind: inputShieldEventKind(event.Kind), KeyCode: event.KeyCode,
		Injected: event.Injected, LowerIntegrity: event.LowerIntegrity,
	}
	if runtime.session.Policy.RecordPointerCoordinates && event.Kind != shieldInputKeyboard {
		x, y := event.X, event.Y
		report.PointerX, report.PointerY = &x, &y
	}
	runtime.report(ctx, "input_blocked", report, nil)
}

func (runtime *windowsInputShieldRuntime) handleDeviceChange(ctx context.Context, event InputDeviceChange) {
	if event.Action == "connected" && runtime.session.Policy.WarnOnDeviceArrival && runtime.overlay != nil {
		runtime.overlay.Show("检测到新的键盘或鼠标设备接入，设备信息已记录。", 5*time.Second)
	}
	if (event.Action == "connected" && !runtime.session.Policy.WarnOnDeviceArrival) ||
		(event.Action == "removed" && !runtime.session.Policy.RecordDeviceRemoval) {
		return
	}
	device := &coreservice.AgentInputShieldDevice{
		Kind: event.Device.Kind, InterfacePath: event.Device.InterfacePath, InstanceID: event.Device.InstanceID,
		VendorID: event.Device.VendorID, ProductID: event.Device.ProductID,
	}
	action := "device_connected"
	if event.Action == "removed" {
		action = "device_removed"
	}
	runtime.report(ctx, action, nil, device)
}

func (runtime *windowsInputShieldRuntime) verifyUnlock(
	ctx context.Context,
	result chan<- inputShieldVerificationResult,
	controlID string,
	generation uint64,
	policy domain.InputShieldPolicy,
) {
	err := runtime.prompt(ctx, policy, func(request coreservice.InputShieldCredentialVerifyRequest) error {
		defer clear(request.Password)
		if err := ctx.Err(); err != nil {
			return err
		}
		request.ControlID = controlID
		requestContext, cancel := context.WithTimeout(ctx, agentRequestTimeout)
		defer cancel()
		return runtime.client.Verify(requestContext, request)
	})
	verification := inputShieldVerificationResult{controlID: controlID, generation: generation, err: err}
	select {
	case result <- verification:
	case <-ctx.Done():
	}
}

func (runtime *windowsInputShieldRuntime) report(
	ctx context.Context,
	action string,
	details *coreservice.AgentInputShieldReportRequest,
	device *coreservice.AgentInputShieldDevice,
) {
	state := inputShieldStateName(runtime.engine.State())
	runtime.reportStateWithDetails(ctx, action, state, true, details, device)
}

func (runtime *windowsInputShieldRuntime) reportState(ctx context.Context, action, state string, hookRunning bool) {
	runtime.reportStateWithDetails(ctx, action, state, hookRunning, nil, nil)
}

func (runtime *windowsInputShieldRuntime) reportStateWithDetails(
	ctx context.Context,
	action, state string,
	hookRunning bool,
	details *coreservice.AgentInputShieldReportRequest,
	device *coreservice.AgentInputShieldDevice,
) {
	report := coreservice.AgentInputShieldReportRequest{}
	if details != nil {
		report = *details
	}
	report.SessionID = runtime.session.SessionID
	report.Action = action
	report.State = state
	report.HookRunning = hookRunning
	report.Device = device
	if runtime.tracker != nil && runtime.session.Policy.TrackActiveDevices {
		devices := runtime.tracker.Snapshot()
		if len(devices) > 64 {
			devices = devices[:64]
		}
		report.Devices = make([]coreservice.AgentInputShieldDevice, 0, len(devices))
		for _, inputDevice := range devices {
			report.Devices = append(report.Devices, coreservice.AgentInputShieldDevice{
				Kind: inputDevice.Kind, InterfacePath: inputDevice.InterfacePath, InstanceID: inputDevice.InstanceID,
				VendorID: inputDevice.VendorID, ProductID: inputDevice.ProductID,
				Active: inputDevice.Active, LastActiveUTC: inputDevice.LastActiveUTC,
			})
		}
	}
	report.ObservedUTC = time.Now().UTC()
	report.DroppedEvents = runtime.droppedEvents()
	reportContext, cancel := context.WithTimeout(ctx, agentRequestTimeout)
	defer cancel()
	_ = runtime.client.Report(reportContext, report)
}

func (runtime *windowsInputShieldRuntime) droppedEvents() uint64 {
	var dropped uint64
	if runtime.engine != nil {
		dropped += runtime.engine.DroppedEvents()
	}
	if runtime.tracker != nil {
		dropped += runtime.tracker.DroppedEvents()
	}
	return dropped
}

func inputShieldStateName(state inputShieldState) string {
	switch state {
	case inputShieldProtecting:
		return "protecting"
	case inputShieldVerifying:
		return "verifying"
	case inputShieldSuspended:
		return "suspended"
	case inputShieldDegraded:
		return "degraded"
	default:
		return "disabled"
	}
}

func inputShieldEventKind(kind shieldInputKind) string {
	switch kind {
	case shieldInputKeyboard:
		return "keyboard"
	case shieldInputMouseWheel:
		return "mouse_wheel"
	default:
		return "mouse_button"
	}
}

type pipeInputShieldClient struct{}

func (pipeInputShieldClient) Current(ctx context.Context) (inputShieldSessionConfiguration, error) {
	requestContext, cancel := context.WithTimeout(ctx, agentRequestTimeout)
	defer cancel()
	response, err := callService(requestContext, contracts.MessageTypeInputControlGet, struct{}{}, contracts.MessageTypeInputControlResult)
	if err != nil {
		return inputShieldSessionConfiguration{}, err
	}
	var result coreservice.InputControlResult
	if err := response.DecodePayload(&result); err != nil {
		return inputShieldSessionConfiguration{}, errors.New("decode input control configuration")
	}
	return inputShieldConfigurationFromControl(result), nil
}

func inputShieldConfigurationFromControl(result coreservice.InputControlResult) inputShieldSessionConfiguration {
	return inputShieldSessionConfiguration{SessionID: result.ControlID, Enabled: result.Enabled, Policy: result.Policy,
		UnlockRequested: result.UnlockRequested}
}

func (pipeInputShieldClient) Report(ctx context.Context, report coreservice.AgentInputShieldReportRequest) error {
	_, err := callService(ctx, contracts.MessageTypeAgentInputShieldReport, report, contracts.MessageTypeAgentInputShieldResult)
	return err
}

func (pipeInputShieldClient) Verify(ctx context.Context, request coreservice.InputShieldCredentialVerifyRequest) error {
	defer clear(request.Password)
	response, err := callService(ctx, contracts.MessageTypeInputShieldCredentialVerify, request, contracts.MessageTypeInputShieldCredentialResult)
	if err != nil {
		return err
	}
	var result coreservice.InputShieldCredentialResult
	if err := response.DecodePayload(&result); err != nil || !result.Verified {
		return errors.New("input shield credential verification failed")
	}
	return nil
}
