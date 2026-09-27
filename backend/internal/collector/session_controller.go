package collector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"desktopguardpro/internal/domain"
)

const defaultSessionPollInterval = 250 * time.Millisecond

var (
	ErrSessionSourceRequired        = errors.New("session source is required")
	ErrCollectorFactoryRequired     = errors.New("collector factory is required")
	ErrSessionPipelineNotActive     = errors.New("session collector pipeline is not active")
	ErrSessionBaselineReviewPending = errors.New("session baseline review is pending")
)

type SessionSource interface {
	Current() (domain.Session, bool)
}

type CollectorFactory func() ([]Collector, error)

type BaselineCapture func(context.Context, string) error

type SessionControllerOptions struct {
	PollInterval         time.Duration
	Writer               WriterOptions
	CaptureStartBaseline BaselineCapture
	CaptureEndBaseline   BaselineCapture
}

type SessionController struct {
	source       SessionSource
	store        EventStore
	factory      CollectorFactory
	pollInterval time.Duration
	writer       WriterOptions
	captureStart BaselineCapture
	captureEnd   BaselineCapture

	healthMutex        sync.RWMutex
	activePipeline     *sessionPipeline
	controllerFailure  error
	endBaselineSession string
}

func NewSessionController(
	source SessionSource,
	store EventStore,
	factory CollectorFactory,
	options SessionControllerOptions,
) (*SessionController, error) {
	if source == nil {
		return nil, ErrSessionSourceRequired
	}
	if store == nil {
		return nil, ErrEventStoreRequired
	}
	if factory == nil {
		return nil, ErrCollectorFactoryRequired
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultSessionPollInterval
	}
	if options.PollInterval < 0 {
		return nil, errors.New("session poll interval must be positive")
	}
	return &SessionController{
		source:       source,
		store:        store,
		factory:      factory,
		pollInterval: options.PollInterval,
		writer:       options.Writer,
		captureStart: options.CaptureStartBaseline,
		captureEnd:   options.CaptureEndBaseline,
	}, nil
}

func (controller *SessionController) Run(ctx context.Context) error {
	var pipeline *sessionPipeline
	defer func() {
		if pipeline != nil {
			_ = pipeline.stop()
		}
		controller.setActivePipeline(nil)
	}()

	ticker := time.NewTicker(controller.pollInterval)
	defer ticker.Stop()
	for {
		var pipelineErrors <-chan error
		if pipeline != nil {
			pipelineErrors = pipeline.errors
		}
		select {
		case <-ctx.Done():
			if pipeline != nil {
				if err := pipeline.stop(); err != nil {
					return err
				}
				pipeline = nil
			}
			return nil
		case err := <-pipelineErrors:
			if stopErr := pipeline.stop(); stopErr != nil {
				controller.setFailure(errors.Join(err, stopErr))
				return errors.Join(err, stopErr)
			}
			pipeline = nil
			controller.setActivePipeline(nil)
			controller.setFailure(err)
			return err
		case <-ticker.C:
			var err error
			pipeline, err = controller.reconcile(ctx, pipeline)
			if err != nil {
				controller.setFailure(err)
				return err
			}
			controller.setActivePipeline(pipeline)
		}
	}
}

func (controller *SessionController) HealthStatus() State {
	controller.healthMutex.RLock()
	pipeline := controller.activePipeline
	failure := controller.controllerFailure
	controller.healthMutex.RUnlock()
	if failure != nil {
		return StateDegraded
	}
	if pipeline == nil {
		return StateRunning
	}
	for _, status := range pipeline.supervisor.Statuses() {
		if status.State == StateDegraded || status.State == StateStopped {
			return StateDegraded
		}
	}
	return StateRunning
}

func (controller *SessionController) WaitForActive(ctx context.Context, sessionID string) error {
	return controller.waitForPipeline(ctx, sessionID, true)
}

func (controller *SessionController) WaitForStopped(ctx context.Context, sessionID string) error {
	return controller.waitForPipeline(ctx, sessionID, false)
}

