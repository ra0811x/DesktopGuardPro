package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

type storedObservation struct {
	event   domain.AuditEvent
	payload []byte
}

type recordingEventStore struct {
	mutex          sync.Mutex
	lastSequence   uint64
	records        []storedObservation
	autoSequences  []uint64
	firstStarted   chan struct{}
	releaseFirst   chan struct{}
	rejectCanceled bool
}

func (store *recordingEventStore) AppendEventAutoSequence(ctx context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error) {
	if store.rejectCanceled {
		if err := ctx.Err(); err != nil {
			return domain.AuditEvent{}, err
		}
	}
	store.mutex.Lock()
	store.autoSequences = append(store.autoSequences, event.Sequence)
	event.Sequence = store.lastSequence + uint64(len(store.records)) + 1
	first := len(store.records) == 0
	store.records = append(store.records, storedObservation{event: event, payload: append([]byte(nil), payload...)})
	store.mutex.Unlock()
	if first && store.firstStarted != nil {
		close(store.firstStarted)
		<-store.releaseFirst
	}
	return event, nil
}

func TestWriterDrainsQueuedEventsAfterRunContextCancellation(t *testing.T) {
	const attempts = 32

	for attempt := 0; attempt < attempts; attempt++ {
		store := &recordingEventStore{rejectCanceled: true}
		writer := newTestWriter(t, store, WriterOptions{QueueCapacity: 1})
		writer.queue <- testObservation("queued", []byte("tail"))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := writer.Run(ctx); err != nil {
			t.Fatalf("attempt %d: Run() error = %v", attempt, err)
		}
		if records := store.snapshot(); len(records) != 1 {
			t.Fatalf("attempt %d: stored record count = %d, want 1", attempt, len(records))
		}
	}
}

func (store *recordingEventStore) snapshot() []storedObservation {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return append([]storedObservation(nil), store.records...)
}

func (store *recordingEventStore) requestedSequences() []uint64 {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return append([]uint64(nil), store.autoSequences...)
}

func TestWriterContinuesSequenceAndDrainsInOrder(t *testing.T) {
	t.Parallel()

	store := &recordingEventStore{lastSequence: 7}
	writer := newTestWriter(t, store, WriterOptions{QueueCapacity: 4})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(ctx) }()

	first := testObservation("created", []byte("first"))
	second := testObservation("modified", []byte("second"))
	if err := writer.Emit(ctx, first); err != nil {
		t.Fatalf("Emit(first) error = %v", err)
	}
	if err := writer.Emit(ctx, second); err != nil {
		t.Fatalf("Emit(second) error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	records := store.snapshot()
	if len(records) != 2 {
		t.Fatalf("stored record count = %d, want 2", len(records))
	}
	if records[0].event.Sequence != 8 || records[1].event.Sequence != 9 {
		t.Fatalf("stored sequences = %d,%d, want 8,9", records[0].event.Sequence, records[1].event.Sequence)
	}
	if sequences := store.requestedSequences(); len(sequences) != 2 || sequences[0] != 0 || sequences[1] != 0 {
		t.Fatalf("auto-sequence requests = %v, want [0 0]", sequences)
	}
	if string(records[0].payload) != "first" || string(records[1].payload) != "second" {
		t.Fatalf("stored payload order is incorrect")
	}
}

func TestWriterRecordsQueueOverflowAfterRecovery(t *testing.T) {
	t.Parallel()

	store := &recordingEventStore{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	writer := newTestWriter(t, store, WriterOptions{
		QueueCapacity: 1,
		EnqueueWait:   time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(ctx) }()

	if err := writer.Emit(ctx, testObservation("first", nil)); err != nil {
		t.Fatalf("Emit(first) error = %v", err)
	}
	select {
	case <-store.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first append did not start")
	}
	if err := writer.Emit(ctx, testObservation("second", nil)); err != nil {
		t.Fatalf("Emit(second) error = %v", err)
	}
	if err := writer.Emit(ctx, testObservation("dropped", nil)); !errors.Is(err, ErrObservationQueueFull) {
		t.Fatalf("Emit(dropped) error = %v, want %v", err, ErrObservationQueueFull)
	}
	close(store.releaseFirst)

	deadline := time.Now().Add(time.Second)
	for len(store.snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	records := store.snapshot()
	if len(records) != 3 {
		t.Fatalf("stored record count = %d, want 3", len(records))
	}
	if records[1].event.Category != domain.EventCategoryHealth || records[1].event.Action != "observation_queue_overflow" {
		t.Fatalf("overflow event = %+v", records[1].event)
	}
	var payload struct {
		DroppedObservations uint64 `json:"droppedObservations"`
	}
	if err := json.Unmarshal(records[1].payload, &payload); err != nil {
		t.Fatalf("decode overflow payload: %v", err)
	}
	if payload.DroppedObservations != 1 {
		t.Fatalf("dropped observations = %d, want 1", payload.DroppedObservations)
	}
}

func TestWriterValidatesObservationAndLifecycle(t *testing.T) {
	t.Parallel()

	store := &recordingEventStore{}
	writer := newTestWriter(t, store, WriterOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(ctx) }()

	invalid := testObservation("", nil)
	if err := writer.Emit(ctx, invalid); !errors.Is(err, ErrObservationInvalid) {
		t.Fatalf("Emit(invalid) error = %v, want %v", err, ErrObservationInvalid)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := writer.Emit(context.Background(), testObservation("late", nil)); !errors.Is(err, ErrWriterNotRunning) {
		t.Fatalf("Emit(after stop) error = %v, want %v", err, ErrWriterNotRunning)
	}
	if err := writer.Run(context.Background()); !errors.Is(err, ErrWriterAlreadyRun) {
		t.Fatalf("Run(second) error = %v, want %v", err, ErrWriterAlreadyRun)
	}
}

func newTestWriter(t *testing.T, store EventStore, options WriterOptions) *Writer {
	t.Helper()

	var eventNumber uint64
	options.Now = func() time.Time {
		return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	}
	options.NewEventID = func() (string, error) {
		eventNumber++
		return fmt.Sprintf("event-%d", eventNumber), nil
	}
	writer, err := NewWriter("session-1", store, options)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	return writer
}

func testObservation(action string, payload []byte) Observation {
	return Observation{
		Category:       domain.EventCategoryFile,
		Action:         action,
		Severity:       domain.EventSeverityLow,
		ObservedUTC:    time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
		MonotonicTicks: 42,
		Source:         "test_collector",
		Confidence:     domain.EventConfidenceDirect,
		Payload:        payload,
	}
}
