package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/storage"
)

func TestAPIHealthGet(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeHealthGet, now, struct{}{}))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response.Type != contracts.MessageTypeHealthResult {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeHealthResult)
	}

	var result HealthResult
	decodeTestPayload(t, response, &result)
	if result.Status != HealthStatusRunning {
		t.Fatalf("health status = %q, want %q", result.Status, HealthStatusRunning)
	}
	if result.Session != nil {
		t.Fatalf("health session = %#v, want nil", result.Session)
	}
}

func TestAPIHealthReportsCollectorDegradation(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	api.SetHealthStatus(HealthStatusDegraded)

	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeHealthGet, now, struct{}{}))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	var result HealthResult
	decodeTestPayload(t, response, &result)
	if result.Status != HealthStatusDegraded {
		t.Fatalf("health status = %q, want %q", result.Status, HealthStatusDegraded)
	}
}

func TestAPIHealthReadsCollectorHealthSource(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	api.SetHealthStatusSource(func() string { return HealthStatusDegraded })

	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeHealthGet, now, struct{}{}))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	var result HealthResult
	decodeTestPayload(t, response, &result)
	if result.Status != HealthStatusDegraded {
		t.Fatalf("health status = %q, want %q", result.Status, HealthStatusDegraded)
	}
}

func TestAPIAcceptsActivityOnlyFromConfiguredAgentForActiveSession(t *testing.T) {
	api, now := newTestAPI()
	policy := domain.DefaultMonitoringPolicy()
	policy.UserSessionActivityEnabled = true
	policy.UserSession.RecordWindowTitle = true
	policy.UserSession.RecordHighRiskShortcuts = true
	if _, err := api.coordinator.CreatePersistedWithMonitoringPolicy("session-1", "代理活动测试", policy, nil); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := api.coordinator.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	api.SetAgentExecutable(`C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`)
	var received AgentActivityReportRequest
	api.SetAgentActivityHandler(func(_ context.Context, client ClientIdentity, report AgentActivityReportRequest) error {
		if client.WindowsSessionID != 3 {
			t.Fatalf("client Windows session = %d, want 3", client.WindowsSessionID)
		}
		received = report
		return nil
	})
	report := AgentActivityReportRequest{
		SessionID: "session-1", WindowTitle: "财务报表 - Desktop Guard Pro", ProcessID: 101,
		ProcessImage: `C:\Program Files\Example\report.exe`, InputActivityCount: 7,
		KeyboardActivityCount: 4, MouseClickCount: 2, MouseWheelCount: 1,
		HighRiskShortcutCount: map[string]uint64{"alt_tab": 1},
		ActivityStartUTC:      now.Add(-time.Second), ActivityEndUTC: now, ObservedUTC: now,
		ForegroundStartUTC: now.Add(-2 * time.Second), ForegroundEndUTC: now, ForegroundDurationMS: 2000,
	}
	client := ClientIdentity{
		WindowsSessionID: 3,
		ImagePath:        `c:\program files\desktop guard pro\DESKTOP-GUARD-AGENT.EXE`,
	}
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentActivityReport, now, report), client)
	if err != nil {
		t.Fatalf("HandleForClient() error = %v", err)
	}
	if response.Type != contracts.MessageTypeAgentActivityResult {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeAgentActivityResult)
	}
	if !reflect.DeepEqual(received, report) {
		t.Fatalf("received report = %#v, want %#v", received, report)
	}

	client.ImagePath = `C:\Temp\untrusted.exe`
	denied, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentActivityReport, now, report), client)
	if err != nil {
		t.Fatalf("HandleForClient(untrusted) error = %v", err)
	}
	assertAPIError(t, denied, ErrorCodeUnauthorized)
}

func TestAPISkipsAgentActivityWhenSessionPolicyDisablesUserActivity(t *testing.T) {
	api, now := newTestAPI()
	policy := domain.DefaultMonitoringPolicy()
	policy.UserSessionActivityEnabled = false
	if _, err := api.coordinator.CreatePersistedWithMonitoringPolicy("session-no-agent", "关闭用户活动", policy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := api.coordinator.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := api.coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	api.SetAgentExecutable(`C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`)
	handlerCalls := 0
	api.SetAgentActivityHandler(func(context.Context, ClientIdentity, AgentActivityReportRequest) error {
		handlerCalls++
		return nil
	})
	report := AgentActivityReportRequest{SessionID: "session-no-agent", ProcessID: 101, ObservedUTC: now}
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentActivityReport, now, report), ClientIdentity{
		ImagePath: `C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`,
	})
	if err != nil || response.Type != contracts.MessageTypeAgentActivityResult || handlerCalls != 0 {
		t.Fatalf("response=%+v handlerCalls=%d error=%v", response, handlerCalls, err)
	}
}

func TestAPIAppliesDetailedUserSessionPolicyBeforeRecording(t *testing.T) {
	api, now := newTestAPI()
	policy := domain.DefaultMonitoringPolicy()
	policy.UserSessionActivityEnabled = true
	policy.UserSession = domain.UserSessionPolicy{
		RecordMouseClicks: true, SampleIntervalSeconds: 5, ReportIntervalSeconds: 30,
	}
	if _, err := api.coordinator.CreatePersistedWithMonitoringPolicy("session-user-detail", "用户活动细项", policy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := api.coordinator.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := api.coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	api.SetAgentExecutable(`C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`)
	var received AgentActivityReportRequest
	api.SetAgentActivityHandler(func(_ context.Context, _ ClientIdentity, report AgentActivityReportRequest) error {
		received = report
		return nil
	})
	report := AgentActivityReportRequest{
		SessionID: "session-user-detail", ProcessID: 101, ProcessImage: `C:\Apps\work.exe`, WindowTitle: "敏感标题",
		InputActivityCount: 5, KeyboardActivityCount: 2, MouseClickCount: 1, MouseWheelCount: 1,
		HighRiskShortcutCount: map[string]uint64{"alt_tab": 1}, ObservedUTC: now,
	}
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentActivityReport, now, report), ClientIdentity{
		ImagePath: `C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`,
	})
	if err != nil || response.Type != contracts.MessageTypeAgentActivityResult {
		t.Fatalf("response=%+v error=%v", response, err)
	}
	if received.ProcessID != 0 || received.ProcessImage != "" || received.WindowTitle != "" ||
		received.KeyboardActivityCount != 0 || received.MouseWheelCount != 0 ||
		received.MouseClickCount != 1 || received.InputActivityCount != 1 || received.HighRiskShortcutCount != nil {
		t.Fatalf("filtered report = %+v", received)
	}
}

