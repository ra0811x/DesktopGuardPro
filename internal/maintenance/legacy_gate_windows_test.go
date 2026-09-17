package maintenance

import (
	"errors"
	"testing"

	coreservice "desktopguardpro/internal/service"
)

func TestRunningLegacyServiceCannotBypassMaintenanceFreeze(t *testing.T) {
	for _, test := range []struct {
		name               string
		running, supported bool
		probeErr           error
		allowed            bool
	}{
		{"new running service", true, true, nil, true},
		{"legacy running service", true, false, nil, false},
		{"legacy system probe denied", true, false, errors.New("unauthorized"), false},
		{"stopped legacy service", false, false, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateMaintenanceCapability(test.running, coreservice.HealthResult{MaintenanceGate: test.supported}, test.probeErr)
			if (err == nil) != test.allowed {
				t.Fatalf("maintenance capability = %v", err)
			}
		})
	}
}
