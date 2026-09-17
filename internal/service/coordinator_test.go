package service

import (
	"errors"
	"testing"

	"desktopguardpro/internal/domain"
)

func TestCoordinatorStartsEmpty(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	if _, ok := coordinator.Current(); ok {
		t.Fatal("Current() ok = true, want false")
	}
}

func TestCoordinatorCreatesAndTransitionsSession(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	session, err := coordinator.Create("session-1", "离席保护")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if session.State != domain.SessionStateDraft {
		t.Fatalf("Create() state = %q, want %q", session.State, domain.SessionStateDraft)
	}

	session, err = coordinator.Transition(domain.SessionStatePreparing)
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if session.State != domain.SessionStatePreparing {
		t.Fatalf("Transition() state = %q, want %q", session.State, domain.SessionStatePreparing)
	}
	if session.Revision != 1 {
		t.Fatalf("Transition() revision = %d, want 1", session.Revision)
	}
}

func TestCoordinatorCreatesSessionWithSelectedMonitoringLevel(t *testing.T) {
	coordinator := NewCoordinator()
	session, err := coordinator.CreateWithMonitoringLevel("session-strict", "严格保护", domain.MonitoringLevelStrict)
	if err != nil {
		t.Fatalf("CreateWithMonitoringLevel() error = %v", err)
	}
	if session.MonitoringLevel != domain.MonitoringLevelStrict {
		t.Fatalf("session.MonitoringLevel = %q, want %q", session.MonitoringLevel, domain.MonitoringLevelStrict)
	}
}

func TestCoordinatorCreatesSessionWithMonitoringPolicy(t *testing.T) {
	coordinator := NewCoordinator()
	policy := domain.DefaultMonitoringPolicy()
	policy.ProcessAndSoftwareEnabled = false
	session, err := coordinator.CreatePersistedWithMonitoringPolicy("session-policy", "策略会话", policy, nil)
	if err != nil {
		t.Fatalf("CreatePersistedWithMonitoringPolicy() error = %v", err)
	}
	if session.MonitoringPolicy != policy {
		t.Fatalf("session policy = %+v, want %+v", session.MonitoringPolicy, policy)
	}
}

func TestCoordinatorLoadsPolicyWhileCreatingSession(t *testing.T) {
	coordinator := NewCoordinator()
	called := false
	policy := domain.DefaultMonitoringPolicy()
	policy.SystemAndNetworkEnabled = false
	session, err := coordinator.CreatePersistedWithMonitoringPolicyProvider("session-loaded-policy", "策略加载", func() (domain.MonitoringPolicy, error) {
		called = true
		return policy, nil
	}, nil)
	if err != nil || !called || session.MonitoringPolicy != policy {
		t.Fatalf("session=%+v called=%t error=%v", session, called, err)
	}
}

func TestCoordinatorRejectsOverlappingSession(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	if _, err := coordinator.Create("session-1", "离席保护"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err := coordinator.Create("session-2", "设备借用")
	if !errors.Is(err, ErrSessionInProgress) {
		t.Fatalf("Create() error = %v, want %v", err, ErrSessionInProgress)
	}
}

func TestCoordinatorRequiresCurrentSessionForTransition(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	_, err := coordinator.Transition(domain.SessionStatePreparing)
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Fatalf("Transition() error = %v, want %v", err, ErrNoCurrentSession)
	}
}

func TestCoordinatorReturnsSessionCopy(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	created, err := coordinator.Create("session-1", "离席保护")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	created.Name = "已修改"

	current, ok := coordinator.Current()
	if !ok {
		t.Fatal("Current() ok = false, want true")
	}
	if current.Name != "离席保护" {
		t.Fatalf("Current() name = %q, want %q", current.Name, "离席保护")
	}
}

func TestCoordinatorAllowsSessionAfterTerminalState(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	if _, err := coordinator.Create("session-1", "离席保护"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	states := []domain.SessionState{
		domain.SessionStatePreparing,
		domain.SessionStateActive,
		domain.SessionStateFinalizing,
		domain.SessionStateCompleted,
	}
	for _, state := range states {
		if _, err := coordinator.Transition(state); err != nil {
			t.Fatalf("Transition(%q) error = %v", state, err)
		}
	}

	session, err := coordinator.Create("session-2", "设备借用")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if session.ID != "session-2" {
		t.Fatalf("Create() id = %q, want %q", session.ID, "session-2")
	}
}

func TestCoordinatorRestoresPersistedSession(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	persisted := domain.Session{
		ID:       "session-restored",
		Name:     "恢复中的会话",
		State:    domain.SessionStateActive,
		Revision: 2,
	}
	if err := coordinator.Restore(persisted); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	current, ok := coordinator.Current()
	if !ok {
		t.Fatal("Current() ok = false, want true")
	}
	if current.MonitoringLevel != domain.MonitoringLevelStandard {
		t.Fatalf("restored monitoring level = %q, want %q", current.MonitoringLevel, domain.MonitoringLevelStandard)
	}
	if current.MonitoringPolicy != domain.DefaultMonitoringPolicy() {
		t.Fatalf("restored policy = %+v, want default policy", current.MonitoringPolicy)
	}

	persisted.Name = "外部修改"
	current, _ = coordinator.Current()
	if current.Name == persisted.Name {
		t.Fatal("Restore() retained caller-owned session memory")
	}
}

func TestCoordinatorRestoresSessionAwaitingBaselineDecision(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	persisted := domain.Session{
		ID:       "session-baseline-review",
		Name:     "等待基线处理",
		State:    domain.SessionStateBaselineReview,
		Revision: 2,
	}
	if err := coordinator.Restore(persisted); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	current, ok := coordinator.Current()
	if !ok || current.State != domain.SessionStateBaselineReview {
		t.Fatalf("Current() = %+v, want restored baseline review", current)
	}
}

func TestCoordinatorRejectsInvalidRestoredSession(t *testing.T) {
	t.Parallel()

	coordinator := NewCoordinator()
	err := coordinator.Restore(domain.Session{
		ID:    "session-invalid",
		Name:  "无效状态",
		State: domain.SessionState("unknown"),
	})
	if !errors.Is(err, ErrInvalidRestoredSession) {
		t.Fatalf("Restore() error = %v, want %v", err, ErrInvalidRestoredSession)
	}
}

func TestCoordinatorKeepsStateWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	persistenceFailure := errors.New("storage unavailable")
	coordinator := NewCoordinator()
	_, err := coordinator.CreatePersisted("session-1", "离席保护", func(domain.Session) error {
		return persistenceFailure
	})
	if !errors.Is(err, persistenceFailure) {
		t.Fatalf("CreatePersisted() error = %v, want %v", err, persistenceFailure)
	}
	if _, ok := coordinator.Current(); ok {
		t.Fatal("Current() ok = true after failed create persistence")
	}

	if _, err := coordinator.Create("session-1", "离席保护"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = coordinator.TransitionPersisted(domain.SessionStatePreparing, func(domain.Session) error {
		return persistenceFailure
	})
	if !errors.Is(err, persistenceFailure) {
		t.Fatalf("TransitionPersisted() error = %v, want %v", err, persistenceFailure)
	}
	current, ok := coordinator.Current()
	if !ok {
		t.Fatal("Current() ok = false after failed transition persistence")
	}
	if current.State != domain.SessionStateDraft || current.Revision != 0 {
		t.Fatalf("Current() = %+v after failed transition persistence", current)
	}
}