func TestAgentActivityReportRejectsUnknownHighRiskShortcutCategory(t *testing.T) {
	now := time.Date(2026, time.September, 13, 1, 2, 3, 0, time.UTC)
	report := AgentActivityReportRequest{
		SessionID: "session-1", ProcessID: 101, ObservedUTC: now,
		InputActivityCount: 2, KeyboardActivityCount: 2,
		HighRiskShortcutCount: map[string]uint64{"raw_key_sequence": 1},
	}
	if report.valid() {
		t.Fatal("activity report accepted an unknown shortcut category")
	}
}

func TestAPIWaitsForCollectorLifecycleBeforeActiveAndCompleted(t *testing.T) {
	api, now := newTestAPI()
	api.verifier = &testSystemCredentialVerifier{}
	api.challenges = map[string]endVerificationChallenge{
		"lifecycle-challenge": {sessionID: "session-1", expiresUTC: now.Add(time.Minute)},
	}
	lifecycle := &testSessionLifecycle{
		coordinator: api.coordinator, observedStates: make(chan domain.SessionState, 2),
		activeStarted: make(chan struct{}), activeRelease: make(chan struct{}),
		stoppedStarted: make(chan struct{}), stoppedRelease: make(chan struct{}),
	}
	api.SetSessionLifecycle(lifecycle)
	create := newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{ID: "session-1", Name: "边界测试"})
	if _, err := api.Handle(create); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	if _, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: domain.SessionStatePreparing})); err != nil {
		t.Fatalf("Handle(preparing) error = %v", err)
	}

	activeDone := make(chan apiCallResult, 1)
	go func() {
		response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: domain.SessionStateActive}))
		activeDone <- apiCallResult{response: response, err: err}
	}()
	waitAPIChannel(t, lifecycle.activeStarted)
	if state := <-lifecycle.observedStates; state != domain.SessionStatePreparing {
		t.Fatalf("state during active wait = %q, want %q", state, domain.SessionStatePreparing)
	}
	select {
	case result := <-activeDone:
		t.Fatalf("active response returned before collector readiness: %+v", result.response)
	default:
	}
	close(lifecycle.activeRelease)
	activeResult := <-activeDone
	if activeResult.err != nil {
		t.Fatalf("Handle(active) error = %v", activeResult.err)
	}
	assertSessionResult(t, activeResult.response, domain.SessionStateActive)

	if _, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &EndProtectionVerification{Token: "lifecycle-challenge", UserName: "Raymond", Password: []byte("correct")},
	})); err != nil {
		t.Fatalf("Handle(finalizing) error = %v", err)
	}
	completedDone := make(chan apiCallResult, 1)
	go func() {
		response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: domain.SessionStateCompleted}))
		completedDone <- apiCallResult{response: response, err: err}
	}()
	waitAPIChannel(t, lifecycle.stoppedStarted)
	if state := <-lifecycle.observedStates; state != domain.SessionStateFinalizing {
		t.Fatalf("state during completed wait = %q, want %q", state, domain.SessionStateFinalizing)
	}
	select {
	case result := <-completedDone:
		t.Fatalf("completed response returned before collector shutdown: %+v", result.response)
	default:
	}
	close(lifecycle.stoppedRelease)
	completedResult := <-completedDone
	if completedResult.err != nil {
		t.Fatalf("Handle(completed) error = %v", completedResult.err)
	}
	assertSessionResult(t, completedResult.response, domain.SessionStateCompleted)
}

func TestAPIReturnsBaselineReviewWhenCollectorStartupPauses(t *testing.T) {
	api, now := newTestAPI()
	lifecycleErr := errors.New("baseline review pending")
	api.SetSessionLifecycle(&testSessionLifecycle{
		coordinator: api.coordinator,
		activeWait: func() error {
			if _, err := api.coordinator.Transition(domain.SessionStateBaselineReview); err != nil {
				t.Fatalf("Transition(baseline_review) error = %v", err)
			}
			return lifecycleErr
		},
	})
	if _, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-review", Name: "基线审查",
	})); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	if _, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State: domain.SessionStatePreparing,
	})); err != nil {
		t.Fatalf("Handle(preparing) error = %v", err)
	}

	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State: domain.SessionStateActive,
	}))
	if err != nil {
		t.Fatalf("Handle(active) error = %v", err)
	}
	assertSessionResult(t, response, domain.SessionStateBaselineReview)
}

func TestAgentActivityReportRejectsInconsistentForegroundDuration(t *testing.T) {
	now := time.Date(2026, time.September, 13, 1, 2, 3, 0, time.UTC)
	report := AgentActivityReportRequest{
		SessionID: "session-1", ProcessID: 101, ObservedUTC: now,
		ForegroundStartUTC: now.Add(-2 * time.Second), ForegroundEndUTC: now,
		ForegroundDurationMS: 1000,
	}
	if report.valid() {
		t.Fatal("activity report accepted an inconsistent foreground duration")
	}
}

func TestAPIRequiresOneTimeSystemCredentialVerificationToFinalize(t *testing.T) {
	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	verifier := &testSystemCredentialVerifier{err: errors.New("invalid credentials")}
	api := &API{
		coordinator: NewCoordinator(), now: func() time.Time { return now },
		authorizedUserSID: "S-1-5-21-1000", verifier: verifier,
		newChallenge: func() (string, error) { return "challenge-1", nil },
	}
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{ID: "session-1", Name: "验证测试"}), client); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: state}), client); err != nil {
			t.Fatalf("Handle(%s) error = %v", state, err)
		}
	}

	direct, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: domain.SessionStateFinalizing}), client)
	if err != nil {
		t.Fatalf("Handle(direct finalizing) error = %v", err)
	}
	assertAPIError(t, direct, ErrorCodeVerificationRequired)

	challengeResponse, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionEndVerificationCreate, now, struct{}{}), client)
	if err != nil {
		t.Fatalf("Handle(challenge) error = %v", err)
	}
	var challenge EndVerificationChallenge
	decodeTestPayload(t, challengeResponse, &challenge)
	if challenge.Token != "challenge-1" || !challenge.ExpiresUTC.After(now) {
		t.Fatalf("challenge = %+v", challenge)
	}

	failed, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &EndProtectionVerification{Token: challenge.Token, UserName: "Raymond", Password: []byte("wrong")},
	}), client)
	if err != nil {
		t.Fatalf("Handle(failed verification) error = %v", err)
	}
	assertAPIError(t, failed, ErrorCodeVerificationFailed)

	replayed, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &EndProtectionVerification{Token: challenge.Token, UserName: "Raymond", Password: []byte("correct")},
	}), client)
	if err != nil {
		t.Fatalf("Handle(replayed verification) error = %v", err)
	}
	assertAPIError(t, replayed, ErrorCodeVerificationRequired)

	api.newChallenge = func() (string, error) { return "challenge-2", nil }
	verifier.err = nil
	challengeResponse, err = api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionEndVerificationCreate, now, struct{}{}), client)
	if err != nil {
		t.Fatalf("Handle(second challenge) error = %v", err)
	}
	decodeTestPayload(t, challengeResponse, &challenge)
	finalized, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &EndProtectionVerification{Token: challenge.Token, UserName: "Raymond", Password: []byte("correct")},
	}), client)
	if err != nil {
		t.Fatalf("Handle(finalizing) error = %v", err)
	}
	assertSessionResult(t, finalized, domain.SessionStateFinalizing)
	if verifier.calls != 2 {
		t.Fatalf("verifier calls = %d, want 2", verifier.calls)
	}
}

