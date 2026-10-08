package collector

import (
	"context"
	"desktopguardpro/internal/domain"
	"testing"
	"time"
)

func TestRegressionUserActivityOnlySessionCanRun(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.Mode = domain.MonitoringModeCustom
	policy.FileActivityEnabled = false
	policy.ProcessAndSoftwareEnabled = false
	policy.SystemAndNetworkEnabled = false
	policy.ExternalDevicesEnabled = false
	policy.UserSessionActivityEnabled = true
	policy.StrictReadAuditEnabled = false
	policy.InputShieldEnabled = false
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	factory := func() ([]Collector, error) { return SystemCollectorsForMonitoringPolicy(nil, nil, policy) }
	source := &fakeSessionSource{session: domain.Session{ID: "activity-only", State: domain.SessionStateActive, MonitoringPolicy: policy}, exists: true}
	store := &fakeEventStore{}
	controller, err := NewSessionController(source, store, factory, SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("controller shutdown: %v", err)
		}
	}()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := controller.WaitForActive(waitCtx, "activity-only"); err != nil {
		t.Fatalf("valid activity-only policy cannot start: %v", err)
	}
	if controller.HealthStatus() != StateRunning {
		t.Fatal("activity-only pipeline is not healthy")
	}
	observation := Observation{Category: domain.EventCategorySystem, Action: "user_activity", Severity: domain.EventSeverityLow, ObservedUTC: time.Now().UTC(), Source: "mock-agent", Confidence: domain.EventConfidenceDirect, Payload: []byte(`{}`)}
	if err := controller.EmitExternalObservation(waitCtx, "activity-only", observation); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	done <- nil
	if store.count() != 1 {
		t.Fatalf("agent observation was not persisted: %d", store.count())
	}
}
