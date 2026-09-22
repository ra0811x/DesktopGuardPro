package agent

import "testing"

func TestDeskGuardAdapterUsesOriginalUnlockStates(t *testing.T) {
	shield := newWindowsInputShield()
	// Exercise the copied engine without installing any global input hooks.
	shield.running.Store(true)
	shield.engine.Protect()
	shield.RequestUnlock()
	if shield.State() != inputShieldVerifying {
		t.Fatal("DeskGuard BeginUnlock was not used")
	}
	select {
	case <-shield.UnlockRequests():
	default:
		t.Fatal("unlock prompt not requested")
	}
	shield.RequestUnlock()
	select {
	case <-shield.UnlockRequests():
		t.Fatal("duplicate prompt requested")
	default:
	}
	shield.CompleteVerification(false)
	if !shield.engine.IsProtecting() {
		t.Fatal("cancel must call DeskGuard Protect")
	}
	shield.RequestUnlock()
	shield.CompleteVerification(true)
	if shield.State() != inputShieldDisabled {
		t.Fatal("successful password must call DeskGuard Unprotect")
	}
	shield.running.Store(false)
	shield.CompleteVerification(false)
	if shield.State() != inputShieldDisabled {
		t.Fatal("stale prompt relocked a stopped task")
	}
}