func TestAPIRejectsExpiredEndVerificationChallenge(t *testing.T) {
	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	api := &API{
		coordinator:       NewCoordinator(),
		authorizedUserSID: "S-1-5-21-1000",
		verifier:          &testSystemCredentialVerifier{},
		newChallenge:      func() (string, error) { return "expired-challenge", nil },
		now:               func() time.Time { return now },
	}
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{ID: "session-1", Name: "过期验证测试"}), client); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: state}), client); err != nil {
			t.Fatalf("Handle(%s) error = %v", state, err)
		}
	}
	challengeResponse, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionEndVerificationCreate, now, struct{}{}), client)
	if err != nil {
		t.Fatalf("Handle(challenge) error = %v", err)
	}
	var challenge EndVerificationChallenge
	decodeTestPayload(t, challengeResponse, &challenge)
	now = challenge.ExpiresUTC.Add(time.Nanosecond)

	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &EndProtectionVerification{Token: challenge.Token, UserName: "Raymond", Password: []byte("correct")},
	}), client)
	if err != nil {
		t.Fatalf("Handle(expired challenge) error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeVerificationRequired)
}

func TestAPIBlocksEndVerificationAfterRepeatedCredentialFailures(t *testing.T) {
	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	challengeNumber := 0
	api := &API{
		coordinator: NewCoordinator(), now: func() time.Time { return now },
		authorizedUserSID: "S-1-5-21-1000", verifier: &testSystemCredentialVerifier{err: errors.New("invalid credentials")},
		newChallenge: func() (string, error) {
			challengeNumber++
			return fmt.Sprintf("challenge-%d", challengeNumber), nil
		},
	}
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{ID: "session-1", Name: "锁定测试"}), client); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: state}), client); err != nil {
			t.Fatalf("Handle(%s) error = %v", state, err)
		}
	}
	for attempt := 0; attempt < maximumEndVerificationFailures; attempt++ {
		challengeResponse, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionEndVerificationCreate, now, struct{}{}), client)
		if err != nil {
			t.Fatalf("Handle(challenge %d) error = %v", attempt, err)
		}
		var challenge EndVerificationChallenge
		decodeTestPayload(t, challengeResponse, &challenge)
		response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
			State:        domain.SessionStateFinalizing,
			Verification: &EndProtectionVerification{Token: challenge.Token, UserName: "Raymond", Password: []byte("wrong")},
		}), client)
		if err != nil {
			t.Fatalf("Handle(failed verification %d) error = %v", attempt, err)
		}
		assertAPIError(t, response, ErrorCodeVerificationFailed)
	}
	locked, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionEndVerificationCreate, now, struct{}{}), client)
	if err != nil {
		t.Fatalf("Handle(locked challenge) error = %v", err)
	}
	assertAPIError(t, locked, ErrorCodeVerificationLocked)
}

func TestAPIManagesAndVerifiesLocalInputShieldCredentials(t *testing.T) {
	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	policy.InputShield.CredentialMode = domain.InputShieldCredentialLocal
	policy.InputShield.AllowRecoveryCode = true
	if _, err := coordinator.CreatePersistedWithMonitoringPolicy("shield-session", "输入防护", policy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	credentialStore := &memoryInputShieldCredentialStore{}
	manager := testInputShieldCredentialManager(credentialStore, &now)
	api := &API{coordinator: coordinator, now: func() time.Time { return now }, authorizedUserSID: "S-1-5-21-1000"}
	api.SetInputShieldCredentialManager(manager)
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}

	updated, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputShieldCredentialUpdate, now,
		InputShieldCredentialUpdateRequest{Password: []byte("correct-password"), EnableRecovery: true}), client)
	if err != nil {
		t.Fatal(err)
	}
	var updateResult InputShieldCredentialResult
	decodeTestPayload(t, updated, &updateResult)
	if !updateResult.Configured || !updateResult.RecoveryCodeEnabled || updateResult.RecoveryCode == "" {
		t.Fatalf("update result = %+v", updateResult)
	}
	verified, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputShieldCredentialVerify, now,
		InputShieldCredentialVerifyRequest{Password: []byte("correct-password")}), client)
	if err != nil {
		t.Fatal(err)
	}
	var verifyResult InputShieldCredentialResult
	decodeTestPayload(t, verified, &verifyResult)
	if !verifyResult.Verified {
		t.Fatalf("verify result = %+v", verifyResult)
	}
}

func TestAPILocalInputShieldVerificationEndsSessionWhenConfigured(t *testing.T) {
	now := time.Date(2026, time.September, 15, 12, 30, 0, 0, time.UTC)
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	policy.InputShield.CredentialMode = domain.InputShieldCredentialLocal
	policy.InputShield.UnlockAction = domain.InputShieldUnlockActionEndSession
	if _, err := coordinator.CreatePersistedWithMonitoringPolicy("shield-session", "输入防护结束会话", policy, nil); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := coordinator.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	credentialStore := &memoryInputShieldCredentialStore{}
	manager := testInputShieldCredentialManager(credentialStore, &now)
	if _, err := manager.SetPassword(context.Background(), []byte("correct-password"), false); err != nil {
		t.Fatal(err)
	}
	api := &API{coordinator: coordinator, now: func() time.Time { return now }, authorizedUserSID: "S-1-5-21-1000"}
	api.SetInputShieldCredentialManager(manager)
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}

	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputShieldCredentialVerify, now,
		InputShieldCredentialVerifyRequest{Password: []byte("correct-password")}), client)
	if err != nil {
		t.Fatal(err)
	}
	var result InputShieldCredentialResult
	decodeTestPayload(t, response, &result)
	if !result.Verified {
		t.Fatalf("verify result = %+v", result)
	}
	session, ok := coordinator.Current()
	if !ok || session.State != domain.SessionStateCompleted {
		t.Fatalf("session after input shield verification = %+v, exists = %v; want completed", session, ok)
	}
}

