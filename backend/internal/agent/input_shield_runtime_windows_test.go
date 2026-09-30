package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

func TestWindowsInputShieldRuntimeStartsAndStopsInSafeOrder(t *testing.T) {
	client := &fakeInputShieldRuntimeClient{configuration: enabledInputShieldConfiguration()}
	engine := newFakeInputShieldEngine()
	overlay := &fakeInputShieldOverlay{}
	tracker := newFakeInputShieldDeviceTracker()
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine { return engine },
		newOverlay: func() inputShieldOverlay { return overlay }, newTracker: func() inputShieldDeviceTracker { return tracker },
		prompt: func(context.Context, domain.InputShieldPolicy, inputShieldVerify) error {
			return nil
		}, pollInterval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	startedReport := waitForInputShieldReport(t, client, "started")
	if !engine.started || !overlay.started || !tracker.started {
		t.Fatalf("components started = engine %v, overlay %v, tracker %v", engine.started, overlay.started, tracker.started)
	}
	if len(startedReport.Devices) != 1 || startedReport.Devices[0].InstanceID != "HID\\KEYBOARD\\1" || !startedReport.Devices[0].Active {
		t.Fatalf("started device snapshot = %+v", startedReport.Devices)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !engine.stopped || !overlay.stopped || !tracker.stopped {
		t.Fatalf("components stopped = engine %v, overlay %v, tracker %v", engine.stopped, overlay.stopped, tracker.stopped)
	}
}

func TestInputShieldConfigurationUsesStandaloneInputControl(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	policy.BlockPhysicalKeyboard = true
	policy.BlockPhysicalMouse = false
	policy.BlockPointerMovement = false
	configuration := inputShieldConfigurationFromControl(coreservice.InputControlResult{
		ControlID: "input-task-1", Source: "temporary", Enabled: true, State: "starting", Policy: policy,
	})
	if !configuration.Enabled || configuration.SessionID != "input-task-1" ||
		!configuration.Policy.BlockPhysicalKeyboard || configuration.Policy.BlockPhysicalMouse {
		t.Fatalf("standalone input control configuration = %+v", configuration)
	}
}

func TestWindowsInputShieldRuntimeReportsDeviceInventoryWhileControlIsDisabled(t *testing.T) {
	client := &fakeInputShieldRuntimeClient{}
	tracker := newFakeInputShieldDeviceTracker()
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine { return newFakeInputShieldEngine() },
		newOverlay: func() inputShieldOverlay { return &fakeInputShieldOverlay{} },
		newTracker: func() inputShieldDeviceTracker { return tracker },
		prompt: func(context.Context, domain.InputShieldPolicy, inputShieldVerify) error {
			return nil
		}, pollInterval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	inventory := waitForInputShieldReport(t, client, "inventory")
	if inventory.SessionID != inputShieldInventorySessionID || inventory.State != "disabled" ||
		len(inventory.Devices) != 1 || !tracker.started {
		t.Fatalf("device inventory = %+v, tracker started = %v", inventory, tracker.started)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !tracker.stopped {
		t.Fatal("device tracker was not stopped")
	}
}

func TestInputShieldUnlockButtonUsesExistingDialogAndCancellation(t *testing.T) {
	configuration := enabledInputShieldConfiguration()
	configuration.UnlockRequested = true
	client := &fakeInputShieldRuntimeClient{configuration: configuration}
	engine := newFakeInputShieldEngine()
	promptStarted := make(chan struct{}, 2)
	promptCancelled := make(chan struct{})
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine { return engine },
		newOverlay: func() inputShieldOverlay { return &fakeInputShieldOverlay{} },
		newTracker: func() inputShieldDeviceTracker { return newFakeInputShieldDeviceTracker() },
		prompt: func(ctx context.Context, _ domain.InputShieldPolicy, _ inputShieldVerify) error {
			promptStarted <- struct{}{}
			<-ctx.Done()
			close(promptCancelled)
			return ctx.Err()
		}, pollInterval: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-promptStarted:
	case <-time.After(time.Second):
		t.Fatal("unlock button did not open password dialog")
	}
	select {
	case <-promptStarted:
		t.Fatal("repeated polling opened a second password dialog")
	case <-time.After(30 * time.Millisecond):
	}
	client.SetConfiguration(inputShieldSessionConfiguration{})
	select {
	case <-promptCancelled:
	case <-time.After(time.Second):
		t.Fatal("expired control left password dialog open")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !engine.stopped || engine.completeCalls != 0 {
		t.Fatal("cancelled dialog changed the stopped engine")
	}
}

func TestWindowsInputShieldRuntimeVerifiesUnlockAsynchronously(t *testing.T) {
	client := &fakeInputShieldRuntimeClient{configuration: enabledInputShieldConfiguration()}
	engine := newFakeInputShieldEngine()
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine { return engine },
		newOverlay: func() inputShieldOverlay { return &fakeInputShieldOverlay{} },
		newTracker: func() inputShieldDeviceTracker { return newFakeInputShieldDeviceTracker() },
		prompt: func(_ context.Context, _ domain.InputShieldPolicy, verify inputShieldVerify) error {
			return verify(coreservice.InputShieldCredentialVerifyRequest{Password: []byte("correct-password")})
		}, pollInterval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitForInputShieldReport(t, client, "started")
	engine.state = inputShieldVerifying
	engine.unlock <- struct{}{}
	waitForInputShieldReport(t, client, "stopped")
	if !engine.stopped || engine.state != inputShieldDisabled || client.verifyCalls != 1 {
		t.Fatalf("unlock stopped = %v, state = %d, verify calls = %d",
			engine.stopped, engine.state, client.verifyCalls)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsInputShieldRuntimeIgnoresVerificationForReplacedControl(t *testing.T) {
	client := &fakeInputShieldRuntimeClient{configuration: enabledInputShieldConfiguration()}
	first := newFakeInputShieldEngine()
	second := newFakeInputShieldEngine()
	engines := []inputShieldEngine{first, second}
	promptStarted := make(chan struct{})
	releasePrompt := make(chan struct{})
	runtime := &windowsInputShieldRuntime{
		client: client,
		newEngine: func() inputShieldEngine {
			engine := engines[0]
			engines = engines[1:]
			return engine
		},
		newOverlay: func() inputShieldOverlay { return &fakeInputShieldOverlay{} },
		newTracker: func() inputShieldDeviceTracker { return newFakeInputShieldDeviceTracker() },
		prompt: func(_ context.Context, _ domain.InputShieldPolicy, verify inputShieldVerify) error {
			close(promptStarted)
			<-releasePrompt
			return verify(coreservice.InputShieldCredentialVerifyRequest{Password: []byte("correct-password")})
		},
		pollInterval: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitForInputShieldReport(t, client, "started")
	first.state = inputShieldVerifying
	first.unlock <- struct{}{}
	select {
	case <-promptStarted:
	case <-time.After(time.Second):
		t.Fatal("unlock prompt did not start")
	}

	secondConfiguration := enabledInputShieldConfiguration()
	secondConfiguration.SessionID = "session-2"
	client.SetConfiguration(secondConfiguration)
	waitForInputShieldReport(t, client, "started")
	if !first.stopped || !second.started {
		t.Fatalf("control replacement first stopped=%v second started=%v", first.stopped, second.started)
	}
	close(releasePrompt)

	time.Sleep(20 * time.Millisecond)
	if second.completeCalls != 0 || second.state != inputShieldProtecting || client.verifyCalls != 0 {
		t.Fatalf("stale verification changed replacement engine: calls=%d state=%d", second.completeCalls, second.state)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsInputShieldRuntimeReportsAndRestartsUnhealthyHook(t *testing.T) {
	client := &fakeInputShieldRuntimeClient{configuration: enabledInputShieldConfiguration()}
	firstEngine := newFakeInputShieldEngine()
	firstEngine.healthy = true
	secondEngine := newFakeInputShieldEngine()
	secondEngine.healthy = true
	engines := []inputShieldEngine{firstEngine, secondEngine}
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine {
			engine := engines[0]
			engines = engines[1:]
			return engine
		},
		newOverlay: func() inputShieldOverlay { return &fakeInputShieldOverlay{} },
		newTracker: func() inputShieldDeviceTracker { return newFakeInputShieldDeviceTracker() },
		prompt: func(context.Context, domain.InputShieldPolicy, inputShieldVerify) error {
			return nil
		}, pollInterval: 20 * time.Millisecond,
	}
	runtime.client.(*fakeInputShieldRuntimeClient).configuration.Policy.HookHeartbeatSeconds = 1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitForInputShieldReport(t, client, "started")
	firstEngine.healthy = false
	degraded := waitForInputShieldReport(t, client, "hook_degraded")
	if degraded.State != "degraded" || degraded.HookRunning {
		t.Fatalf("unhealthy hook report = %+v", degraded)
	}
	waitForInputShieldReport(t, client, "started")
	if !firstEngine.stopped || !secondEngine.started {
		t.Fatalf("hook restart first stopped=%v second started=%v", firstEngine.stopped, secondEngine.started)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsInputShieldRuntimeAcknowledgesStopAfterFailedStart(t *testing.T) {
	configuration := enabledInputShieldConfiguration()
	configuration.Policy.ShowWarningOverlay = false
	configuration.Policy.TrackActiveDevices = false
	configuration.Policy.WarnOnDeviceArrival = false
	configuration.Policy.RecordDeviceRemoval = false
	client := &fakeInputShieldRuntimeClient{configuration: configuration}
	engine := newFakeInputShieldEngine()
	engine.startErr = errors.New("hook could not start")
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine { return engine },
	}
	runtime.reconcile(context.Background())
	waitForInputShieldReport(t, client, "hook_degraded")
	client.SetConfiguration(inputShieldSessionConfiguration{SessionID: configuration.SessionID})
	runtime.reconcile(context.Background())
	stopped := waitForInputShieldReport(t, client, "stopped")
	if stopped.SessionID != configuration.SessionID || stopped.HookRunning {
		t.Fatalf("stop acknowledgement after failed start = %+v", stopped)
	}
}

func TestWindowsInputShieldRuntimeAcknowledgesStoppedControlAfterAgentRestart(t *testing.T) {
	client := &fakeInputShieldRuntimeClient{configuration: inputShieldSessionConfiguration{SessionID: "stopping-task"}}
	runtime := &windowsInputShieldRuntime{client: client}
	runtime.reconcile(context.Background())
	stopped := waitForInputShieldReport(t, client, "stopped")
	if stopped.SessionID != "stopping-task" || stopped.HookRunning {
		t.Fatalf("new agent stop acknowledgement = %+v", stopped)
	}
}

func TestWindowsInputShieldRuntimeRetriesFailedStopAcknowledgement(t *testing.T) {
	configuration := enabledInputShieldConfiguration()
	configuration.Policy.ShowWarningOverlay = false
	configuration.Policy.TrackActiveDevices = false
	configuration.Policy.WarnOnDeviceArrival = false
	configuration.Policy.RecordDeviceRemoval = false
	client := &fakeInputShieldRuntimeClient{configuration: configuration, failStoppedReports: 1}
	engine := newFakeInputShieldEngine()
	runtime := &windowsInputShieldRuntime{
		client: client, newEngine: func() inputShieldEngine { return engine },
	}
	runtime.reconcile(context.Background())
	waitForInputShieldReport(t, client, "started")
	client.SetConfiguration(inputShieldSessionConfiguration{SessionID: configuration.SessionID})
	runtime.reconcile(context.Background())
	waitForInputShieldReport(t, client, "stopped")
	runtime.reconcile(context.Background())
	stopped := waitForInputShieldReport(t, client, "stopped")
	if stopped.SessionID != configuration.SessionID || !engine.stopped {
		t.Fatalf("retried stop acknowledgement = %+v, engine stopped = %v", stopped, engine.stopped)
	}
}

func enabledInputShieldConfiguration() inputShieldSessionConfiguration {
	policy := domain.DefaultMonitoringPolicy().InputShield
	return inputShieldSessionConfiguration{SessionID: "session-1", Enabled: true, Policy: policy}
}

type fakeInputShieldRuntimeClient struct {
	configuration      inputShieldSessionConfiguration
	reports            chan coreservice.AgentInputShieldReportRequest
	verifications      chan coreservice.InputShieldCredentialVerifyRequest
	mu                 sync.Mutex
	verifyCalls        int
	failStoppedReports int
}

func (client *fakeInputShieldRuntimeClient) Current(context.Context) (inputShieldSessionConfiguration, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.configuration, nil
}

func (client *fakeInputShieldRuntimeClient) SetConfiguration(configuration inputShieldSessionConfiguration) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.configuration = configuration
}

func (client *fakeInputShieldRuntimeClient) Report(_ context.Context, report coreservice.AgentInputShieldReportRequest) error {
	client.mu.Lock()
	if client.reports == nil {
		client.reports = make(chan coreservice.AgentInputShieldReportRequest, 16)
	}
	reports := client.reports
	fail := report.Action == "stopped" && client.failStoppedReports > 0
	if fail {
		client.failStoppedReports--
	}
	client.mu.Unlock()
	reports <- report
	if fail {
		return errors.New("stop acknowledgement failed")
	}
	return nil
}

func (client *fakeInputShieldRuntimeClient) Verify(_ context.Context, request coreservice.InputShieldCredentialVerifyRequest) error {
	client.mu.Lock()
	client.verifyCalls++
	if client.verifications == nil {
		client.verifications = make(chan coreservice.InputShieldCredentialVerifyRequest, 4)
	}
	verifications := client.verifications
	client.mu.Unlock()
	verifications <- request
	return nil
}

func waitForInputShieldVerification(t *testing.T, client *fakeInputShieldRuntimeClient) coreservice.InputShieldCredentialVerifyRequest {
	t.Helper()
	client.mu.Lock()
	if client.verifications == nil {
		client.verifications = make(chan coreservice.InputShieldCredentialVerifyRequest, 4)
	}
	verifications := client.verifications
	client.mu.Unlock()
	select {
	case request := <-verifications:
		return request
	case <-time.After(time.Second):
		t.Fatal("input shield verification was not received")
		return coreservice.InputShieldCredentialVerifyRequest{}
	}
}

func waitForInputShieldReport(t *testing.T, client *fakeInputShieldRuntimeClient, action string) coreservice.AgentInputShieldReportRequest {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		client.mu.Lock()
		if client.reports == nil {
			client.reports = make(chan coreservice.AgentInputShieldReportRequest, 16)
		}
		reports := client.reports
		client.mu.Unlock()
		select {
		case report := <-reports:
			if report.Action == action {
				return report
			}
		case <-deadline:
			t.Fatalf("input shield report %q was not received", action)
		}
	}
}

type fakeInputShieldEngine struct {
	started       bool
	stopped       bool
	startErr      error
	healthy       bool
	state         inputShieldState
	completeCalls int
	events        chan BlockedInputEvent
	unlock        chan struct{}
}

func newFakeInputShieldEngine() *fakeInputShieldEngine {
	return &fakeInputShieldEngine{events: make(chan BlockedInputEvent, 4), unlock: make(chan struct{}, 1), healthy: true}
}

func (engine *fakeInputShieldEngine) Start(domain.InputShieldPolicy) error {
	if engine.startErr != nil {
		return engine.startErr
	}
	engine.started = true
	engine.state = inputShieldProtecting
	return nil
}
func (engine *fakeInputShieldEngine) Stop() {
	engine.stopped = true
	engine.state = inputShieldDisabled
}
func (engine *fakeInputShieldEngine) RequestUnlock() {
	if engine.state != inputShieldProtecting {
		return
	}
	engine.state = inputShieldVerifying
	engine.unlock <- struct{}{}
}
func (engine *fakeInputShieldEngine) Events() <-chan BlockedInputEvent { return engine.events }
func (engine *fakeInputShieldEngine) UnlockRequests() <-chan struct{}  { return engine.unlock }
func (engine *fakeInputShieldEngine) DroppedEvents() uint64            { return 0 }
func (engine *fakeInputShieldEngine) State() inputShieldState          { return engine.state }
func (engine *fakeInputShieldEngine) Healthy() bool                    { return engine.healthy }
func (engine *fakeInputShieldEngine) CompleteVerification(success bool) {
	engine.completeCalls++
	if success {
		engine.state = inputShieldSuspended
	} else {
		engine.state = inputShieldProtecting
	}
}

type fakeInputShieldOverlay struct{ started, stopped bool }

func (overlay *fakeInputShieldOverlay) Start() error       { overlay.started = true; return nil }
func (*fakeInputShieldOverlay) Show(string, time.Duration) {}
func (overlay *fakeInputShieldOverlay) Stop()              { overlay.stopped = true }

type fakeInputShieldDeviceTracker struct {
	started bool
	stopped bool
	events  chan InputDeviceChange
}

func newFakeInputShieldDeviceTracker() *fakeInputShieldDeviceTracker {
	return &fakeInputShieldDeviceTracker{events: make(chan InputDeviceChange, 4)}
}
func (tracker *fakeInputShieldDeviceTracker) Start() error                     { tracker.started = true; return nil }
func (tracker *fakeInputShieldDeviceTracker) Stop()                            { tracker.stopped = true }
func (tracker *fakeInputShieldDeviceTracker) Events() <-chan InputDeviceChange { return tracker.events }
func (*fakeInputShieldDeviceTracker) DroppedEvents() uint64                    { return 0 }
func (*fakeInputShieldDeviceTracker) Snapshot() []InputDeviceIdentity {
	return []InputDeviceIdentity{{Kind: "keyboard", InstanceID: "HID\\KEYBOARD\\1", Active: true}}
}
