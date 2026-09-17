package collector

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"desktopguardpro/internal/domain"
)

const (
	defaultClockJumpInterval  = 5 * time.Second
	defaultClockJumpThreshold = 2 * time.Second
)

type ClockReading struct {
	WallUTC        time.Time
	MonotonicTicks time.Duration
}

type clockReader func() ClockReading

type ClockJumpCollector struct {
	read      clockReader
	interval  time.Duration
	threshold time.Duration
}

func NewClockJumpCollector() (*ClockJumpCollector, error) {
	origin := time.Now()
	return newClockJumpCollector(func() ClockReading {
		current := time.Now()
		return ClockReading{WallUTC: current.UTC(), MonotonicTicks: current.Sub(origin)}
	}, defaultClockJumpInterval, defaultClockJumpThreshold)
}

func newClockJumpCollector(read clockReader, interval, threshold time.Duration) (*ClockJumpCollector, error) {
	if read == nil || interval <= 0 || threshold <= 0 {
		return nil, errors.New("clock jump collector dependency is invalid")
	}
	return &ClockJumpCollector{read: read, interval: interval, threshold: threshold}, nil
}

func (*ClockJumpCollector) Name() string { return "monotonic_clock_comparison" }

func (collector *ClockJumpCollector) Run(ctx context.Context, sink Sink) error {
	previous := collector.read()
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current := collector.read()
			wallElapsed := current.WallUTC.Sub(previous.WallUTC)
			monotonicElapsed := current.MonotonicTicks - previous.MonotonicTicks
			drift := wallElapsed - monotonicElapsed
			if drift < 0 {
				drift = -drift
			}
			if drift >= collector.threshold {
				payload, _ := json.Marshal(struct {
					BeforeUTC          time.Time `json:"beforeUtc"`
					AfterUTC           time.Time `json:"afterUtc"`
					WallElapsedMS      int64     `json:"wallElapsedMillis"`
					MonotonicElapsedMS int64     `json:"monotonicElapsedMillis"`
					DriftMS            int64     `json:"driftMillis"`
				}{previous.WallUTC, current.WallUTC, wallElapsed.Milliseconds(), monotonicElapsed.Milliseconds(), drift.Milliseconds()})
				if err := sink.Emit(ctx, Observation{
					Category: domain.EventCategorySystem, Action: "system_clock_jump_detected", Severity: domain.EventSeverityHigh,
					ObservedUTC: current.WallUTC.UTC(), MonotonicTicks: current.MonotonicTicks.Nanoseconds(),
					ObjectKey: "system-clock", Source: collector.Name(), Confidence: domain.EventConfidenceDirect, Payload: payload,
				}); err != nil {
					return err
				}
			}
			previous = current
		}
	}
}