func TestAPIUsesSessionLimitsForWindowsInputShieldVerification(t *testing.T) {
	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	policy.InputShield.MaxFailedUnlockAttempts = 2
	policy.InputShield.UnlockLockoutSeconds = 30
	if _, err := coordinator.CreatePersistedWithMonitoringPolicy("shield-session", "输入防护", policy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	api := &API{
		coordinator: coordinator, now: func() time.Time { return now }, authorizedUserSID: "S-1-5-21-1000",
		verifier: &testSystemCredentialVerifier{err: errors.New("invalid credentials")},
	}
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}
	for attempt := 1; attempt <= 2; attempt++ {
		response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputShieldCredentialVerify, now,
			InputShieldCredentialVerifyRequest{UserName: "Raymond", Password: []byte("wrong-password")}), client)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 1 {
			assertAPIError(t, response, ErrorCodeVerificationFailed)
		} else {
			assertAPIError(t, response, ErrorCodeVerificationLocked)
		}
	}
}

func TestAPIRecordsPolicyFilteredInputShieldReportAndStatus(t *testing.T) {
	now := time.Date(2026, time.September, 15, 13, 0, 0, 0, time.UTC)
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	if _, err := coordinator.CreatePersistedWithMonitoringPolicy("shield-session", "输入防护", policy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	api := &API{coordinator: coordinator, now: func() time.Time { return now }, authorizedUserSID: "S-1-5-21-1000"}
	api.SetAgentExecutable(`C:\Program Files\DesktopGuardPro\desktop-guard-agent.exe`)
	var recorded AgentInputShieldReportRequest
	api.SetAgentInputShieldHandler(func(_ context.Context, _ ClientIdentity, report AgentInputShieldReportRequest) error {
		recorded = report
		return nil
	})
	client := ClientIdentity{
		UserSID: "S-1-5-21-1000", WindowsSessionID: 2,
		ImagePath: `C:\Program Files\DesktopGuardPro\desktop-guard-agent.exe`,
	}
	x, y := int32(120), int32(240)
	lastActive := now.Add(-time.Second)
	devices := []AgentInputShieldDevice{{
		Kind: "keyboard", InterfacePath: `\\?\HID#VID_1234&PID_5678`, InstanceID: `HID\VID_1234&PID_5678\1`,
		VendorID: "1234", ProductID: "5678", Active: true, LastActiveUTC: lastActive,
	}}
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentInputShieldReport, now,
		AgentInputShieldReportRequest{
			SessionID: "shield-session", Action: "input_blocked", State: "protecting", HookRunning: true,
			InputKind: "keyboard", KeyCode: 65, PointerX: &x, PointerY: &y, Devices: devices, ObservedUTC: now,
		}), client)
	if err != nil || response.Type != contracts.MessageTypeAgentInputShieldResult {
		t.Fatalf("report response = %q, error = %v", response.Type, err)
	}
	if recorded.InputKind != "keyboard" || recorded.KeyCode != 0 || recorded.PointerX != nil || recorded.PointerY != nil {
		t.Fatalf("policy-filtered report = %+v", recorded)
	}
	statusResponse, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputShieldStatusGet, now, struct{}{}), client)
	if err != nil {
		t.Fatal(err)
	}
	var status InputShieldStatusResult
	decodeTestPayload(t, statusResponse, &status)
	if status.SessionID != "shield-session" || status.State != "protecting" || !status.HookRunning || !status.ObservedUTC.Equal(now) ||
		!reflect.DeepEqual(status.Devices, devices) {
		t.Fatalf("input shield status = %+v", status)
	}
}

func TestAPIInputShieldStatusDegradesAfterMissedHeartbeats(t *testing.T) {
	now := time.Date(2026, time.September, 15, 13, 30, 0, 0, time.UTC)
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	policy.InputShield.HookHeartbeatSeconds = 2
	if _, err := coordinator.CreatePersistedWithMonitoringPolicy("shield-session", "输入防护心跳", policy, nil); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := coordinator.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	api := &API{coordinator: coordinator, now: func() time.Time { return now }}
	api.inputShieldStatus = InputShieldStatusResult{
		SessionID: "shield-session", State: "protecting", HookRunning: true, ObservedUTC: now.Add(-7 * time.Second),
	}

	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeInputShieldStatusGet, now, struct{}{}))
	if err != nil {
		t.Fatal(err)
	}
	var status InputShieldStatusResult
	decodeTestPayload(t, response, &status)
	if status.State != "degraded" || status.HookRunning {
		t.Fatalf("stale input shield status = %+v, want degraded with stopped hook", status)
	}
}

func TestAPIManagesStandaloneInputControlWithoutChangingProtectionSession(t *testing.T) {
	now := time.Date(2026, time.September, 16, 8, 0, 0, 0, time.UTC)
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	if _, err := coordinator.CreatePersistedWithMonitoringPolicy("audit-session", "标准保护", policy, nil); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if _, err := coordinator.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	api := &API{
		coordinator: coordinator, now: func() time.Time { return now }, authorizedUserSID: "S-1-5-21-1000",
		newChallenge: func() (string, error) { return "input-task-1", nil },
	}
	api.SetAgentExecutable(`C:\Program Files\DesktopGuardPro\desktop-guard-agent.exe`)
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}
	shield := domain.DefaultMonitoringPolicy().InputShield
	shield.BlockPhysicalKeyboard = true
	shield.BlockPhysicalMouse = false
	shield.BlockPointerMovement = false

	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputControlStart, now,
		InputControlStartRequest{Policy: shield, DurationMinutes: 15}), client)
	if err != nil {
		t.Fatal(err)
	}
	var started InputControlResult
	decodeTestPayload(t, response, &started)
	if !started.Enabled || started.Source != "temporary" || started.ControlID != "input-task-1" ||
		!started.Policy.BlockPhysicalKeyboard || started.Policy.BlockPhysicalMouse ||
		!started.ExpiresUTC.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("standalone input control = %+v", started)
	}
	session, ok := coordinator.Current()
	if !ok || session.ID != "audit-session" || session.MonitoringPolicy.InputShieldEnabled {
		t.Fatalf("protection session changed by standalone input control: %+v", session)
	}

	recorded := 0
	api.SetAgentInputShieldHandler(func(context.Context, ClientIdentity, AgentInputShieldReportRequest) error {
		recorded++
		return nil
	})
	agent := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2,
		ImagePath: `C:\Program Files\DesktopGuardPro\desktop-guard-agent.exe`}
	response, err = api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentInputShieldReport, now,
		AgentInputShieldReportRequest{SessionID: "input-task-1", Action: "started", State: "protecting", HookRunning: true, ObservedUTC: now}), agent)
	if err != nil || response.Type != contracts.MessageTypeAgentInputShieldResult || recorded != 0 {
		t.Fatalf("standalone agent report response=%+v recorded=%d error=%v", response, recorded, err)
	}

	response, err = api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputControlStop, now, struct{}{}), client)
	if err != nil {
		t.Fatal(err)
	}
	var stopped InputControlResult
	decodeTestPayload(t, response, &stopped)
	if stopped.Enabled || stopped.State != "disabled" {
		t.Fatalf("stopped input control = %+v", stopped)
	}
	session, ok = coordinator.Current()
	if !ok || session.State != domain.SessionStateActive {
		t.Fatalf("stopping standalone input control changed protection session: %+v", session)
	}
}

