package maintenance

import (
	"errors"
	"testing"
	"time"

	"desktopguardpro/internal/installpolicy"
)

const uninstallOwnerSID = "S-1-5-21-1000-1001-1002-1003"

func TestUninstallWindowsRestoresSystemBeforeServiceRemoval(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	result, err := uninstallWindows(UninstallOptions{}, dependencies)
	if err != nil {
		t.Fatalf("uninstallWindows() error = %v", err)
	}
	want := []string{"expected-data", "validate", "load-policy", "load-manifest", "current-policy", "check-session", "restore", "unregister-startup", "delete-service", "schedule-removal", "write-log"}
	assertCalls(t, calls, want)
	if !result.DataPreserved || result.Disposition != DataDispositionPreserve || result.CompletedUTC.IsZero() {
		t.Fatalf("uninstall result = %#v", result)
	}
}

func TestUninstallWindowsDeletesDataAfterAllCleanup(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	result, err := uninstallWindows(UninstallOptions{Disposition: DataDispositionDeleteNow, DeletionConfirmation: "DELETE " + uninstallOwnerSID}, dependencies)
	if err != nil {
		t.Fatalf("uninstallWindows() error = %v", err)
	}
	want := []string{"expected-data", "validate", "load-policy", "load-manifest", "current-policy", "check-session", "restore", "unregister-startup", "delete-service", "schedule-removal", "remove-data", "write-log"}
	assertCalls(t, calls, want)
	if result.DataPreserved {
		t.Fatal("DataPreserved = true, want false")
	}
}

func TestMSIManagedUninstallLeavesProgramRemovalToWindowsInstaller(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	result, err := uninstallWindows(UninstallOptions{ManagedByMSI: true}, dependencies)
	if err != nil {
		t.Fatalf("uninstallWindows() error = %v", err)
	}
	want := []string{"expected-data", "validate", "load-policy", "load-manifest", "current-policy", "check-session", "restore", "unregister-startup", "delete-service", "write-log"}
	assertCalls(t, calls, want)
	if result.RestartRequired {
		t.Fatal("MSI-managed uninstall unexpectedly requires a restart")
	}
}

func TestUninstallWindowsStopsBeforeMutationOnOwnerMismatch(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	dependencies.currentPolicy = func() (installpolicy.Policy, error) {
		calls = append(calls, "current-policy")
		return installpolicy.New("S-1-5-21-2000-2001-2002-2003")
	}
	_, err := uninstallWindows(UninstallOptions{}, dependencies)
	if !errors.Is(err, installpolicy.ErrPolicyOwnerMismatch) {
		t.Fatalf("uninstallWindows() error = %v", err)
	}
	assertCalls(t, calls, []string{"expected-data", "validate", "load-policy", "load-manifest", "current-policy"})
}

func TestUninstallWindowsStopsBeforeMutationForActiveSession(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	dependencies.checkNoActiveSession = func() error {
		calls = append(calls, "check-session")
		return ErrActiveProtectionSession
	}
	_, err := uninstallWindows(UninstallOptions{}, dependencies)
	if !errors.Is(err, ErrActiveProtectionSession) {
		t.Fatalf("uninstallWindows() error = %v", err)
	}
	assertCalls(t, calls, []string{"expected-data", "validate", "load-policy", "load-manifest", "current-policy", "check-session", "write-log"})
}

func TestUninstallRetryAfterRemovalSchedulingFailureRetainsOwnerGateAndLog(t *testing.T) {
	var calls []string
	dependencies := successfulUninstallerDependencies(&calls)
	schedulingErr := errors.New("restart registration denied")
	attempts := 0
	dependencies.scheduleProgramRemoval = func(InstallManifest) error {
		attempts++
		if attempts == 1 {
			return schedulingErr
		}
		return nil
	}
	logged := 0
	dependencies.writeLog = func(result UninstallResult, owner string) (string, error) {
		logged++
		if owner != uninstallOwnerSID {
			t.Fatal("failure log owner changed")
		}
		if logged == 1 && (result.Failure == "" || result.Stage != "schedule_program_removal") {
			t.Fatalf("failure stage missing: %+v", result)
		}
		return "result.json", nil
	}
	result, err := uninstallWindows(UninstallOptions{}, dependencies)
	if !errors.Is(err, schedulingErr) || result.LogPath == "" || logged != 1 {
		t.Fatalf("failure lost diagnostic state: %+v, %v, logs=%d", result, err, logged)
	}
	if _, err := uninstallWindows(UninstallOptions{}, dependencies); err != nil || logged != 2 {
		t.Fatalf("retry failed: %v", err)
	}
}

func successfulUninstallerDependencies(calls *[]string) uninstallerDependencies {
	policy, _ := installpolicy.New(uninstallOwnerSID)
	return uninstallerDependencies{
		expectedDataDirectory: func() (string, error) {
			*calls = append(*calls, "expected-data")
			return `C:\ProgramData\DesktopGuardPro`, nil
		},
		validate: func(options UninstallOptions, _ string) (ValidatedUninstallOptions, error) {
			*calls = append(*calls, "validate")
			disposition := options.Disposition
			if disposition == "" {
				disposition = DataDispositionPreserve
			}
			return ValidatedUninstallOptions{
				DataDirectory: `C:\ProgramData\DesktopGuardPro`, ServiceName: DefaultServiceName,
				ManagedByMSI: options.ManagedByMSI, Disposition: disposition, DeletionConfirmation: options.DeletionConfirmation,
			}, nil
		},
		loadPolicy: func(string) (installpolicy.Policy, error) {
			*calls = append(*calls, "load-policy")
			return policy, nil
		},
		loadManifest: func(string) (InstallManifest, error) {
			*calls = append(*calls, "load-manifest")
			return removalTestManifest(`C:\Program Files\Desktop Guard Pro`), nil
		},
		currentPolicy: func() (installpolicy.Policy, error) {
			*calls = append(*calls, "current-policy")
			return policy, nil
		},
		checkNoActiveSession:   func() error { *calls = append(*calls, "check-session"); return nil },
		restoreSystemChanges:   func(string) error { *calls = append(*calls, "restore"); return nil },
		unregisterStartup:      func() error { *calls = append(*calls, "unregister-startup"); return nil },
		deleteService:          func(string) error { *calls = append(*calls, "delete-service"); return nil },
		scheduleProgramRemoval: func(InstallManifest) error { *calls = append(*calls, "schedule-removal"); return nil },
		removeDataDirectory: func(string, string) error {
			*calls = append(*calls, "remove-data")
			return nil
		},
		writeLog: func(UninstallResult, string) (string, error) {
			*calls = append(*calls, "write-log")
			return `C:\ProgramData\DesktopGuardPro-UninstallLogs\result.json`, nil
		},
		now: func() time.Time { return time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC) },
	}
}
