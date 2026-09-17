package collector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type discardSink struct{}

func (discardSink) Emit(context.Context, Observation) error { return nil }

type collectorFunc struct {
	name string
	run  func(context.Context, Sink) error
}

func (collector collectorFunc) Name() string { return collector.name }

func (collector collectorFunc) Run(ctx context.Context, sink Sink) error {
	return collector.run(ctx, sink)
}

func TestSupervisorRestartsFailedCollectorWithoutStoppingOthers(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	recovered := make(chan struct{})
	steadyStarted := make(chan struct{})
	flaky := collectorFunc{name: "flaky", run: func(ctx context.Context, _ Sink) error {
		if attempts.Add(1) == 1 {
			return errors.New("temporary collector failure")
		}
		close(recovered)
		<-ctx.Done()
		return ctx.Err()
	}}
	steady := collectorFunc{name: "steady", run: func(ctx context.Context, _ Sink) error {
		close(steadyStarted)
		<-ctx.Done()
		return ctx.Err()
	}}
	supervisor, err := newSupervisor([]Collector{flaky, steady}, discardSink{}, time.Millisecond, time.Now)
	if err != nil {
		t.Fatalf("newSupervisor() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	select {
	case <-steadyStarted:
	case <-time.After(time.Second):
		t.Fatal("steady collector did not start")
	}
	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("flaky collector did not restart")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	statuses := supervisor.Statuses()
	if statuses[0].State != StateStopped || statuses[0].RestartCount != 1 || statuses[0].RecoveryCount != 1 {
		t.Fatalf("flaky status = %+v", statuses[0])
	}
	if statuses[1].State != StateStopped || statuses[1].RestartCount != 0 {
		t.Fatalf("steady status = %+v", statuses[1])
	}
}

func TestSupervisorRecoversCollectorPanic(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	restarted := make(chan struct{})
	panicking := collectorFunc{name: "panicking", run: func(ctx context.Context, _ Sink) error {
		if attempts.Add(1) == 1 {
			panic("simulated panic")
		}
		close(restarted)
		<-ctx.Done()
		return ctx.Err()
	}}
	supervisor, err := newSupervisor([]Collector{panicking}, discardSink{}, time.Millisecond, time.Now)
	if err != nil {
		t.Fatalf("newSupervisor() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("panicking collector did not restart")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := supervisor.Statuses()[0].RestartCount; got != 1 {
		t.Fatalf("RestartCount = %d, want 1", got)
	}
}

func TestSupervisorValidatesConfiguration(t *testing.T) {
	t.Parallel()

	valid := collectorFunc{name: "duplicate", run: func(context.Context, Sink) error { return nil }}
	if _, err := NewSupervisor(nil, discardSink{}); !errors.Is(err, ErrCollectorRequired) {
		t.Fatalf("NewSupervisor(nil) error = %v, want %v", err, ErrCollectorRequired)
	}
	if _, err := NewSupervisor([]Collector{valid}, nil); !errors.Is(err, ErrSinkRequired) {
		t.Fatalf("NewSupervisor(nil sink) error = %v, want %v", err, ErrSinkRequired)
	}
	if _, err := NewSupervisor([]Collector{valid, valid}, discardSink{}); !errors.Is(err, ErrCollectorDuplicate) {
		t.Fatalf("NewSupervisor(duplicates) error = %v, want %v", err, ErrCollectorDuplicate)
	}
}
