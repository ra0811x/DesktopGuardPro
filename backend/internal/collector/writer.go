package collector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"desktopguardpro/internal/domain"
)

const (
	defaultQueueCapacity = 1_024
	defaultEnqueueWait   = 250 * time.Millisecond
	defaultDrainTimeout  = 5 * time.Second
)

var (
	ErrEventStoreRequired    = errors.New("event store is required")
	ErrWriterSessionRequired = errors.New("event writer session id is required")
	ErrWriterAlreadyRun      = errors.New("event writer can only be run once")
	ErrWriterNotRunning      = errors.New("event writer is not running")
	ErrObservationQueueFull  = errors.New("observation queue is full")
)

type EventStore interface {
	AppendEventAutoSequence(ctx context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error)
}

type WriterOptions struct {
	QueueCapacity int
	EnqueueWait   time.Duration
	DrainTimeout  time.Duration
	Now           func() time.Time
	NewEventID    func() (string, error)
}

type Writer struct {
	sessionID    string
	store        EventStore
	queue        chan Observation
	enqueueWait  time.Duration
	drainTimeout time.Duration
	now          func() time.Time
	newEventID   func() (string, error)

	ready     chan struct{}
	runCalled atomic.Bool
	accepting atomic.Bool
	dropped   atomic.Uint64
}

func NewWriter(sessionID string, store EventStore, options WriterOptions) (*Writer, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrWriterSessionRequired
	}
	if store == nil {
		return nil, ErrEventStoreRequired
	}
	if options.QueueCapacity == 0 {
		options.QueueCapacity = defaultQueueCapacity
	}
	if options.QueueCapacity < 0 {
		return nil, errors.New("event writer queue capacity must be positive")
	}
	if options.EnqueueWait == 0 {
		options.EnqueueWait = defaultEnqueueWait
	}
	if options.EnqueueWait < 0 {
		return nil, errors.New("event writer enqueue wait must not be negative")
	}
	if options.DrainTimeout == 0 {
		options.DrainTimeout = defaultDrainTimeout
	}
	if options.DrainTimeout < 0 {
		return nil, errors.New("event writer drain timeout must not be negative")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewEventID == nil {
		options.NewEventID = randomEventID
	}

	return &Writer{
		sessionID:    sessionID,
		store:        store,
		queue:        make(chan Observation, options.QueueCapacity),
		enqueueWait:  options.EnqueueWait,
		drainTimeout: options.DrainTimeout,
		now:          options.Now,
		newEventID:   options.NewEventID,
		ready:        make(chan struct{}),
	}, nil
}

func (writer *Writer) Run(ctx context.Context) error {
	if !writer.runCalled.CompareAndSwap(false, true) {
		return ErrWriterAlreadyRun
	}

	writer.accepting.Store(true)
	close(writer.ready)
	defer writer.accepting.Store(false)

	for {
		select {
		case observation := <-writer.queue:
			if ctx.Err() != nil {
				writer.accepting.Store(false)
				return writer.drain(observation)
			}
			if err := writer.persistObservation(ctx, observation); err != nil {
				return err
			}
		case <-ctx.Done():
			writer.accepting.Store(false)
			return writer.drain()
		}
	}
}

func (writer *Writer) Emit(ctx context.Context, observation Observation) error {
	if err := observation.Validate(); err != nil {
		return err
	}
	select {
	case <-writer.ready:
	case <-ctx.Done():
		return ctx.Err()
	}

	if !writer.accepting.Load() {
		return ErrWriterNotRunning
	}

	queued := cloneObservation(observation)
	timer := time.NewTimer(writer.enqueueWait)
	defer timer.Stop()
	select {
	case writer.queue <- queued:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		writer.dropped.Add(1)
		return ErrObservationQueueFull
	}
}

func (writer *Writer) persistObservation(ctx context.Context, observation Observation) error {
	if err := writer.persistDroppedCount(ctx); err != nil {
		return err
	}
	return writer.append(ctx, observation)
}

func (writer *Writer) persistDroppedCount(ctx context.Context) error {
	dropped := writer.dropped.Swap(0)
	if dropped == 0 {
		return nil
	}
	payload, err := json.Marshal(struct {
		DroppedObservations uint64 `json:"droppedObservations"`
	}{DroppedObservations: dropped})
	if err != nil {
		writer.dropped.Add(dropped)
		return fmt.Errorf("encode queue overflow event: %w", err)
	}
	health := Observation{
		Category:    domain.EventCategoryHealth,
		Action:      "observation_queue_overflow",
		Severity:    domain.EventSeverityHigh,
		ObservedUTC: writer.now().UTC(),
		Source:      "event_writer",
		Confidence:  domain.EventConfidenceDirect,
		Payload:     payload,
	}
	if err := writer.append(ctx, health); err != nil {
		writer.dropped.Add(dropped)
		return err
	}
	return nil
}

func (writer *Writer) append(ctx context.Context, observation Observation) error {
	eventID, err := writer.newEventID()
	if err != nil {
		return fmt.Errorf("create audit event id: %w", err)
	}
	event := domain.AuditEvent{
		EventID:          eventID,
		SessionID:        writer.sessionID,
		Category:         observation.Category,
		Action:           observation.Action,
		Severity:         observation.Severity,
		ObservedUTC:      observation.ObservedUTC,
		MonotonicTicks:   observation.MonotonicTicks,
		WindowsSessionID: cloneUint32(observation.WindowsSessionID),
		UserSIDHash:      append([]byte(nil), observation.UserSIDHash...),
		ProcessKey:       observation.ProcessKey,
		ObjectKey:        observation.ObjectKey,
		Source:           observation.Source,
		Confidence:       observation.Confidence,
	}
	if _, err := writer.store.AppendEventAutoSequence(ctx, event, observation.Payload); err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	return nil
}

func (writer *Writer) drain(initial ...Observation) error {
	ctx, cancel := context.WithTimeout(context.Background(), writer.drainTimeout)
	defer cancel()
	for _, observation := range initial {
		if err := writer.persistObservation(ctx, observation); err != nil {
			return err
		}
	}
	for {
		select {
		case observation := <-writer.queue:
			if err := writer.persistObservation(ctx, observation); err != nil {
				return err
			}
			continue
		default:
			return writer.persistDroppedCount(ctx)
		}
	}
}

func cloneObservation(observation Observation) Observation {
	observation.WindowsSessionID = cloneUint32(observation.WindowsSessionID)
	observation.UserSIDHash = append([]byte(nil), observation.UserSIDHash...)
	observation.Payload = append([]byte(nil), observation.Payload...)
	return observation
}

func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	copyOfValue := *value
	return &copyOfValue
}

func randomEventID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}
