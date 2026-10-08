package service_test

import (
	"context"
	"desktopguardpro/internal/collector"
	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
	"testing"
	"time"
)

type reviewEventStore struct{}

func (reviewEventStore) AppendEventAutoSequence(_ context.Context, event domain.AuditEvent, _ []byte) (domain.AuditEvent, error) {
	return event, nil
}

type reviewCollector struct{}

func (reviewCollector) Name() string { return "review-mock" }
func (reviewCollector) Run(ctx context.Context, _ collector.Sink) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestRegressionResumePausedWithRealController(t *testing.T) {
	coordinator := coreservice.NewCoordinator()
	if _, err := coordinator.Create("resume-probe", "resume probe"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStatePaused} {
		if _, err := coordinator.Transition(state); err != nil {
			t.Fatal(err)
		}
	}
	controller, err := collector.NewSessionController(coordinator, reviewEventStore{}, func() ([]collector.Collector, error) { return []collector.Collector{reviewCollector{}}, nil }, collector.SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	api := coreservice.NewAPI(coordinator)
	api.SetSessionLifecycle(controller)
	request, err := contracts.NewMessage("resume", contracts.MessageTypeSessionTransition, time.Now().UTC().Add(time.Second), coreservice.TransitionSessionRequest{State: domain.SessionStateActive})
	if err != nil {
		t.Fatal(err)
	}
	response, err := api.Handle(request)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := coordinator.Current()
	if response.Type != contracts.MessageTypeSessionResult || current.State != domain.SessionStateActive {
		t.Fatalf("resume failed: response=%s payload=%s state=%s", response.Type, response.Payload, current.State)
	}
}