func TestAPIStandaloneInputControlExpiresWithoutProtectionSession(t *testing.T) {
	now := time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)
	api := &API{coordinator: NewCoordinator(), now: func() time.Time { return now }, authorizedUserSID: "owner",
		newChallenge: func() (string, error) { return "input-task-expiring", nil }}
	client := ClientIdentity{UserSID: "owner"}
	shield := domain.DefaultMonitoringPolicy().InputShield
	shield.BlockPhysicalKeyboard = false
	shield.BlockPhysicalMouse = true
	shield.BlockPointerMovement = true
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputControlStart, now,
		InputControlStartRequest{Policy: shield, DurationMinutes: 1}), client); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputControlGet, now, struct{}{}), client)
	if err != nil {
		t.Fatal(err)
	}
	var result InputControlResult
	decodeTestPayload(t, response, &result)
	if result.Enabled || result.State != "disabled" {
		t.Fatalf("expired standalone input control = %+v", result)
	}
}

func TestAPIStandaloneInputControlCanRunUntilExplicitUnlock(t *testing.T) {
	now := time.Date(2026, time.September, 17, 9, 0, 0, 0, time.UTC)
	api := &API{coordinator: NewCoordinator(), now: func() time.Time { return now }, authorizedUserSID: "owner",
		newChallenge: func() (string, error) { return "input-task-until-unlock", nil }}
	client := ClientIdentity{UserSID: "owner"}
	shield := domain.DefaultMonitoringPolicy().InputShield
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputControlStart, now,
		InputControlStartRequest{Policy: shield, Indefinite: true}), client)
	if err != nil {
		t.Fatal(err)
	}
	var started InputControlResult
	decodeTestPayload(t, response, &started)
	if !started.Enabled || !started.Indefinite || !started.ExpiresUTC.IsZero() {
		t.Fatalf("indefinite input control = %+v", started)
	}

	now = now.Add(365 * 24 * time.Hour)
	response, err = api.HandleForClient(newTestMessage(t, contracts.MessageTypeInputControlGet, now, struct{}{}), client)
	if err != nil {
		t.Fatal(err)
	}
	var current InputControlResult
	decodeTestPayload(t, response, &current)
	if !current.Enabled || !current.Indefinite || current.ControlID != "input-task-until-unlock" {
		t.Fatalf("input control did not remain active until unlock: %+v", current)
	}
}

func TestAPISessionLifecycle(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	create := newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID:   "session-1",
		Name: "离席保护",
	})
	createResponse, err := api.Handle(create)
	if err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	assertSessionResult(t, createResponse, domain.SessionStateDraft)

	transition := newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State: domain.SessionStatePreparing,
	})
	transitionResponse, err := api.Handle(transition)
	if err != nil {
		t.Fatalf("Handle(transition) error = %v", err)
	}
	assertSessionResult(t, transitionResponse, domain.SessionStatePreparing)

	current := newTestMessage(t, contracts.MessageTypeSessionCurrentGet, now, struct{}{})
	currentResponse, err := api.Handle(current)
	if err != nil {
		t.Fatalf("Handle(current) error = %v", err)
	}
	assertSessionResult(t, currentResponse, domain.SessionStatePreparing)
}

func TestAPIRejectsExpiredRequest(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	request, err := contracts.NewMessage(
		"request-1",
		contracts.MessageTypeHealthGet,
		now.Add(-time.Second),
		struct{}{},
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	response, err := api.Handle(request)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeRequestExpired)
}

func TestAPIAuthorizesInstalledOwnerSID(t *testing.T) {
	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	api := &API{
		coordinator: NewCoordinator(), now: func() time.Time { return now },
		authorizedUserSID: "S-1-5-21-1000",
	}
	request := newTestMessage(t, contracts.MessageTypeHealthGet, now, struct{}{})

	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "s-1-5-21-1000", WindowsSessionID: 2})
	if err != nil {
		t.Fatalf("HandleForClient() error = %v", err)
	}
	if response.Type != contracts.MessageTypeHealthResult {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeHealthResult)
	}
}

func TestAPIRejectsDifferentUserSID(t *testing.T) {
	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	api := &API{
		coordinator: NewCoordinator(), now: func() time.Time { return now },
		authorizedUserSID: "S-1-5-21-1000",
	}
	request := newTestMessage(t, contracts.MessageTypeTimelineQuery, now, struct{}{})

	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "S-1-5-21-2000", WindowsSessionID: 2})
	if err != nil {
		t.Fatalf("HandleForClient() error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeUnauthorized)
}

func TestAPIRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	request := newTestMessage(t, contracts.MessageTypeSessionCreate, now, struct{}{})
	request.Payload = json.RawMessage(`{"id":`)

	response, err := api.Handle(request)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeInvalidPayload)
}

func TestAPIReportsOverlappingSession(t *testing.T) {
	t.Parallel()

	api, now := newTestAPI()
	first := newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-1", Name: "离席保护",
	})
	if _, err := api.Handle(first); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}

	second := newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-2", Name: "设备借用",
	})
	response, err := api.Handle(second)
	if err != nil {
		t.Fatalf("Handle(second) error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeSessionInProgress)
}

func TestAPIUpdatesRetentionLockAndPrunesTerminalSessions(t *testing.T) {
	now := time.Date(2026, time.September, 7, 4, 0, 0, 0, time.UTC)
	store := &testSessionStore{pruned: []string{"expired-session"}}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}
	lockResponse, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionRetentionLockUpdate, now, SessionRetentionLockRequest{
		SessionID: "protected-session", Locked: true,
	}))
	if err != nil {
		t.Fatalf("Handle(lock) error = %v", err)
	}
	if lockResponse.Type != contracts.MessageTypeSessionRetentionLockResult || !store.retentionLocks["protected-session"] {
		t.Fatalf("lock response=%q locks=%#v", lockResponse.Type, store.retentionLocks)
	}
	pruneResponse, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionRetentionPrune, now, SessionRetentionPruneRequest{
		BeforeUTC: now.Add(-24 * time.Hour), Limit: 10,
	}))
	if err != nil {
		t.Fatalf("Handle(prune) error = %v", err)
	}
	if pruneResponse.Type != contracts.MessageTypeSessionRetentionPruneResult {
		t.Fatalf("prune response type = %q", pruneResponse.Type)
	}
	var result SessionRetentionPruneResult
	decodeTestPayload(t, pruneResponse, &result)
	if len(result.DeletedSessionIDs) != 1 || result.DeletedSessionIDs[0] != "expired-session" ||
		store.pruneBefore != now.Add(-24*time.Hour) || store.pruneLimit != 10 {
		t.Fatalf("prune result=%+v store=%+v", result, store)
	}
}

