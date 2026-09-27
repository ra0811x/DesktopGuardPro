package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultRestartDelay = 2 * time.Second

var (
	ErrCollectorRequired            = errors.New("at least one collector is required")
	ErrCollectorDuplicate           = errors.New("collector names must be unique")
	ErrSinkRequired                 = errors.New("collector sink is required")
	ErrSupervisorAlreadyRunning     = errors.New("collector supervisor is already running")
	ErrCollectorStoppedUnexpectedly = errors.New("collector stopped unexpectedly")
)

type State string

const (
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateDegraded State = "degraded"
	StateStopped  State = "stopped"
)

type Status struct {
	Name          string
	State         State
	LastError     string
	StartedUTC    time.Time
	UpdatedUTC    time.Time
	RestartCount  uint64
	RecoveryCount uint64
}

type Supervisor struct {
	collectors   []Collector
	sink         Sink
	restartDelay time.Duration
	now          func() time.Time

	mutex    sync.RWMutex
	running  bool
	statuses map[string]Status
}

func NewSupervisor(collectors []Collector, sink Sink) (*Supervisor, error) {
	return newSupervisor(collectors, sink, defaultRestartDelay, time.Now)
}

func newSupervisor(
	collectors []Collector,
	sink Sink,
	restartDelay time.Duration,
	now func() time.Time,
) (*Supervisor, error) {
	if len(collectors) == 0 {
		return nil, ErrCollectorRequired
	}
	if sink == nil {
		return nil, ErrSinkRequired
	}
	if restartDelay < 0 {
		return nil, errors.New("collector restart delay must not be negative")
	}

	seen := make(map[string]struct{}, len(collectors))
	statuses := make(map[string]Status, len(collectors))
	copyOfCollectors := append([]Collector(nil), collectors...)
	for _, current := range copyOfCollectors {
		if current == nil {
			return nil, ErrCollectorRequired
		}
		name := strings.TrimSpace(current.Name())
		if name == "" {
			return nil, ErrCollectorNameRequired
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("%w: %s", ErrCollectorDuplicate, name)
		}
		seen[name] = struct{}{}
		statuses[name] = Status{Name: name, State: StateStarting, UpdatedUTC: now().UTC()}
	}

	return &Supervisor{
		collectors:   copyOfCollectors,
		sink:         sink,
		restartDelay: restartDelay,
		now:          now,
		statuses:     statuses,
	}, nil
}

func (supervisor *Supervisor) Run(ctx context.Context) error {
	supervisor.mutex.Lock()
	if supervisor.running {
		supervisor.mutex.Unlock()
		return ErrSupervisorAlreadyRunning
	}
	supervisor.running = true
	supervisor.mutex.Unlock()
	defer func() {
		supervisor.mutex.Lock()
		supervisor.running = false
		supervisor.mutex.Unlock()
	}()

	var wait sync.WaitGroup
	for _, current := range supervisor.collectors {
		wait.Add(1)
		go func() {
			defer wait.Done()
			supervisor.runCollector(ctx, current)
		}()
	}
	wait.Wait()
	return nil
}

func (supervisor *Supervisor) Statuses() []Status {
	supervisor.mutex.RLock()
	defer supervisor.mutex.RUnlock()

	result := make([]Status, 0, len(supervisor.collectors))
	for _, current := range supervisor.collectors {
		result = append(result, supervisor.statuses[strings.TrimSpace(current.Name())])
	}
	return result
}

func (supervisor *Supervisor) runCollector(ctx context.Context, current Collector) {
	name := strings.TrimSpace(current.Name())
	hadFailure := false
	for {
		if ctx.Err() != nil {
			supervisor.updateStatus(name, func(status *Status) {
				status.State = StateStopped
			})
			return
		}

		supervisor.updateStatus(name, func(status *Status) {
			status.State = StateRunning
			status.LastError = ""
			if status.StartedUTC.IsZero() {
				status.StartedUTC = supervisor.now().UTC()
			}
			if hadFailure {
				status.RecoveryCount++
			}
		})
		hadFailure = false

		err := runSafely(ctx, current, supervisor.sink)
		if ctx.Err() != nil {
			supervisor.updateStatus(name, func(status *Status) {
				status.State = StateStopped
			})
			return
		}
		if err == nil {
			err = ErrCollectorStoppedUnexpectedly
		}
		hadFailure = true
		supervisor.updateStatus(name, func(status *Status) {
			status.State = StateDegraded
			status.LastError = err.Error()
			status.RestartCount++
		})

		timer := time.NewTimer(supervisor.restartDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			supervisor.updateStatus(name, func(status *Status) {
				status.State = StateStopped
			})
			return
		case <-timer.C:
		}
	}
}

func (supervisor *Supervisor) updateStatus(name string, update func(*Status)) {
	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()
	status := supervisor.statuses[name]
	update(&status)
	status.UpdatedUTC = supervisor.now().UTC()
	supervisor.statuses[name] = status
}

func runSafely(ctx context.Context, current Collector, sink Sink) (runError error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			runError = fmt.Errorf("collector panic: %v", recovered)
		}
	}()
	return current.Run(ctx, sink)
}
