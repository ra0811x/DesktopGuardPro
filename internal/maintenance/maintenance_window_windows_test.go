package maintenance

import (
	"errors"
	"testing"
)

func TestUpgradeHoldsMaintenanceWindowAcrossCheckAndCommit(t *testing.T) {
	var calls []string
	dependencies, _ := successfulUpgraderDependencies(&calls)
	held := false
	dependencies.beginMaintenance = func(string) (func() error, error) {
		held = true
		return func() error { held = false; return nil }, nil
	}
	for name, original := range map[string]func() error{"check": dependencies.checkNoActiveSession, "health": dependencies.waitForHealth} {
		wrapped := func() error {
			if !held {
				t.Fatalf("maintenance lock not held at %s", name)
			}
			return original()
		}
		if name == "check" {
			dependencies.checkNoActiveSession = wrapped
		} else {
			dependencies.waitForHealth = wrapped
		}
	}
	if _, err := upgradeWindows(UpgradeOptions{}, dependencies); err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("maintenance lock leaked")
	}
}

func TestUninstallReleasesMaintenanceWindowAfterFailure(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	held := false
	dependencies.beginMaintenance = func(string) (func() error, error) {
		held = true
		return func() error { held = false; return nil }, nil
	}
	dependencies.checkNoActiveSession = func() error {
		if !held {
			t.Fatal("session preflight ran without maintenance freeze")
		}
		return ErrActiveProtectionSession
	}
	if _, err := uninstallWindows(UninstallOptions{}, dependencies); !errors.Is(err, ErrActiveProtectionSession) {
		t.Fatal(err)
	}
	if held {
		t.Fatal("maintenance lock leaked on failure")
	}
}
