package collector

import (
	"context"
	"testing"
	"time"
)

func TestClockJumpCollectorDetectsWallClockChangeAgainstMonotonicTime(t *testing.T) {
	base := time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC)
	readings := []ClockReading{
		{WallUTC: base, MonotonicTicks: 0},
		{WallUTC: base.Add(11 * time.Second), MonotonicTicks: time.Second},
	}
	index := 0
	collector, err := newClockJumpCollector(func() ClockReading {
		if index >= len(readings) {
			return readings[len(readings)-1]
		}
		reading := readings[index]
		index++
		return reading
	}, time.Millisecond, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &observationSink{onEmit: func(int) { cancel() }}
	if err := collector.Run(ctx, sink); err != nil {
		t.Fatal(err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "system_clock_jump_detected" {
		t.Fatalf("observations=%#v", observations)
	}
}