func TestAPIReturnsSessionStartPreviewForSelectedDirectories(t *testing.T) {
	now := time.Date(2026, time.September, 10, 8, 0, 0, 0, time.UTC)
	store := &testSessionStore{directories: []string{`C:\\Evidence`, `D:\\Case Files`}}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}

	for _, test := range []struct {
		name                  string
		level                 domain.MonitoringLevel
		requiresAdministrator bool
		eventVolume           domain.MonitoringImpactLevel
	}{
		{
			name:        "standard",
			level:       domain.MonitoringLevelStandard,
			eventVolume: domain.MonitoringImpactMedium,
		},
		{
			name:                  "strict",
			level:                 domain.MonitoringLevelStrict,
			requiresAdministrator: true,
			eventVolume:           domain.MonitoringImpactHigh,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionStartPreviewGet, now, SessionStartPreviewRequest{
				MonitoringLevel: test.level,
			}))
			if err != nil {
				t.Fatalf("Handle(start preview) error = %v", err)
			}
			if response.Type != contracts.MessageTypeSessionStartPreviewResult {
				t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeSessionStartPreviewResult)
			}

			var result SessionStartPreviewResult
			decodeTestPayload(t, response, &result)
			if result.MonitoredTargetCount != 2 || result.MonitoringLevel != test.level {
				t.Fatalf("preview = %+v, want level %q and 2 targets", result, test.level)
			}
			if result.Impact.RequiresAdministrator != test.requiresAdministrator ||
				result.Impact.ExpectedEventVolume != test.eventVolume {
				t.Fatalf("impact = %+v", result.Impact)
			}
		})
	}
}

func TestAPICreatesSessionWithSelectedMonitoringLevel(t *testing.T) {
	now := time.Date(2026, time.September, 10, 8, 0, 0, 0, time.UTC)
	store := &testSessionStore{}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-strict", Name: "严格保护", MonitoringLevel: domain.MonitoringLevelStrict,
	}))
	if err != nil {
		t.Fatalf("Handle(create strict session) error = %v", err)
	}
	var result SessionResult
	decodeTestPayload(t, response, &result)
	if result.Session == nil || result.Session.MonitoringLevel != domain.MonitoringLevelStrict ||
		len(store.created) != 1 || store.created[0].MonitoringLevel != domain.MonitoringLevelStrict {
		t.Fatalf("created strict session result=%+v stored=%#v", result, store.created)
	}
}

func TestAPICreatesSessionWithSelectedBuiltInMonitoringMode(t *testing.T) {
	now := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)
	store := &testSessionStore{}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-relaxed", Name: "宽松保护", MonitoringMode: domain.MonitoringModeRelaxed,
	}))
	if err != nil {
		t.Fatalf("Handle(create relaxed session) error = %v", err)
	}
	var result SessionResult
	decodeTestPayload(t, response, &result)
	if result.Session == nil || result.Session.MonitoringPolicy.Mode != domain.MonitoringModeRelaxed ||
		!result.Session.MonitoringPolicy.FileActivityEnabled || result.Session.MonitoringPolicy.ProcessAndSoftwareEnabled ||
		len(store.created) != 1 || store.created[0].MonitoringPolicy.Mode != domain.MonitoringModeRelaxed {
		t.Fatalf("created relaxed session result=%+v stored=%#v", result, store.created)
	}
}

func TestAPISessionStartPreviewUsesSelectedMonitoringMode(t *testing.T) {
	now := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)
	store := &testSessionStore{directories: []string{`C:\Evidence`}}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionStartPreviewGet, now, SessionStartPreviewRequest{
		MonitoringMode: domain.MonitoringModeStrict,
	}))
	if err != nil {
		t.Fatalf("Handle(strict mode preview) error = %v", err)
	}
	var result SessionStartPreviewResult
	decodeTestPayload(t, response, &result)
	if result.MonitoringMode != domain.MonitoringModeStrict || result.MonitoringLevel != domain.MonitoringLevelStrict ||
		!result.Impact.RequiresAdministrator || result.MonitoringPolicy.Mode != domain.MonitoringModeStrict ||
		!result.MonitoringPolicy.StrictReadAuditEnabled || result.MonitoringPolicy.InputShieldEnabled {
		t.Fatalf("strict mode preview = %+v", result)
	}
}

