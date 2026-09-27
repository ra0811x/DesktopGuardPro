package maintenance

import (
	"strings"

	"desktopguardpro/internal/installpolicy"
	winapi "golang.org/x/sys/windows"
)

// Only an elevated LocalSystem custom action may forward the installing user's
// SID. Interactive callers must use their own token; the installed owner is
// still compared by the maintenance operation before any mutation.
func maintenanceCallerPolicy(delegatedOwnerSID string) (installpolicy.Policy, error) {
	caller, err := installpolicy.NewForCurrentUser()
	if err != nil {
		return installpolicy.Policy{}, err
	}
	return resolveMaintenanceCallerPolicy(caller, delegatedOwnerSID, winapi.GetCurrentProcessToken().IsElevated())
}

func resolveMaintenanceCallerPolicy(caller installpolicy.Policy, delegatedOwnerSID string, elevated bool) (installpolicy.Policy, error) {
	if !elevated {
		return installpolicy.Policy{}, ErrAdministratorRequired
	}
	delegatedOwnerSID = strings.TrimSpace(delegatedOwnerSID)
	if delegatedOwnerSID == "" {
		return caller, nil
	}
	if !strings.EqualFold(caller.OwnerUserSID, "S-1-5-18") {
		return installpolicy.Policy{}, installpolicy.ErrPolicyOwnerMismatch
	}
	return installpolicy.New(delegatedOwnerSID)
}
