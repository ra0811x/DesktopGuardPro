package maintenance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/installpolicy"
)

func TestUpgradeWindowsCommitsAfterNewServiceHealth(t *testing.T) {
	var calls []string
	dependencies, swap := successfulUpgraderDependencies(&calls)
	result, err := upgradeWindows(UpgradeOptions{}, dependencies)
	if err != nil {
		t.Fatalf("upgradeWindows() error = %v", err)
	}
	want := []string{"validate", "load-manifest", "verify-installed", "load-policy", "current-policy", "check-session", "preflight", "prepare", "unregister", "stop", "activate", "start", "health", "build-manifest", "save-manifest", "register", "commit"}
	assertCalls(t, calls, want)
	if result.PreviousVersion != "1.0.0" || result.CurrentVersion != "1.1.0" || !swap.committed {
		t.Fatalf("upgrade result=%#v swap=%#v", result, swap)
	}
}

func TestUpgradeReplacesMaintenanceAlongsideOtherComponents(t *testing.T) {
	var calls []string
	dependencies, swap := successfulUpgraderDependencies(&calls)
	dependencies.prepareSwap = func(replacements []ComponentReplacement) (componentSwapTransaction, error) {
		if len(replacements) != 4 {
			t.Fatalf("upgrade replaces %d components, want 4", len(replacements))
		}
		if replacements[3].Name != "maintenance" || !strings.HasSuffix(replacements[3].TargetPath, "desktop-guard-maintenance.exe") || replacements[3].TargetPath == replacements[3].StagedPath {
			t.Fatalf("wrong maintenance replacement: %+v", replacements[3])
		}
		return swap, nil
	}
	if _, err := upgradeWindows(UpgradeOptions{}, dependencies); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeCallerMustNotLockInstalledMaintenance(t *testing.T) {
	if err := validateUpgradeCaller(`C:\App\desktop-guard-maintenance.exe`, `C:\App`); !errors.Is(err, ErrUpgradeOptionsInvalid) {
		t.Fatalf("self-replacement was accepted: %v", err)
	}
	if err := validateUpgradeCaller(`C:\Stage\desktop-guard-maintenance.exe`, `C:\App`); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeWindowsRollsBackAfterHealthFailure(t *testing.T) {
	var calls []string
	dependencies, swap := successfulUpgraderDependencies(&calls)
	wantErr := errors.New("new service unhealthy")
	healthCalls := 0
	dependencies.waitForHealth = func() error {
		calls = append(calls, "health")
		healthCalls++
		if healthCalls == 1 {
			return wantErr
		}
		return nil
	}
	_, err := upgradeWindows(UpgradeOptions{}, dependencies)
	if !errors.Is(err, wantErr) || !swap.rolledBack {
		t.Fatalf("upgradeWindows() error=%v swap=%#v", err, swap)
	}
	want := []string{"validate", "load-manifest", "verify-installed", "load-policy", "current-policy", "check-session", "preflight", "prepare", "unregister", "stop", "activate", "start", "health", "stop", "rollback", "register", "start", "health"}
	assertCalls(t, calls, want)
}

func TestUpgradeRestoresDatabaseBeforeRestartingOldRelease(t *testing.T) {
	var calls []string
	dependencies, _ := successfulUpgraderDependencies(&calls)
	restored := false
	dependencies.backupData = func(string) (func() error, error) {
		return func() error { restored = true; return nil }, nil
	}
	healthCalls, starts := 0, 0
	dependencies.startService = func(string) error {
		starts++
		if starts == 2 && !restored {
			t.Fatal("old release started with the migrated database")
		}
		return nil
	}
	dependencies.waitForHealth = func() error {
		healthCalls++
		if healthCalls == 1 {
			return errors.New("new release startup failed")
		}
		return nil
	}
	if _, err := upgradeWindows(UpgradeOptions{}, dependencies); err == nil || !restored || starts != 2 {
		t.Fatalf("rollback = %v, restored=%v starts=%d", err, restored, starts)
	}
}

func TestUpgradeWindowsRejectsDifferentSignerBeforeMutation(t *testing.T) {
	var calls []string
	dependencies, _ := successfulUpgraderDependencies(&calls)
	dependencies.preflight = func(ValidatedInstallOptions) (PreflightReport, error) {
		calls = append(calls, "preflight")
		return PreflightReport{SignerSHA256: strings.Repeat("b", 64)}, nil
	}
	_, err := upgradeWindows(UpgradeOptions{}, dependencies)
	if !errors.Is(err, ErrUpgradePublisherMismatch) {
		t.Fatalf("upgradeWindows() error = %v", err)
	}
	assertCalls(t, calls, []string{"validate", "load-manifest", "verify-installed", "load-policy", "current-policy", "check-session", "preflight"})
}

func TestUpgradeWindowsRestoresOldManifestWhenLateStepFails(t *testing.T) {
	var calls []string
	dependencies, _ := successfulUpgraderDependencies(&calls)
	wantErr := errors.New("startup registry failure")
	registerCalls := 0
	dependencies.registerStartup = func(string) error {
		calls = append(calls, "register")
		registerCalls++
		if registerCalls == 1 {
			return wantErr
		}
		return nil
	}
	var savedVersions []string
	dependencies.saveManifest = func(_ string, manifest InstallManifest) error {
		calls = append(calls, "save-manifest")
		savedVersions = append(savedVersions, manifest.ProductVersion)
		return nil
	}
	_, err := upgradeWindows(UpgradeOptions{}, dependencies)
	if !errors.Is(err, wantErr) {
		t.Fatalf("upgradeWindows() error = %v", err)
	}
	if len(savedVersions) != 2 || savedVersions[1] != "1.0.0" {
		t.Fatalf("saved manifest versions = %#v, want new then 1.0.0", savedVersions)
	}
}

type fakeComponentSwap struct {
	calls      *[]string
	rolledBack bool
	committed  bool
}

func (swap *fakeComponentSwap) Activate() error {
	*swap.calls = append(*swap.calls, "activate")
	return nil
}
func (swap *fakeComponentSwap) Rollback() error {
	*swap.calls = append(*swap.calls, "rollback")
	swap.rolledBack = true
	return nil
}
func (swap *fakeComponentSwap) Commit() error {
	*swap.calls = append(*swap.calls, "commit")
	swap.committed = true
	return nil
}

func successfulUpgraderDependencies(calls *[]string) (upgraderDependencies, *fakeComponentSwap) {
	policy, _ := installpolicy.New(uninstallOwnerSID)
	signer := strings.Repeat("a", 64)
	validated := ValidatedUpgradeOptions{
		Current: ValidatedInstallOptions{DataDirectory: `C:\ProgramData\DesktopGuardPro`, ServiceName: DefaultServiceName, Version: "1.0.0", ServiceExecutable: `C:\App\service.exe`, UIExecutable: `C:\App\ui.exe`, AgentExecutable: `C:\App\agent.exe`},
		Staged:  ValidatedInstallOptions{Version: "1.1.0", ServiceExecutable: `C:\Stage\service.exe`, UIExecutable: `C:\Stage\ui.exe`, AgentExecutable: `C:\Stage\agent.exe`},
	}
	swap := &fakeComponentSwap{calls: calls}
	dependencies := upgraderDependencies{
		validate: func(UpgradeOptions) (ValidatedUpgradeOptions, error) {
			*calls = append(*calls, "validate")
			return validated, nil
		},
		loadManifest: func(string) (InstallManifest, error) {
			*calls = append(*calls, "load-manifest")
			return InstallManifest{ProductVersion: "1.0.0", SignerSHA256: signer}, nil
		},
		verifyInstalled:      func(InstallManifest) error { *calls = append(*calls, "verify-installed"); return nil },
		loadPolicy:           func(string) (installpolicy.Policy, error) { *calls = append(*calls, "load-policy"); return policy, nil },
		currentPolicy:        func() (installpolicy.Policy, error) { *calls = append(*calls, "current-policy"); return policy, nil },
		checkNoActiveSession: func() error { *calls = append(*calls, "check-session"); return nil },
		preflight: func(ValidatedInstallOptions) (PreflightReport, error) {
			*calls = append(*calls, "preflight")
			return PreflightReport{SignerSHA256: signer}, nil
		},
		prepareSwap: func([]ComponentReplacement) (componentSwapTransaction, error) {
			*calls = append(*calls, "prepare")
			return swap, nil
		},
		unregisterStartup: func() error { *calls = append(*calls, "unregister"); return nil },
		registerStartup:   func(string) error { *calls = append(*calls, "register"); return nil },
		stopService:       func(string) error { *calls = append(*calls, "stop"); return nil },
		startService:      func(string) error { *calls = append(*calls, "start"); return nil },
		waitForHealth:     func() error { *calls = append(*calls, "health"); return nil },
		buildManifest: func(ValidatedInstallOptions, PreflightReport, time.Time) (InstallManifest, error) {
			*calls = append(*calls, "build-manifest")
			return InstallManifest{}, nil
		},
		saveManifest: func(string, InstallManifest) error { *calls = append(*calls, "save-manifest"); return nil },
		now:          func() time.Time { return time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC) },
	}
	return dependencies, swap
}
