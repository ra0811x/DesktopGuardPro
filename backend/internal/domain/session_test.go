package domain

import (
	"errors"
	"testing"
)

func TestDefaultMonitoringProfileUsesStandardLevel(t *testing.T) {
	profile := DefaultMonitoringProfile()

	if profile.Level != MonitoringLevelStandard {
		t.Fatalf("default level = %q, want %q", profile.Level, MonitoringLevelStandard)
	}

	preview, err := profile.StartPreview(2)
	if err != nil {
		t.Fatalf("build default preview: %v", err)
	}
	if preview.MonitoredTargetCount != 2 {
		t.Fatalf("target count = %d, want 2", preview.MonitoredTargetCount)
	}
	if preview.Impact.RequiresAdministrator {
		t.Fatal("standard mode should not require administrator privileges")
	}
	if preview.Impact.PerformanceImpact != MonitoringImpactLow {
		t.Fatalf("standard performance impact = %q, want %q", preview.Impact.PerformanceImpact, MonitoringImpactLow)
	}
}

func TestStrictMonitoringProfileBuildsElevatedPreview(t *testing.T) {
	profile := MonitoringProfile{Level: MonitoringLevelStrict}

	preview, err := profile.StartPreview(3)
	if err != nil {
		t.Fatalf("build strict preview: %v", err)
	}
	if !preview.Impact.RequiresAdministrator {
		t.Fatal("strict mode should require administrator privileges")
	}
	if preview.Impact.ExpectedEventVolume != MonitoringImpactHigh {
		t.Fatalf("strict event volume = %q, want %q", preview.Impact.ExpectedEventVolume, MonitoringImpactHigh)
	}
}

func TestMonitoringProfileRejectsUnknownLevel(t *testing.T) {
	profile := MonitoringProfile{Level: MonitoringLevel("unknown")}

	if err := profile.Validate(); !errors.Is(err, ErrInvalidMonitoringLevel) {
		t.Fatalf("validation error = %v, want %v", err, ErrInvalidMonitoringLevel)
	}
}

func TestMonitoringPreviewRejectsNegativeTargetCount(t *testing.T) {
	profile := DefaultMonitoringProfile()

	if _, err := profile.StartPreview(-1); !errors.Is(err, ErrInvalidMonitoredTargetCount) {
		t.Fatalf("preview error = %v, want %v", err, ErrInvalidMonitoredTargetCount)
	}
}

func TestNewSessionStartsInDraft(t *testing.T) {
	t.Parallel()

	session, err := NewSession("session-1", "离席保护")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	if session.ID != "session-1" {
		t.Fatalf("session.ID = %q, want %q", session.ID, "session-1")
	}
	if session.Name != "离席保护" {
		t.Fatalf("session.Name = %q, want %q", session.Name, "离席保护")
	}
	if session.State != SessionStateDraft {
		t.Fatalf("session.State = %q, want %q", session.State, SessionStateDraft)
	}
	if session.Revision != 0 {
		t.Fatalf("session.Revision = %d, want 0", session.Revision)
	}
	if session.MonitoringLevel != MonitoringLevelStandard {
		t.Fatalf("session.MonitoringLevel = %q, want %q", session.MonitoringLevel, MonitoringLevelStandard)
	}
}

func TestNewSessionWithMonitoringLevelKeepsSelectedProfile(t *testing.T) {
	session, err := NewSessionWithMonitoringLevel("session-strict", "严格保护", MonitoringLevelStrict)
	if err != nil {
		t.Fatalf("NewSessionWithMonitoringLevel() error = %v", err)
	}
	if session.MonitoringLevel != MonitoringLevelStrict {
		t.Fatalf("session.MonitoringLevel = %q, want %q", session.MonitoringLevel, MonitoringLevelStrict)
	}
	if _, err := NewSessionWithMonitoringLevel("session-invalid", "无效配置", MonitoringLevel("unknown")); !errors.Is(err, ErrInvalidMonitoringLevel) {
		t.Fatalf("invalid monitoring level error = %v, want %v", err, ErrInvalidMonitoringLevel)
	}
}

func TestNewSessionWithMonitoringPolicySnapshotsEnabledCapabilities(t *testing.T) {
	policy := DefaultMonitoringPolicy()
	policy.ProcessAndSoftwareEnabled = false
	policy.StrictReadAuditEnabled = true
	session, err := NewSessionWithMonitoringPolicy("session-policy", "策略快照", policy)
	if err != nil {
		t.Fatalf("NewSessionWithMonitoringPolicy() error = %v", err)
	}
	if session.MonitoringPolicy != policy {
		t.Fatalf("session policy = %+v, want %+v", session.MonitoringPolicy, policy)
	}
	if session.MonitoringLevel != MonitoringLevelStrict {
		t.Fatalf("session level = %q, want %q", session.MonitoringLevel, MonitoringLevelStrict)
	}
}

func TestNewSessionValidatesRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		title   string
		wantErr error
	}{
		{name: "missing id", id: " ", title: "离席保护", wantErr: ErrSessionIDRequired},
		{name: "missing name", id: "session-1", title: " ", wantErr: ErrSessionNameRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewSession(tt.id, tt.title)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewSession() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSessionTransitionLifecycle(t *testing.T) {
	t.Parallel()

	session, err := NewSession("session-1", "维修交接")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	states := []SessionState{
		SessionStatePreparing,
		SessionStateActive,
		SessionStatePaused,
		SessionStateActive,
		SessionStateDegraded,
		SessionStateActive,
		SessionStateFinalizing,
		SessionStateCompleted,
	}

	for _, state := range states {
		if err := session.Transition(state); err != nil {
			t.Fatalf("Transition(%q) error = %v", state, err)
		}
	}

	if session.Revision != uint64(len(states)) {
		t.Fatalf("session.Revision = %d, want %d", session.Revision, len(states))
	}
}

func TestSessionAllowsBaselineReviewContinuationOrCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		next SessionState
	}{
		{name: "continue after partial baseline failure", next: SessionStateActive},
		{name: "cancel after partial baseline failure", next: SessionStateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			session, err := NewSession("session-1", "基线处理")
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []SessionState{SessionStatePreparing, SessionStateBaselineReview, tt.next} {
				if err := session.Transition(state); err != nil {
					t.Fatalf("Transition(%q) error = %v", state, err)
				}
			}
			if session.State != tt.next {
				t.Fatalf("state = %q, want %q", session.State, tt.next)
			}
		})
	}
}

func TestSessionRejectsBaselineReviewAfterProtectionStarts(t *testing.T) {
	t.Parallel()

	session, err := NewSession("session-1", "无效基线处理")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []SessionState{SessionStatePreparing, SessionStateActive} {
		if err := session.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Transition(SessionStateBaselineReview); !errors.Is(err, ErrInvalidSessionTransition) {
		t.Fatalf("Transition(baseline review) error = %v, want %v", err, ErrInvalidSessionTransition)
	}
}

func TestSessionRejectsInvalidTransition(t *testing.T) {
	t.Parallel()

	session, err := NewSession("session-1", "设备借用")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	err = session.Transition(SessionStateActive)
	if !errors.Is(err, ErrInvalidSessionTransition) {
		t.Fatalf("Transition() error = %v, want %v", err, ErrInvalidSessionTransition)
	}
	if session.State != SessionStateDraft {
		t.Fatalf("session.State = %q, want %q", session.State, SessionStateDraft)
	}
	if session.Revision != 0 {
		t.Fatalf("session.Revision = %d, want 0", session.Revision)
	}
}
