package maintenance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/installpolicy"
)

func TestVerifyWindowsInstallationReportsAllHealthyChecks(t *testing.T) {
	dependencies := successfulVerificationDependencies()
	result, err := verifyWindowsInstallation(`C:\ProgramData\DesktopGuardPro`, dependencies)
	if err != nil {
		t.Fatalf("verifyWindowsInstallation() error = %v", err)
	}
	if !result.Healthy || len(result.Checks) != 7 {
		t.Fatalf("verification result = %#v", result)
	}
	for _, check := range result.Checks {
		if !check.Passed {
			t.Fatalf("failed check = %#v", check)
		}
	}
}

func TestVerifyWindowsInstallationCollectsIndependentFailures(t *testing.T) {
	dependencies := successfulVerificationDependencies()
	dependencies.verifyComponents = func(InstallManifest) error { return errors.New("hash mismatch") }
	dependencies.waitForHealth = func() error { return errors.New("service unavailable") }
	result, err := verifyWindowsInstallation(`C:\ProgramData\DesktopGuardPro`, dependencies)
	if err == nil || result.Healthy {
		t.Fatalf("verification result=%#v error=%v", result, err)
	}
	failed := 0
	for _, check := range result.Checks {
		if !check.Passed {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("failed checks = %d, want 2: %#v", failed, result.Checks)
	}
}

func successfulVerificationDependencies() verificationDependencies {
	policy, _ := installpolicy.New(uninstallOwnerSID)
	signer := strings.Repeat("a", 64)
	manifest := InstallManifest{
		SchemaVersion: installManifestVersion, ProductVersion: "1.0.0", InstalledUTC: time.Now().UTC(),
		SignerSHA256: signer, SignerSubject: "Desktop Guard Pro Test",
		Components: []InstallComponentRecord{
			{Name: "service", Path: `C:\Program Files\Desktop Guard Pro\desktop-guard-service.exe`, Size: 1, SHA256: signer, Signature: "trusted"},
			{Name: "ui", Path: `C:\Program Files\Desktop Guard Pro\desktop-guard-ui.exe`, Size: 1, SHA256: signer, Signature: "trusted"},
			{Name: "agent", Path: `C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`, Size: 1, SHA256: signer, Signature: "trusted"},
		},
	}
	return verificationDependencies{
		expectedDataDirectory: func() (string, error) { return `C:\ProgramData\DesktopGuardPro`, nil },
		loadPolicy:            func(string) (installpolicy.Policy, error) { return policy, nil },
		currentPolicy:         func() (installpolicy.Policy, error) { return policy, nil },
		loadManifest:          func(string) (InstallManifest, error) { return manifest, nil },
		verifyComponents:      func(InstallManifest) error { return nil },
		preflight: func(ValidatedInstallOptions) (PreflightReport, error) {
			return PreflightReport{SignerSHA256: signer}, nil
		},
		loadRestoration: func(string) (RestorationJournal, error) { return RestorationJournal{Version: 1}, nil },
		waitForHealth:   func() error { return nil },
		now:             time.Now,
	}
}