func TestAPISessionStartPreviewCountsFileAndRemovableVolumeTargets(t *testing.T) {
	now := time.Date(2026, time.September, 10, 8, 0, 0, 0, time.UTC)
	store := &testSessionStore{targets: []domain.MonitoringTarget{
		{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
		{Path: `C:\Evidence\summary.txt`, Kind: domain.MonitoringTargetKindFile},
		{Path: `E:\`, Kind: domain.MonitoringTargetKindRemovableVolume},
	}}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionStartPreviewGet, now, SessionStartPreviewRequest{
		MonitoringLevel: domain.MonitoringLevelStandard,
	}))
	if err != nil {
		t.Fatalf("Handle(start preview) error = %v", err)
	}
	var result SessionStartPreviewResult
	decodeTestPayload(t, response, &result)
	if result.MonitoredTargetCount != 3 || len(result.Targets) != 3 ||
		result.Targets[1].Kind != domain.MonitoringTargetKindFile || result.Targets[2].Kind != domain.MonitoringTargetKindRemovableVolume {
		t.Fatalf("preview target count = %d, want 3", result.MonitoredTargetCount)
	}
}

func TestAPIRejectsSessionStartPreviewWithUnknownMonitoringLevel(t *testing.T) {
	now := time.Date(2026, time.September, 10, 8, 0, 0, 0, time.UTC)
	store := &testSessionStore{}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}

	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionStartPreviewGet, now, SessionStartPreviewRequest{
		MonitoringLevel: domain.MonitoringLevel("unsupported"),
	}))
	if err != nil {
		t.Fatalf("Handle(start preview) error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeInvalidPayload)
}

type testSessionStore struct {
	created        []domain.Session
	updated        []domain.Session
	events         []domain.AuditEvent
	eventPayloads  [][]byte
	atomicEvents   int
	review         storage.BaselineReview
	reviewErr      error
	err            error
	directories    []string
	targets        []domain.MonitoringTarget
	retentionLocks map[string]bool
	pruned         []string
	pruneBefore    time.Time
	pruneLimit     int
}

type testSessionLifecycle struct {
	coordinator    *Coordinator
	observedStates chan domain.SessionState
	activeWait     func() error
	activeStarted  chan struct{}
	activeRelease  chan struct{}
	stoppedStarted chan struct{}
	stoppedRelease chan struct{}
}

type testSystemCredentialVerifier struct {
	err   error
	calls int
}

func (verifier *testSystemCredentialVerifier) VerifySystemCredentials(
	context.Context,
	SystemCredentials,
	string,
) error {
	verifier.calls++
	return verifier.err
}

type apiCallResult struct {
	response contracts.Message
	err      error
}

func (lifecycle *testSessionLifecycle) WaitForActive(ctx context.Context, _ string) error {
	if lifecycle.activeWait != nil {
		return lifecycle.activeWait()
	}
	if session, ok := lifecycle.coordinator.Current(); ok {
		lifecycle.observedStates <- session.State
	}
	close(lifecycle.activeStarted)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lifecycle.activeRelease:
		return nil
	}
}

func (lifecycle *testSessionLifecycle) WaitForStopped(ctx context.Context, _ string) error {
	if session, ok := lifecycle.coordinator.Current(); ok {
		lifecycle.observedStates <- session.State
	}
	close(lifecycle.stoppedStarted)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lifecycle.stoppedRelease:
		return nil
	}
}

func waitAPIChannel(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle signal")
	}
}

func (store *testSessionStore) CreateSession(_ context.Context, session domain.Session, _ time.Time) error {
	if store.err != nil {
		return store.err
	}
	store.created = append(store.created, session)
	return nil
}

func (store *testSessionStore) UpdateSession(_ context.Context, session domain.Session, _ time.Time) error {
	if store.err != nil {
		return store.err
	}
	store.updated = append(store.updated, session)
	return nil
}

func (store *testSessionStore) AppendEventAutoSequence(_ context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error) {
	if store.err != nil {
		return domain.AuditEvent{}, store.err
	}
	event.Sequence = uint64(len(store.events) + 1)
	store.events = append(store.events, event)
	store.eventPayloads = append(store.eventPayloads, append([]byte(nil), payload...))
	return event, nil
}

func (store *testSessionStore) UpdateSessionAndAppendEvent(
	ctx context.Context,
	session domain.Session,
	at time.Time,
	event domain.AuditEvent,
	payload []byte,
) (domain.AuditEvent, error) {
	if err := store.UpdateSession(ctx, session, at); err != nil {
		return domain.AuditEvent{}, err
	}
	store.atomicEvents++
	return store.AppendEventAutoSequence(ctx, event, payload)
}

func (store *testSessionStore) LoadBaselineReview(context.Context, string) (storage.BaselineReview, error) {
	if store.reviewErr != nil {
		return storage.BaselineReview{}, store.reviewErr
	}
	return store.review, nil
}

func (store *testSessionStore) ResolveBaselineReviewAndUpdateSessionAndAppendEvent(
	ctx context.Context,
	session domain.Session,
	at time.Time,
	resolution storage.BaselineReviewResolution,
	event domain.AuditEvent,
	payload []byte,
) (domain.AuditEvent, error) {
	if store.review.Resolution != "" {
		return domain.AuditEvent{}, storage.ErrBaselineReviewResolved
	}
	if err := store.UpdateSession(ctx, session, at); err != nil {
		return domain.AuditEvent{}, err
	}
	store.review.Resolution = resolution
	store.atomicEvents++
	return store.AppendEventAutoSequence(ctx, event, payload)
}

func (store *testSessionStore) LoadMonitoredDirectories(context.Context) ([]string, error) {
	if store.err != nil {
		return nil, store.err
	}
	return append([]string(nil), store.directories...), nil
}

func (store *testSessionStore) LoadMonitoringTargets(context.Context) ([]domain.MonitoringTarget, error) {
	if store.err != nil {
		return nil, store.err
	}
	return domain.CloneMonitoringTargets(store.targets), nil
}

func (store *testSessionStore) SaveMonitoringTargets(_ context.Context, targets []domain.MonitoringTarget) error {
	if store.err != nil {
		return store.err
	}
	store.targets = domain.CloneMonitoringTargets(targets)
	return nil
}

func (store *testSessionStore) SaveMonitoredDirectories(_ context.Context, directories []string) error {
	if store.err != nil {
		return store.err
	}
	store.directories = append([]string(nil), directories...)
	return nil
}

func (store *testSessionStore) SetSessionRetentionLock(_ context.Context, sessionID string, locked bool) error {
	if store.err != nil {
		return store.err
	}
	if store.retentionLocks == nil {
		store.retentionLocks = make(map[string]bool)
	}
	store.retentionLocks[sessionID] = locked
	return nil
}

func (store *testSessionStore) PruneTerminalSessions(_ context.Context, before time.Time, limit int) ([]string, error) {
	if store.err != nil {
		return nil, store.err
	}
	store.pruneBefore, store.pruneLimit = before, limit
	return append([]string(nil), store.pruned...), nil
}

func TestAPIPersistsSessionLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	store := &testSessionStore{}
	api := &API{coordinator: NewCoordinator(), store: store, now: func() time.Time { return now }}

	create := newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-1", Name: "持久化会话",
	})
	if _, err := api.Handle(create); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	transition := newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State: domain.SessionStatePreparing,
	})
	if _, err := api.Handle(transition); err != nil {
		t.Fatalf("Handle(transition) error = %v", err)
	}
	if len(store.created) != 1 || len(store.updated) != 1 {
		t.Fatalf("store calls = created:%d updated:%d, want 1 each", len(store.created), len(store.updated))
	}
	if store.updated[0].State != domain.SessionStatePreparing || store.updated[0].Revision != 1 {
		t.Fatalf("persisted transition = %+v", store.updated[0])
	}
}

func TestAPIRecordsLifecycleEventsForProtectionSession(t *testing.T) {
	now := time.Date(2026, time.September, 10, 9, 0, 0, 0, time.UTC)
	store := &testSessionStore{}
	api := &API{
		coordinator:  NewCoordinator(),
		store:        store,
		now:          func() time.Time { return now },
		verifier:     &testSystemCredentialVerifier{},
		newChallenge: func() (string, error) { return "challenge-1", nil },
	}
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 2}
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-1", Name: "审计事件测试",
	}), client); err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	for _, state := range []domain.SessionState{
		domain.SessionStatePreparing,
		domain.SessionStateActive,
		domain.SessionStatePaused,
		domain.SessionStateActive,
		domain.SessionStateDegraded,
		domain.SessionStateActive,
	} {
		if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{State: state}), client); err != nil {
			t.Fatalf("Handle(%s) error = %v", state, err)
		}
	}
	challengeResponse, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionEndVerificationCreate, now, struct{}{}), client)
	if err != nil {
		t.Fatalf("Handle(end verification) error = %v", err)
	}
	var challenge EndVerificationChallenge
	decodeTestPayload(t, challengeResponse, &challenge)
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &EndProtectionVerification{Token: challenge.Token, UserName: "Raymond", Password: []byte("test-password")},
	}), client); err != nil {
		t.Fatalf("Handle(finalizing) error = %v", err)
	}
	if _, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeSessionTransition, now, TransitionSessionRequest{
		State: domain.SessionStateCompleted,
	}), client); err != nil {
		t.Fatalf("Handle(completed) error = %v", err)
	}

	wantActions := []string{
		"session_created", "protection_preparing", "protection_started", "protection_paused", "protection_resumed", "protection_interrupted",
		"protection_resumed", "protection_ending", "protection_ended",
	}
	if len(store.events) != len(wantActions) {
		t.Fatalf("event count = %d, want %d", len(store.events), len(wantActions))
	}
	if store.atomicEvents != len(wantActions)-1 {
		t.Fatalf("atomic transition event count = %d, want %d", store.atomicEvents, len(wantActions)-1)
	}
	for index, wantAction := range wantActions {
		if store.events[index].Action != wantAction || store.events[index].Sequence != uint64(index+1) {
			t.Fatalf("event %d = %+v, want action %q and sequence %d", index, store.events[index], wantAction, index+1)
		}
	}
	var payload struct {
		PreviousState domain.SessionState `json:"previousState"`
		CurrentState  domain.SessionState `json:"currentState"`
	}
	if err := json.Unmarshal(store.eventPayloads[len(store.eventPayloads)-1], &payload); err != nil {
		t.Fatalf("decode final lifecycle payload: %v", err)
	}
	if payload.PreviousState != domain.SessionStateFinalizing || payload.CurrentState != domain.SessionStateCompleted {
		t.Fatalf("final lifecycle payload = %+v", payload)
	}
}

func TestAPIReturnsPendingBaselineReviewFailures(t *testing.T) {
	now := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	store := &testSessionStore{review: storage.BaselineReview{
		SessionID: "session-1",
		Decision: domain.BaselineStartDecision{
			Status:             domain.BaselineCaptureStatusPartialFailure,
			Failures:           []domain.BaselineCaptureFailure{{Item: `D:\Evidence`, Reason: "access denied"}},
			RequiresUserChoice: true, CanContinue: true, CanCancel: true,
		},
	}}
	coordinator := NewCoordinator()
	if err := coordinator.Restore(domain.Session{ID: "session-1", Name: "基线审查", State: domain.SessionStateBaselineReview, Revision: 2}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	api := &API{coordinator: coordinator, store: store, now: func() time.Time { return now }}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionBaselineReviewGet, now, SessionBaselineReviewGetRequest{SessionID: "session-1"}))
	if err != nil {
		t.Fatalf("Handle(baseline review get) error = %v", err)
	}
	if response.Type != contracts.MessageTypeSessionBaselineReviewResult {
		t.Fatalf("response type = %q", response.Type)
	}
	var result SessionBaselineReviewResult
	decodeTestPayload(t, response, &result)
	if result.SessionID != "session-1" || len(result.Decision.Failures) != 1 || result.Decision.Failures[0].Reason != "access denied" {
		t.Fatalf("baseline review result = %#v", result)
	}
}

func TestAPIResolvesBaselineReviewAndTransitionsSession(t *testing.T) {
	now := time.Date(2026, time.September, 10, 10, 30, 0, 0, time.UTC)
	store := &testSessionStore{review: storage.BaselineReview{
		SessionID: "session-1",
		Decision: domain.BaselineStartDecision{
			Status: domain.BaselineCaptureStatusPartialFailure, RequiresUserChoice: true, CanContinue: true, CanCancel: true,
		},
	}}
	coordinator := NewCoordinator()
	if err := coordinator.Restore(domain.Session{ID: "session-1", Name: "基线审查", State: domain.SessionStateBaselineReview, Revision: 2}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	api := &API{coordinator: coordinator, store: store, now: func() time.Time { return now }}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionBaselineReviewResolve, now, SessionBaselineReviewResolveRequest{
		SessionID: "session-1", Resolution: storage.BaselineReviewResolutionContinue,
	}))
	if err != nil {
		t.Fatalf("Handle(baseline review resolve) error = %v", err)
	}
	if response.Type != contracts.MessageTypeSessionBaselineReviewResult {
		t.Fatalf("response type = %q", response.Type)
	}
	var result SessionBaselineReviewResult
	decodeTestPayload(t, response, &result)
	if result.Resolution != storage.BaselineReviewResolutionContinue || result.Session == nil || result.Session.State != domain.SessionStateActive {
		t.Fatalf("baseline resolution result = %#v", result)
	}
	if len(store.events) != 1 || store.events[0].Action != "protection_started" || store.atomicEvents != 1 {
		t.Fatalf("resolution persistence = events:%#v atomic:%d", store.events, store.atomicEvents)
	}
}

func TestAPIKeepsCoordinatorStateWhenStorageFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	store := &testSessionStore{err: errors.New("disk full")}
	coordinator := NewCoordinator()
	api := &API{coordinator: coordinator, store: store, now: func() time.Time { return now }}
	create := newTestMessage(t, contracts.MessageTypeSessionCreate, now, CreateSessionRequest{
		ID: "session-1", Name: "失败会话",
	})

	response, err := api.Handle(create)
	if err != nil {
		t.Fatalf("Handle(create) error = %v", err)
	}
	assertAPIError(t, response, ErrorCodeStorageFailure)
	if _, ok := coordinator.Current(); ok {
		t.Fatal("Current() ok = true after failed storage write")
	}
}

func newTestAPI() (*API, time.Time) {
	now := time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC)
	api := newAPIWithClock(NewCoordinator(), func() time.Time { return now })
	return api, now
}

func newTestMessage(
	t *testing.T,
	messageType contracts.MessageType,
	now time.Time,
	payload any,
) contracts.Message {
	t.Helper()

	message, err := contracts.NewMessage("request-1", messageType, now.Add(time.Second), payload)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	return message
}

func decodeTestPayload(t *testing.T, message contracts.Message, target any) {
	t.Helper()

	if err := message.DecodePayload(target); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
}

func assertSessionResult(t *testing.T, response contracts.Message, want domain.SessionState) {
	t.Helper()

	if response.Type != contracts.MessageTypeSessionResult {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeSessionResult)
	}
	var result SessionResult
	decodeTestPayload(t, response, &result)
	if result.Session == nil {
		t.Fatal("session result is nil")
	}
	if result.Session.State != want {
		t.Fatalf("session state = %q, want %q", result.Session.State, want)
	}
}

func assertAPIError(t *testing.T, response contracts.Message, want string) {
	t.Helper()

	if response.Type != contracts.MessageTypeError {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeError)
	}
	var result ErrorResult
	decodeTestPayload(t, response, &result)
	if result.Code != want {
		t.Fatalf("error code = %q, want %q", result.Code, want)
	}
}
