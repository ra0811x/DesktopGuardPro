package maintenance

import (
	"context"
	"errors"
	"testing"

	coreservice "desktopguardpro/internal/service"
)

func TestWaitForServiceHealthRetriesUntilRunning(t *testing.T) {
	attempts := 0
	pauses := 0
	probe := func() (coreservice.HealthResult, error) {
		attempts++
		if attempts < 3 {
			return coreservice.HealthResult{}, errors.New("pipe unavailable")
		}
		return coreservice.HealthResult{Status: coreservice.HealthStatusRunning}, nil
	}
	pause := func(context.Context) error {
		pauses++
		return nil
	}

	if err := waitForServiceHealth(context.Background(), probe, pause); err != nil {
		t.Fatalf("waitForServiceHealth() error = %v", err)
	}
	if attempts != 3 || pauses != 2 {
		t.Fatalf("health attempts=%d pauses=%d", attempts, pauses)
	}
}

func TestWaitForServiceHealthReturnsLastFailureOnTimeout(t *testing.T) {
	wantErr := errors.New("access denied")
	probe := func() (coreservice.HealthResult, error) { return coreservice.HealthResult{}, wantErr }
	pause := func(context.Context) error { return context.DeadlineExceeded }

	err := waitForServiceHealth(context.Background(), probe, pause)
	if !errors.Is(err, wantErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitForServiceHealth() error = %v", err)
	}
}