func (controller *SessionController) EmitExternalObservation(
	ctx context.Context,
	sessionID string,
	observation Observation,
) error {
	if ctx == nil || sessionID == "" {
		return ErrSessionPipelineNotActive
	}
	controller.healthMutex.RLock()
	pipeline := controller.activePipeline
	if pipeline == nil || pipeline.sessionID != sessionID {
		controller.healthMutex.RUnlock()
		return ErrSessionPipelineNotActive
	}
	writer := pipeline.writer
	controller.healthMutex.RUnlock()
	return writer.Emit(ctx, observation)
}

func (controller *SessionController) waitForPipeline(ctx context.Context, sessionID string, active bool) error {
	if ctx == nil {
		return errors.New("wait context is required")
	}
	if sessionID == "" {
		return errors.New("session id is required")
	}
	ticker := time.NewTicker(controller.pollInterval)
	defer ticker.Stop()
	for {
		controller.healthMutex.RLock()
		pipeline := controller.activePipeline
		failure := controller.controllerFailure
		endBaselineSession := controller.endBaselineSession
		controller.healthMutex.RUnlock()
		if failure != nil {
			return fmt.Errorf("collector controller failed: %w", failure)
		}
		if active {
			session, exists := controller.source.Current()
			if exists && session.ID == sessionID && session.State == domain.SessionStateBaselineReview {
				return ErrSessionBaselineReviewPending
			}
		}
		if pipeline == nil || pipeline.sessionID != sessionID {
			if !active {
				session, exists := controller.source.Current()
				if controller.captureEnd == nil || !exists || session.ID != sessionID ||
					(session.State != domain.SessionStateFinalizing && session.State != domain.SessionStateCompleted) ||
					endBaselineSession == sessionID {
					return nil
				}
			}
		} else if active && pipeline.running() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (controller *SessionController) setActivePipeline(pipeline *sessionPipeline) {
	controller.healthMutex.Lock()
	defer controller.healthMutex.Unlock()
	controller.activePipeline = pipeline
}

func (controller *SessionController) setFailure(err error) {
	controller.healthMutex.Lock()
	defer controller.healthMutex.Unlock()
	controller.controllerFailure = err
}

func (controller *SessionController) reconcile(ctx context.Context, current *sessionPipeline) (*sessionPipeline, error) {
	session, exists := controller.source.Current()
	shouldCollect := exists && (session.State == domain.SessionStatePreparing ||
		session.State == domain.SessionStateActive || session.State == domain.SessionStateDegraded)
	if !shouldCollect {
		if current != nil {
			if err := current.stop(); err != nil {
				return nil, err
			}
		}
		controller.healthMutex.RLock()
		captured := controller.endBaselineSession == session.ID
		controller.healthMutex.RUnlock()
		if exists && !captured && (session.State == domain.SessionStateFinalizing ||
			session.State == domain.SessionStateCompleted) && controller.captureEnd != nil {
			if err := controller.captureEnd(ctx, session.ID); err != nil {
				return nil, fmt.Errorf("capture end baseline for session %s: %w", session.ID, err)
			}
			controller.healthMutex.Lock()
			controller.endBaselineSession = session.ID
			controller.healthMutex.Unlock()
		}
		return nil, nil
	}
	if current != nil && current.sessionID == session.ID {
		return current, nil
	}
	if current != nil {
		if err := current.stop(); err != nil {
			return nil, err
		}
	}
	if controller.captureStart != nil && session.State == domain.SessionStatePreparing {
		if err := controller.captureStart(ctx, session.ID); err != nil {
			return nil, fmt.Errorf("capture start baseline for session %s: %w", session.ID, err)
		}
		refreshed, refreshedExists := controller.source.Current()
		if !refreshedExists || refreshed.ID != session.ID || refreshed.State == domain.SessionStateBaselineReview {
			return nil, nil
		}
		session = refreshed
		if session.State != domain.SessionStatePreparing && session.State != domain.SessionStateActive &&
			session.State != domain.SessionStateDegraded {
			return nil, nil
		}
	}
	collectors, err := controller.factory()
	if err != nil {
		return nil, fmt.Errorf("create collectors for session %s: %w", session.ID, err)
	}
	pipeline, err := newSessionPipeline(session.ID, controller.store, collectors, controller.writer)
	if err != nil {
		return nil, fmt.Errorf("create collector pipeline for session %s: %w", session.ID, err)
	}
	pipeline.start(ctx)
	return pipeline, nil
}

func DefaultSystemCollectors() ([]Collector, error) {
	processes, err := NewProcessCollector(0)
	if err != nil {
		return nil, err
	}
	devices, err := NewDeviceCollector(0)
	if err != nil {
		return nil, err
	}
	assets, err := NewSystemAssetCollector()
	if err != nil {
		return nil, err
	}
	securityLog, err := NewSecurityLogCollector()
	if err != nil {
		return nil, err
	}
	clockJump, err := NewClockJumpCollector()
	if err != nil {
		return nil, err
	}
	softwareRepair, err := NewSoftwareRepairCollector()
	if err != nil {
		return nil, err
	}
	registryCollectors, err := DefaultRegistryCollectors()
	if err != nil {
		return nil, err
	}
	return append([]Collector{processes, devices, assets, securityLog, clockJump, softwareRepair}, registryCollectors...), nil
}

type sessionPipeline struct {
	sessionID  string
	writer     *Writer
	supervisor *Supervisor
	errors     chan error

	collectorsCancel context.CancelFunc
	writerCancel     context.CancelFunc
	collectorsDone   chan struct{}
	writerDone       chan struct{}
	stopOnce         sync.Once
	stopError        error
}

func (pipeline *sessionPipeline) running() bool {
	for _, status := range pipeline.supervisor.Statuses() {
		if status.State != StateRunning {
			return false
		}
	}
	return true
}

func newSessionPipeline(
	sessionID string,
	store EventStore,
	collectors []Collector,
	options WriterOptions,
) (*sessionPipeline, error) {
	writer, err := NewWriter(sessionID, store, options)
	if err != nil {
		return nil, err
	}
	supervisor, err := NewSupervisor(collectors, writer)
	if err != nil {
		return nil, err
	}
	return &sessionPipeline{
		sessionID:      sessionID,
		writer:         writer,
		supervisor:     supervisor,
		errors:         make(chan error, 2),
		collectorsDone: make(chan struct{}),
		writerDone:     make(chan struct{}),
	}, nil
}

func (pipeline *sessionPipeline) start(parent context.Context) {
	// stop() cancels the writer only after collectors finish their bounded
	// final scans, including when the service's parent context is canceled.
	writerContext, writerCancel := context.WithCancel(context.WithoutCancel(parent))
	collectorsContext, collectorsCancel := context.WithCancel(parent)
	pipeline.writerCancel = writerCancel
	pipeline.collectorsCancel = collectorsCancel
	go func() {
		defer close(pipeline.writerDone)
		if err := pipeline.writer.Run(writerContext); err != nil {
			pipeline.errors <- fmt.Errorf("event writer for session %s: %w", pipeline.sessionID, err)
		}
	}()
	go func() {
		defer close(pipeline.collectorsDone)
		if err := pipeline.supervisor.Run(collectorsContext); err != nil && collectorsContext.Err() == nil {
			pipeline.errors <- fmt.Errorf("collector supervisor for session %s: %w", pipeline.sessionID, err)
		}
	}()
}

func (pipeline *sessionPipeline) stop() error {
	pipeline.stopOnce.Do(func() {
		pipeline.collectorsCancel()
		<-pipeline.collectorsDone
		pipeline.writerCancel()
		<-pipeline.writerDone
		select {
		case pipeline.stopError = <-pipeline.errors:
		default:
		}
	})
	return pipeline.stopError
}
