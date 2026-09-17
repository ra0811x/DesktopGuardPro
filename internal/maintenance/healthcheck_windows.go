package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"desktopguardpro/internal/appbridge"
	coreservice "desktopguardpro/internal/service"
)

const (
	serviceHealthTimeout = 20 * time.Second
	serviceHealthDelay   = 250 * time.Millisecond
)

type serviceHealthProbe func() (coreservice.HealthResult, error)
type healthPause func(context.Context) error

func WaitForWindowsServiceHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), serviceHealthTimeout)
	defer cancel()
	bridge := appbridge.New()
	return waitForServiceHealth(ctx, bridge.GetHealth, pauseHealthCheck)
}

func waitForServiceHealth(ctx context.Context, probe serviceHealthProbe, pause healthPause) error {
	var lastErr error
	for {
		result, err := probe()
		if err == nil && result.Status == coreservice.HealthStatusRunning {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("unexpected service health status %q", result.Status)
		}
		if err := pause(ctx); err != nil {
			return errors.Join(fmt.Errorf("service health check timed out: %w", err), lastErr)
		}
	}
}

func pauseHealthCheck(ctx context.Context) error {
	timer := time.NewTimer(serviceHealthDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
