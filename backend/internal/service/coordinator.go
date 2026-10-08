package service

import (
	"errors"
	"sync"

	"desktopguardpro/internal/domain"
)

var (
	ErrNoCurrentSession       = errors.New("no current session")
	ErrSessionInProgress      = errors.New("a session is already in progress")
	ErrInvalidRestoredSession = errors.New("invalid restored session")
	ErrSessionChanged         = errors.New("session changed during operation")
)

type Coordinator struct {
	mu      sync.RWMutex
	current *domain.Session
}

func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

func (coordinator *Coordinator) Restore(session domain.Session) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()

	if coordinator.current != nil && !isTerminal(coordinator.current.State) {
		return ErrSessionInProgress
	}
	if session.MonitoringLevel == "" {
		session.MonitoringLevel = domain.MonitoringLevelStandard
	}
	if session.MonitoringPolicy == (domain.MonitoringPolicy{}) {
		session.MonitoringPolicy = domain.DefaultMonitoringPolicy()
		session.MonitoringPolicy.StrictReadAuditEnabled = session.MonitoringLevel == domain.MonitoringLevelStrict
	}
	validated, err := domain.NewSessionWithMonitoringPolicy(session.ID, session.Name, session.MonitoringPolicy)
	if err != nil || validated.ID != session.ID || validated.Name != session.Name ||
		validated.MonitoringLevel != session.MonitoringLevel || !isKnownState(session.State) {
		return ErrInvalidRestoredSession
	}

	restored := session
	coordinator.current = &restored
	return nil
}

func (coordinator *Coordinator) Create(id, name string) (domain.Session, error) {
	return coordinator.CreatePersistedWithMonitoringLevel(id, name, domain.MonitoringLevelStandard, nil)
}

func (coordinator *Coordinator) CreateWithMonitoringLevel(id, name string, level domain.MonitoringLevel) (domain.Session, error) {
	return coordinator.CreatePersistedWithMonitoringLevel(id, name, level, nil)
}

func (coordinator *Coordinator) CreatePersisted(
	id string,
	name string,
	persist func(domain.Session) error,
) (domain.Session, error) {
	return coordinator.CreatePersistedWithMonitoringLevel(id, name, domain.MonitoringLevelStandard, persist)
}

func (coordinator *Coordinator) CreatePersistedWithMonitoringLevel(
	id string,
	name string,
	level domain.MonitoringLevel,
	persist func(domain.Session) error,
) (domain.Session, error) {
	if err := (domain.MonitoringProfile{Level: level}).Validate(); err != nil {
		return domain.Session{}, err
	}
	policy := domain.DefaultMonitoringPolicy()
	policy.StrictReadAuditEnabled = level == domain.MonitoringLevelStrict
	return coordinator.CreatePersistedWithMonitoringPolicy(id, name, policy, persist)
}

func (coordinator *Coordinator) CreatePersistedWithMonitoringPolicy(
	id string,
	name string,
	policy domain.MonitoringPolicy,
	persist func(domain.Session) error,
) (domain.Session, error) {
	return coordinator.CreatePersistedWithMonitoringPolicyProvider(id, name, func() (domain.MonitoringPolicy, error) {
		return policy, nil
	}, persist)
}

func (coordinator *Coordinator) CreatePersistedWithMonitoringPolicyProvider(
	id string,
	name string,
	loadPolicy func() (domain.MonitoringPolicy, error),
	persist func(domain.Session) error,
) (domain.Session, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()

	if coordinator.current != nil && !isTerminal(coordinator.current.State) {
		return domain.Session{}, ErrSessionInProgress
	}

	policy, err := loadPolicy()
	if err != nil {
		return domain.Session{}, err
	}
	session, err := domain.NewSessionWithMonitoringPolicy(id, name, policy)
	if err != nil {
		return domain.Session{}, err
	}
	if persist != nil {
		if err := persist(*session); err != nil {
			return domain.Session{}, err
		}
	}

	coordinator.current = session
	return *session, nil
}

func (coordinator *Coordinator) Transition(next domain.SessionState) (domain.Session, error) {
	return coordinator.TransitionPersisted(next, nil)
}

func (coordinator *Coordinator) TransitionPersisted(
	next domain.SessionState,
	persist func(domain.Session) error,
) (domain.Session, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.transitionLocked(next, persist)
}

// TransitionPersistedFrom binds asynchronous work to the session and revision
// it observed, including when another request replaces the current session.
func (coordinator *Coordinator) TransitionPersistedFrom(
	expected domain.Session,
	next domain.SessionState,
	persist func(domain.Session) error,
) (domain.Session, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.current == nil || coordinator.current.ID != expected.ID ||
		coordinator.current.Revision != expected.Revision || coordinator.current.State != expected.State {
		return domain.Session{}, ErrSessionChanged
	}
	return coordinator.transitionLocked(next, persist)
}

func (coordinator *Coordinator) transitionLocked(
	next domain.SessionState,
	persist func(domain.Session) error,
) (domain.Session, error) {

	if coordinator.current == nil {
		return domain.Session{}, ErrNoCurrentSession
	}
	candidate := *coordinator.current
	if err := candidate.Transition(next); err != nil {
		return domain.Session{}, err
	}
	if persist != nil {
		if err := persist(candidate); err != nil {
			return domain.Session{}, err
		}
	}

	coordinator.current = &candidate
	return candidate, nil
}

func (coordinator *Coordinator) Current() (domain.Session, bool) {
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()

	if coordinator.current == nil {
		return domain.Session{}, false
	}
	return *coordinator.current, true
}

func isTerminal(state domain.SessionState) bool {
	return state == domain.SessionStateCompleted || state == domain.SessionStateFailed
}

func isKnownState(state domain.SessionState) bool {
	switch state {
	case domain.SessionStateDraft, domain.SessionStatePreparing, domain.SessionStateBaselineReview, domain.SessionStateActive,
		domain.SessionStateDegraded, domain.SessionStatePaused, domain.SessionStateFinalizing,
		domain.SessionStateCompleted, domain.SessionStateFailed:
		return true
	default:
		return false
	}
}
