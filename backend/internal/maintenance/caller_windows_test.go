package maintenance

import (
	"errors"
	"testing"

	"desktopguardpro/internal/installpolicy"
)

func TestMaintenanceCallerDelegationRequiresElevatedLocalSystem(t *testing.T) {
	owner, _ := installpolicy.New(uninstallOwnerSID)
	system, _ := installpolicy.New("S-1-5-18")
	for _, test := range []struct {
		name      string
		caller    installpolicy.Policy
		delegated string
		elevated  bool
		allowed   bool
	}{
		{"owner direct", owner, "", true, true},
		{"MSI system delegation", system, uninstallOwnerSID, true, true},
		{"unelevated owner", owner, "", false, false},
		{"unelevated system", system, uninstallOwnerSID, false, false},
		{"administrator cannot impersonate owner", owner, "S-1-5-21-2000-2001-2002-2003", true, false},
		{"invalid delegated SID", system, "invalid", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveMaintenanceCallerPolicy(test.caller, test.delegated, test.elevated)
			if !test.allowed {
				if err == nil {
					t.Fatal("untrusted maintenance caller accepted")
				}
				if !test.elevated && !errors.Is(err, ErrAdministratorRequired) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || got.OwnerUserSID != uninstallOwnerSID {
				t.Fatalf("owner = %s, error = %v", got.OwnerUserSID, err)
			}
		})
	}
}
