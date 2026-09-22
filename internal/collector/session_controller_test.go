package collector

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestSessionControllerStartsActiveSessionAndDrainsOnFinalizing(t *testing.T) {
	source := &fakeSessionSource{}
	store := &fakeEventStore{}
	collectorStarted := make(chan struct{})
	collectorStopped := make(chan struct{})
	controller, err := NewSessionController(source, store, func() ([]Collector, error) {
		return []Collector{&lifecycleCollector{started: collectorStarted, stopped: collectorStopped}}, nil
	}, SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	source.set(domain.Session{ID: "session-1", State: domain.SessionStateActive}, true)
	waitChannel(t, collectorStarted)
	source.set(domain.Session{ID: "session-1", State: domain.SessionStateFinalizing}, true)
	waitChannel(t, collectorStopped)

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if store.count() != 1 {
		t.Fatalf("expected one drained event, got %d", store.count())
	}
}

func TestSessionControllerCapturesBaselinesAroundCollectorLifecycle(t *testing.T) {
	source := &fakeSessionSource{
		session: domain.Session{ID: "session-1", State: domain.SessionStatePreparing},
		exists:  true,
	}
	collectorStarted := make(chan struct{})
	collectorStopped := make(chan struct{})
	startCaptureCalled := make(chan struct{})
	allowStartCapture := make(chan struct{})
	endCaptureCalled := make(chan struct{})
	controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) {
		return []Collector{&lifecycleCollector{
			started: collectorStarted,
			stopped: collectorStopped,
		}}, nil
	}, SessionControllerOptions{
		PollInterval: time.Millisecond,
		CaptureStartBaseline: func(_ context.Context, sessionID string) error {
			if sessionID != "session-1" {
				return errors.New("unexpected start baseline session ID")
			}
			close(startCaptureCalled)
			<-allowStartCapture
			return nil
		},
		CaptureEndBaseline: func(_ context.Context, sessionID string) error {
			if sessionID != "session-1" {
				return errors.New("unexpected end baseline session ID")
			}
			select {
			case <-collectorStopped:
			default:
				return errors.New("end baseline captured before collectors stopped")
			}
			close(endCaptureCalled)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	waitChannel(t, startCaptureCalled)
	select {
	case <-collectorStarted:
		t.Fatal("collector started before the start baseline completed")
	default:
	}
	close(allowStartCapture)
	waitChannel(t, collectorStarted)

	source.set(domain.Session{ID: "session-1", State: domain.SessionStateFinalizing}, true)
	waitChannel(t, endCaptureCalled)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSessionControllerReportsStartBaselineCaptureFailure(t *testing.T) {
	source := &fakeSessionSource{
		session: domain.Session{ID: "broken", State: domain.SessionStatePreparing},
		exists:  true,
	}
	want := errors.New("baseline unavailable")
	factoryCalled := false
	controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) {
		factoryCalled = true
		return []Collector{&lifecycleCollector{}}, nil
	}, SessionControllerOptions{
		PollInterval: time.Millisecond,
		CaptureStartBaseline: func(context.Context, string) error {
			return want
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := controller.Run(ctx); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
	if factoryCalled {
		t.Fatal("collector factory called after baseline capture failed")
	}
}

func TestSessionControllerStopsBeforePipelineWhenBaselineNeedsReview(t *testing.T) {
	source := &fakeSessionSource{
		session: domain.Session{ID: "session-review", State: domain.SessionStatePreparing},
		exists:  true,
	}
	reviewReady := make(chan struct{})
	factoryCalled := false
	controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) {
		factoryCalled = true
		return []Collector{&lifecycleCollector{}}, nil
	}, SessionControllerOptions{
		PollInterval: time.Millisecond,
		CaptureStartBaseline: func(context.Context, string) error {
			source.set(domain.Session{ID: "session-review", State: domain.SessionStateBaselineReview}, true)
			close(reviewReady)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	waitChannel(t, reviewReady)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := controller.WaitForActive(waitCtx, "session-review"); !errors.Is(err, ErrSessionBaselineReviewPending) {
		t.Fatalf("WaitForActive() error = %v, want %v", err, ErrSessionBaselineReviewPending)
	}
	if factoryCalled {
		t.Fatal("collector factory called while baseline review was pending")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSessionControllerStartsRecoveredActiveSession(t *testing.T) {
	source := &fakeSessionSource{session: domain.Session{ID: "recovered", State: domain.SessionStateDegraded}, exists: true}
	store := &fakeEventStore{}
	started := make(chan struct{})
	startBaselineCaptures := 0
	controller, err := NewSessionController(source, store, func() ([]Collector, error) {
		return []Collector{&lifecycleCollector{started: started}}, nil
	}, SessionControllerOptions{
		PollInterval: time.Millisecond,
		CaptureStartBaseline: func(context.Context, string) error {
			startBaselineCaptures++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	waitChannel(t, started)
	if startBaselineCaptures != 0 {
		t.Fatalf("recovered session recaptured start baseline %d times", startBaselineCaptures)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionControllerWaitsForPreparedPipelineStartAndStop(t *testing.T) {
	source := &fakeSessionSource{session: domain.Session{ID: "session-1", State: domain.SessionStatePreparing}, exists: true}
	started := make(chan struct{})
	stopped := make(chan struct{})
	controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) {
		return []Collector{&lifecycleCollector{started: started, stopped: stopped}}, nil
	}, SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	readyCtx, readyCancel := context.WithTimeout(context.Background(), time.Second)
	defer readyCancel()
	if err := controller.WaitForActive(readyCtx, "session-1"); err != nil {
		t.Fatalf("WaitForActive() error = %v", err)
	}
	waitChannel(t, started)

	source.set(domain.Session{ID: "session-1", State: domain.SessionStateFinalizing}, true)
	stoppedCtx, stoppedCancel := context.WithTimeout(context.Background(), time.Second)
	defer stoppedCancel()
	if err := controller.WaitForStopped(stoppedCtx, "session-1"); err != nil {
		t.Fatalf("WaitForStopped() error = %v", err)
	}
	waitChannel(t, stopped)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSessionControllerReportsWriterAppendFailure(t *testing.T) {
	source := &fakeSessionSource{session: domain.Session{ID: "broken", State: domain.SessionStateActive}, exists: true}
	want := errors.New("event persistence unavailable")
	store := &fakeEventStore{lastError: want}
	controller, err := NewSessionController(source, store, func() ([]Collector, error) {
		return []Collector{&lifecycleCollector{}}, nil
	}, SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := controller.Run(ctx); !errors.Is(err, want) {
		t.Fatalf("expected writer error, got %v", err)
	}
}

func TestSessionControllerEmitsExternalObservationOnlyForActiveSession(t *testing.T) {
	source := &fakeSessionSource{session: domain.Session{ID: "session-1", State: domain.SessionStateActive}, exists: true}
	store := &fakeEventStore{}
	started := make(chan struct{})
	controller, err := NewSessionController(source, store, func() ([]Collector, error) {
		return []Collector{&lifecycleCollector{started: started}}, nil
	}, SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	waitChannel(t, started)

	observation := Observation{
		Category: domain.EventCategorySystem, Action: "agent_activity_summary", Severity: domain.EventSeverityLow,
		ObservedUTC: time.Now().UTC(), Source: "session_agent", Confidence: domain.EventConfidenceDirect,
	}
	if err := controller.EmitExternalObservation(context.Background(), "session-1", observation); err != nil {
		t.Fatalf("EmitExternalObservation() error = %v", err)
	}
	if err := controller.EmitExternalObservation(context.Background(), "other-session", observation); !errors.Is(err, ErrSessionPipelineNotActive) {
		t.Fatalf("EmitExternalObservation() error = %v, want %v", err, ErrSessionPipelineNotActive)
	}

	deadline := time.Now().Add(time.Second)
	for store.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.count() != 2 {
		t.Fatalf("stored event count = %d, want collector event and external observation", store.count())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionControllerReportsDegradedHealthForFailedCollector(t *testing.T) {
	source := &fakeSessionSource{session: domain.Session{ID: "broken", State: domain.SessionStateActive}, exists: true}
	controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) {
		return []Collector{collectorFunc{name: "broken", run: func(context.Context, Sink) error {
			return errors.New("collector unavailable")
		}}}, nil
	}, SessionControllerOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for controller.HealthStatus() != StateDegraded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := controller.HealthStatus(); got != StateDegraded {
		t.Fatalf("HealthStatus() = %q, want %q", got, StateDegraded)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSessionPipelineReportsWriterFailureDuringShutdown(t *testing.T) {
	want := errors.New("drain write failed")
	store := &shutdownFailingEventStore{
		appendStarted: make(chan struct{}),
		err:           want,
	}
	pipeline, err := newSessionPipeline(
		"session-1",
		store,
		[]Collector{&lifecycleCollector{}},
		WriterOptions{},
	)
	if err != nil {
		t.Fatalf("newSessionPipeline() error = %v", err)
	}
	pipeline.start(context.Background())
	waitChannel(t, store.appendStarted)

	if err := pipeline.stop(); !errors.Is(err, want) {
		t.Fatalf("pipeline.stop() error = %v, want %v", err, want)
	}
}

func TestParentShutdownKeepsWriterOpenForCollectorFinalScan(t *testing.T) {
	store := &fakeEventStore{}
	started := make(chan struct{})
	finalReady := make(chan struct{})
	allowFinal := make(chan struct{})
	emitted := make(chan error, 1)
	finalCollector := collectorFunc{name: "final-scan", run: func(ctx context.Context, sink Sink) error {
		close(started)
		<-ctx.Done()
		close(finalReady)
		<-allowFinal
		finalCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		emitted <- sink.Emit(finalCtx, Observation{Category: domain.EventCategoryFile, Action: "file_created", Severity: domain.EventSeverityLow, ObservedUTC: time.Now().UTC(), Source: "final-scan", Confidence: domain.EventConfidenceSnapshotDiff})
		return ctx.Err()
	}}
	pipeline, err := newSessionPipeline("shutdown", store, []Collector{finalCollector}, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pipeline.start(ctx)
	waitChannel(t, started)
	waitChannel(t, pipeline.writer.ready)
	cancel()
	waitChannel(t, finalReady)
	time.Sleep(25 * time.Millisecond)
	close(allowFinal)
	if err := pipeline.stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-emitted; err != nil {
		t.Fatalf("final snapshot could not enqueue: %v", err)
	}
	if store.count() != 1 {
		t.Fatalf("final snapshot was not persisted: %d", store.count())
	}
}

type fakeSessionSource struct {
	mutex   sync.RWMutex
	session domain.Session
	exists  bool
}

func (source *fakeSessionSource) Current() (domain.Session, bool) {
	source.mutex.RLock()
	defer source.mutex.RUnlock()
	return source.session, source.exists
}

func (source *fakeSessionSource) set(session domain.Session, exists bool) {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	source.session = session
	source.exists = exists
}

type lifecycleCollector struct {
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (*lifecycleCollector) Name() string { return "lifecycle" }

func (collector *lifecycleCollector) Run(ctx context.Context, sink Sink) error {
	collector.once.Do(func() {
		if collector.started != nil {
			close(collector.started)
		}
	})
	_ = sink.Emit(ctx, Observation{
		Category: domain.EventCategoryHealth, Action: "collector_started",
		Severity: domain.EventSeverityLow, ObservedUTC: time.Now().UTC(),
		Source: "lifecycle", Confidence: domain.EventConfidenceDirect,
	})
	<-ctx.Done()
	if collector.stopped != nil {
		close(collector.stopped)
	}
	return ctx.Err()
}

type fakeEventStore struct {
	mutex     sync.Mutex
	events    []domain.AuditEvent
	lastError error
}

type shutdownFailingEventStore struct {
	appendStarted chan struct{}
	err           error
	once          sync.Once
}

func (store *shutdownFailingEventStore) AppendEventAutoSequence(ctx context.Context, _ domain.AuditEvent, _ []byte) (domain.AuditEvent, error) {
	store.once.Do(func() { close(store.appendStarted) })
	<-ctx.Done()
	return domain.AuditEvent{}, store.err
}

func (store *fakeEventStore) AppendEventAutoSequence(_ context.Context, event domain.AuditEvent, _ []byte) (domain.AuditEvent, error) {
	if store.lastError != nil {
		return domain.AuditEvent{}, store.lastError
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	event.Sequence = uint64(len(store.events) + 1)
	store.events = append(store.events, event)
	return event, nil
}

func (store *fakeEventStore) count() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return len(store.events)
}

func waitChannel(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle signal")
	}
}
func TestAuditEndBaselineAfterPause(t *testing.T) {
	for _, pause := range []bool{false, true} {
		name := "direct_end"
		if pause {
			name = "pause_then_end"
		}
		t.Run(name, func(t *testing.T) {
			source := &fakeSessionSource{session: domain.Session{ID: "audit-probe", State: domain.SessionStateActive}, exists: true}
			endCount := 0
			started := make(chan struct{})
			controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) {
				return []Collector{&lifecycleCollector{started: started}}, nil
			}, SessionControllerOptions{CaptureEndBaseline: func(context.Context, string) error { endCount++; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pipeline, err := controller.reconcile(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer pipeline.stop()
			waitChannel(t, started)
			if pause {
				source.set(domain.Session{ID: "audit-probe", State: domain.SessionStatePaused}, true)
				pipeline, err = controller.reconcile(ctx, pipeline)
				if err != nil {
					t.Fatal(err)
				}
			}
			source.set(domain.Session{ID: "audit-probe", State: domain.SessionStateFinalizing}, true)
			_, err = controller.reconcile(ctx, pipeline)
			if err != nil {
				t.Fatal(err)
			}
			if endCount != 1 {
				t.Fatalf("end baseline captures = %d, want 1", endCount)
			}
		})
	}
}

func TestSessionControllerWaitForStoppedWaitsForEndBaselineWithoutPipeline(t *testing.T) {
	source := &fakeSessionSource{session: domain.Session{ID: "paused-end", State: domain.SessionStateFinalizing}, exists: true}
	captures := 0
	controller, err := NewSessionController(source, &fakeEventStore{}, func() ([]Collector, error) { return nil, nil }, SessionControllerOptions{
		PollInterval:       time.Millisecond,
		CaptureEndBaseline: func(context.Context, string) error { captures++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := controller.WaitForStopped(timeout, "paused-end"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("returned before end baseline: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := controller.reconcile(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if captures != 1 {
		t.Fatalf("end baseline captured %d times, want exactly once", captures)
	}
	if err := controller.WaitForStopped(context.Background(), "paused-end"); err != nil {
		t.Fatal(err)
	}
}
