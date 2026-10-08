package service

import (
	"context"
	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"errors"
	"testing"
	"time"
)

type resumeLifecycle struct {
	active func(context.Context, string) error
}

func (l resumeLifecycle) WaitForActive(ctx context.Context, id string) error {
	return l.active(ctx, id)
}
func (resumeLifecycle) WaitForStopped(context.Context, string) error { return nil }

func pausedAPI(t *testing.T) *API {
	t.Helper()
	api := NewPersistentAPI(NewCoordinator(), &testSessionStore{})
	if _, err := api.coordinator.Create("paused-session", "paused session"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStatePaused} {
		if _, err := api.coordinator.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	return api
}

func TestTransitionRejectsExplicitStaleSessionAndRevision(t *testing.T) {
	for _, wrongID := range []bool{false, true} {
		api := pausedAPI(t)
		current, _ := api.coordinator.Current()
		revision := current.Revision - 1
		id := current.ID
		if wrongID {
			id = "other-session"
			revision = current.Revision
		}
		response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, time.Now().UTC(), TransitionSessionRequest{SessionID: id, ExpectedRevision: &revision, State: domain.SessionStateActive}))
		if err != nil {
			t.Fatal(err)
		}
		assertAPIError(t, response, ErrorCodeInvalidTransition)
		if actual, _ := api.coordinator.Current(); actual != current {
			t.Fatalf("stale request changed session: %+v", actual)
		}
	}
}

func TestBoundStartRequestReturnsReviewCapturedBeforeRequest(t *testing.T) {
	api := NewAPI(NewCoordinator())
	if _, err := api.coordinator.Create("baseline", "baseline"); err != nil {
		t.Fatal(err)
	}
	preparing, err := api.coordinator.Transition(domain.SessionStatePreparing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.coordinator.Transition(domain.SessionStateBaselineReview); err != nil {
		t.Fatal(err)
	}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, time.Now().UTC(), TransitionSessionRequest{SessionID: preparing.ID, ExpectedRevision: &preparing.Revision, State: domain.SessionStateActive}))
	if err != nil || response.Type != contracts.MessageTypeSessionResult {
		t.Fatalf("pending review lost to stale preparing revision: response=%s err=%v", response.Payload, err)
	}
	var result SessionResult
	decodeTestPayload(t, response, &result)
	if result.Session == nil || result.Session.State != domain.SessionStateBaselineReview {
		t.Fatalf("review was bypassed: %+v", result)
	}
}

func TestSessionInputVerificationCannotEndReplacement(t *testing.T) {
	api := pausedAPI(t)
	request := newTestMessage(t, contracts.MessageTypeInputShieldCredentialVerify, time.Now().UTC(), struct{}{})
	response, err := api.inputShieldVerificationSucceeded(request, InputControlResult{Source: "session", ControlID: "old-session", Policy: domain.InputShieldPolicy{UnlockAction: domain.InputShieldUnlockActionEndSession}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, ErrorCodeInputShieldUnavailable)
	current, _ := api.coordinator.Current()
	if current.State != domain.SessionStatePaused {
		t.Fatalf("replacement ended: %+v", current)
	}
}

func TestAgentActivityRejectsPreviousSessionRevision(t *testing.T) {
	api := pausedAPI(t)
	if _, err := api.coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	current, _ := api.coordinator.Current()
	api.SetAgentExecutable(`C:\App\desktop-guard-agent.exe`)
	called := false
	api.SetAgentActivityHandler(func(context.Context, ClientIdentity, AgentActivityReportRequest) error { called = true; return nil })
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeAgentActivityReport, time.Now().UTC(), AgentActivityReportRequest{SessionID: current.ID, SessionRevision: current.Revision - 1, ProcessID: 101, ObservedUTC: time.Now().UTC()}), ClientIdentity{ImagePath: `C:\App\desktop-guard-agent.exe`})
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, ErrorCodeNoCurrentSession)
	if called {
		t.Fatal("stale activity reached the writer")
	}
}

func TestResumeTimeoutReturnsToPausedWithLifecycleEvidence(t *testing.T) {
	api := pausedAPI(t)
	previous, _ := api.coordinator.Current()
	api.SetSessionLifecycle(resumeLifecycle{active: func(ctx context.Context, id string) error {
		current, _ := api.coordinator.Current()
		if current.ID != id || current.State != domain.SessionStateActive {
			t.Fatalf("resume waited before state was activated: %+v", current)
		}
		<-ctx.Done()
		return ctx.Err()
	}})
	request := newTestMessage(t, contracts.MessageTypeSessionTransition, time.Now().UTC(), TransitionSessionRequest{State: domain.SessionStateActive})
	request.DeadlineUTC = time.Now().UTC().Add(25 * time.Millisecond)
	response, err := api.Handle(request)
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, ErrorCodeInvalidRequest)
	current, _ := api.coordinator.Current()
	if current.State != domain.SessionStatePaused || current.Revision != previous.Revision+2 {
		t.Fatalf("failed resume not reverted: %+v", current)
	}
	store := api.store.(*testSessionStore)
	if len(store.events) != 2 || store.events[0].Action != "protection_resumed" || store.events[1].Action != "protection_paused" {
		t.Fatalf("rollback evidence=%+v", store.events)
	}
}

func TestFailedResumeCannotRollBackConcurrentRevision(t *testing.T) {
	api := pausedAPI(t)
	var concurrent domain.Session
	api.SetSessionLifecycle(resumeLifecycle{active: func(context.Context, string) error {
		current, _ := api.coordinator.Current()
		var err error
		concurrent, err = api.persistSessionTransition(context.Background(), current, domain.SessionStatePaused)
		if err != nil {
			t.Fatal(err)
		}
		return errors.New("old resume failed")
	}})
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeSessionTransition, time.Now().UTC(), TransitionSessionRequest{State: domain.SessionStateActive}))
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, ErrorCodeInvalidRequest)
	current, _ := api.coordinator.Current()
	if current != concurrent {
		t.Fatalf("old resume overwrote new revision: current=%+v expected=%+v", current, concurrent)
	}
}

func TestCoordinatorRejectsStaleRevisionBeforePersistence(t *testing.T) {
	api := pausedAPI(t)
	previous, _ := api.coordinator.Current()
	if _, err := api.coordinator.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := api.coordinator.TransitionPersistedFrom(previous, domain.SessionStateActive, func(domain.Session) error { called = true; return nil })
	if !errors.Is(err, ErrSessionChanged) || called {
		t.Fatalf("stale transition: %v persisted=%t", err, called)
	}
}
