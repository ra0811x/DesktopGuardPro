package agent

import (
	"context"
	"errors"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

const inputShieldPolicyPollInterval = time.Second

type inputShieldSessionConfiguration struct {
	SessionID string
	Enabled   bool
	Policy    domain.InputShieldPolicy
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

type inputShieldPrompt func(inputShieldEngine, domain.InputShieldPolicy) (coreservice.InputShieldCredentialVerifyRequest, error)

type windowsInputShieldRuntime struct {
	client       inputShieldRuntimeClient
	newEngine    func() inputShieldEngine
	newOverlay   func() inputShieldOverlay
	newTracker   func() inputShieldDeviceTracker
	prompt       inputShieldPrompt
	pollInterval time.Duration

	engine  inputShieldEngine
	overlay inputShieldOverlay
	tracker inputShieldDeviceTracker
	active  bool
	session inputShieldSessionConfiguration
}

func newWindowsInputShieldRuntime() *windowsInputShieldRuntime {
	return &windowsInputShieldRuntime{
		client:       pipeInputShieldClient{},
		newEngine:    func() inputShieldEngine { return newWindowsInputShield() },
		newOverlay:   func() inputShieldOverlay { return newInputWarningOverlay() },
		newTracker:   func() inputShieldDeviceTracker { return newInputDeviceTracker() },
		pollInterval: inputShieldPolicyPollInterval,
		prompt: func(engine inputShieldEngine, policy domain.InputShieldPolicy) (coreservice.InputShieldCredentialVerifyRequest, error) {
			shield, ok := engine.(*windowsInputShield)
			if !ok {
				return coreservice.InputShieldCredentialVerifyRequest{}, errors.New("input shield verification target is unavailable")
			}
			return promptInputShieldCredentials(shield, policy)
		},
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
	verificationResults := make(chan error, 1)
	lastHeartbeat := time.Time{}
	runtime.reconcile(ctx)
	for {
		var blockedEvents <-chan BlockedInputEvent
		var unlockRequests <-chan struct{}
		var deviceEvents <-chan InputDeviceChange
		if runtime.active {
			blockedEvents = runtime.engine.Events()
			unlockRequests = runtime.engine.UnlockRequests()
			if runtime.tracker != nil {
				deviceEvents = runtime.tracker.Events()
			}
		}
		select {
		case <-ctx.Done():
			stopContext, cancel := context.WithTimeout(context.Background(), agentRequestTimeout)
			runtime.stop(stopContext)
			cancel()
			return nil
		case <-poll.C:
			runtime.reconcile(ctx)
		case <-heartbeat.C:
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
			runtime.report(ctx, "unlock_requested", nil, nil)
			go runtime.verifyUnlock(ctx, verificationResults)
		case err := <-verificationResults:
			success := err == nil
			runtime.engine.CompleteVerification(success)
			action := "unlock_succeeded"
			if !success {
				action = "unlock_failed"
				if runtime.overlay != nil {
					runtime.overlay.Show("本地输入防护验证失败，保护仍然有效。", 5*time.Second)
				}
			}
			runtime.report(ctx, action, nil, nil)
		case event := <-deviceEvents:
			runtime.handleDeviceChange(ctx, event)
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
	}
}

func (runtime *windowsInputShieldRuntime) start(ctx context.Context, configuration inputShieldSessionConfiguration) {
	runtime.session = configuration
	runtime.engine = runtime.newEngine()
	if configuration.Policy.ShowWarningOverlay {
		runtime.overlay = runtime.newOverlay()
		if err := runtime.overlay.Start(); err != nil {
			runtime.overlay = nil
			runtime.reportState(ctx, "hook_degraded", "degraded", false)
			return
		}
	}
	if configuration.Policy.TrackActiveDevices || configuration.Policy.WarnOnDeviceArrival || configuration.Policy.RecordDeviceRemoval {
		runtime.tracker = runtime.newTracker()
		if err := runtime.tracker.Start(); err != nil {
			if runtime.overlay != nil {
				runtime.overlay.Stop()
				runtime.overlay = nil
			}
			runtime.tracker = nil
			runtime.reportState(ctx, "hook_degraded", "degraded", false)
			return
		}
	}
	if err := runtime.engine.Start(configuration.Policy); err != nil {
		if runtime.tracker != nil {
			runtime.tracker.Stop()
			runtime.tracker = nil
		}
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
	runtime.engine.Stop()
	runtime.active = false
	if reportStopped {
		runtime.reportState(ctx, "stopped", "disabled", false)
	}
	if runtime.tracker != nil {
		runtime.tracker.Stop()
		runtime.tracker = nil
	}
	if runtime.overlay != nil {
		runtime.overlay.Stop()
		runtime.overlay = nil
	}
	runtime.engine = nil
	runtime.session = inputShieldSessionConfiguration{}
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

func (runtime *windowsInputShieldRuntime) verifyUnlock(ctx context.Context, result chan<- error) {
	request, err := runtime.prompt(runtime.engine, runtime.session.Policy)
	if err == nil {
		err = runtime.client.Verify(ctx, request)
	}
	clear(request.Password)
	select {
	case result <- err:
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
	return inputShieldSessionConfiguration{SessionID: result.ControlID, Enabled: result.Enabled, Policy: result.Policy}
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
