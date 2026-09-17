package maintenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/installpolicy"
	"golang.org/x/sys/windows/svc"
)

func TestMSIDataRollbackRestoresExactBytesAndPreservesAuditFiles(t *testing.T) {
	directory := t.TempDir()
	auditPath := filepath.Join(directory, "desktop-guard.db")
	if err := os.WriteFile(auditPath, []byte("audit data"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{installpolicy.FileName, InstallManifestFileName} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("new metadata"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := msiSnapshot{PolicyJSON: []byte("original policy bytes"), ManifestJSON: []byte("original manifest bytes")}
	if err := restoreMSIData(directory, before); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{installpolicy.FileName: "original policy bytes", InstallManifestFileName: "original manifest bytes", "desktop-guard.db": "audit data"} {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(got) != want {
			t.Fatalf("rollback %s = %q, %v", name, got, err)
		}
	}
	if err := restoreMSIData(directory, msiSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(auditPath); err != nil {
		t.Fatal("fresh-install rollback deleted audit data", err)
	}
	if _, err := os.Stat(filepath.Join(directory, installpolicy.FileName)); !os.IsNotExist(err) {
		t.Fatalf("new policy not removed: %v", err)
	}
}

func TestWriteMSIFailureLogCapturesActionableCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "msi-maintenance-error.json")
	observed := time.Date(2026, time.September, 13, 13, 0, 0, 0, time.UTC)
	if err := writeMSIFailureLog(path, msiFailureLog{
		Stage: "prepare", Version: "1.0.21", ObservedUTC: observed,
		Error: "service did not reach a stable state within 15s",
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got msiFailureLog
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Stage != "prepare" || got.Version != "1.0.21" || !got.ObservedUTC.Equal(observed) ||
		!strings.Contains(got.Error, "stable state") {
		t.Fatalf("failure log = %+v", got)
	}
}

func TestMSIInstallAlwaysUsesHealthProbeAndFixedService(t *testing.T) {
	got := msiInstallOptions(MSIOptions{InstallDirectory: `C:\Program Files\Desktop Guard Pro`, DataDirectory: `C:\ProgramData\DesktopGuardPro`, Version: "1.2.3"}, uninstallOwnerSID)
	if got.SkipServiceHealthCheck || got.AllowLegacySystemOwnerMigration || got.ServiceName != DefaultServiceName || got.OwnerUserSID != uninstallOwnerSID {
		t.Fatalf("unsafe MSI install options: %+v", got)
	}
}

func TestWaitForStableMSIServiceStateAllowsRestartManagerTransitions(t *testing.T) {
	for _, test := range []struct {
		name     string
		states   []svc.State
		expected svc.State
	}{
		{name: "stopping", states: []svc.State{svc.StopPending, svc.Stopped}, expected: svc.Stopped},
		{name: "restarting", states: []svc.State{svc.StartPending, svc.Running}, expected: svc.Running},
	} {
		t.Run(test.name, func(t *testing.T) {
			index := 0
			status, err := waitForStableMSIServiceState(func() (svc.Status, error) {
				state := test.states[index]
				if index < len(test.states)-1 {
					index++
				}
				return svc.Status{State: state}, nil
			}, 100*time.Millisecond, time.Millisecond)
			if err != nil || status.State != test.expected {
				t.Fatalf("stable state = %v, error = %v", status.State, err)
			}
		})
	}
}
