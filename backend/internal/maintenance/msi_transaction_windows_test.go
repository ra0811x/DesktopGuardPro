package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"desktopguardpro/internal/maintenancegate"
)

func TestMSIPreflightRejectsOwnerPublisherAndActiveBeforeStop(t *testing.T) {
	for _, failure := range []string{"owner", "publisher", "active"} {
		t.Run(failure, func(t *testing.T) {
			options, dependencies := msiTestFixture(t)
			if failure == "owner" {
				dependencies.ownerSID = "another owner"
			}
			if failure == "publisher" {
				dependencies.signer.SHA256 = strings.Repeat("b", 64)
			}
			if failure == "active" {
				dependencies.checkSession = func(msiSnapshot) error { return ErrActiveProtectionSession }
			}
			dependencies.stop = func() error { t.Fatal("preflight failure stopped the service"); return nil }
			if _, err := runMSITransaction(context.Background(), "prepare", options, dependencies); err == nil {
				t.Fatal("unsafe MSI preflight accepted")
			}
			if _, err := os.Stat(filepath.Join(options.DataDirectory, maintenancegate.JournalFileName)); !os.IsNotExist(err) {
				t.Fatalf("failed preflight created a transaction: %v", err)
			}
		})
	}
}

func TestMSIRollbackRetainsOriginalStateAfterHealthOrLateFailure(t *testing.T) {
	for _, phase := range []string{"health", "late", "stop"} {
		t.Run(phase, func(t *testing.T) {
			options, dependencies := msiTestFixture(t)
			injected := errors.New("injected " + phase + " failure")
			if phase == "stop" {
				dependencies.stop = func() error { return injected }
			}
			_, err := runMSITransaction(context.Background(), "prepare", options, dependencies)
			if phase != "stop" && err != nil {
				t.Fatal(err)
			}
			if phase == "health" {
				dependencies.apply = func(msiJournal) error { return injected }
			}
			dependencies.stop = func() error { return nil }
			if phase != "stop" {
				_, err = runMSITransaction(context.Background(), "apply", options, dependencies)
				if phase == "health" && !errors.Is(err, injected) {
					t.Fatalf("health failure lost: %v", err)
				}
				if phase == "late" && err != nil {
					t.Fatal(err)
				}
			}
			restored := false
			dependencies.restore = func(journal msiJournal) error {
				if journal.Before.Manifest.ProductVersion != "1.0.0" || string(journal.Before.PolicyJSON) != "original policy" || !journal.Before.ServiceRunning {
					t.Fatalf("rollback snapshot changed: %+v", journal.Before)
				}
				restored = true
				return nil
			}
			if _, err := runMSITransaction(context.Background(), "stop-rollback", options, dependencies); err != nil {
				t.Fatal(err)
			}
			if _, err := runMSITransaction(context.Background(), "rollback", options, dependencies); err != nil || !restored {
				t.Fatalf("rollback failed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(options.DataDirectory, maintenancegate.JournalFileName)); !os.IsNotExist(err) {
				t.Fatalf("successful rollback left freeze: %v", err)
			}
		})
	}
}

func TestMSICommitRequiresAppliedHealthyVersionAndKeepsRecoveryOnFailure(t *testing.T) {
	options, dependencies := msiTestFixture(t)
	if _, err := runMSITransaction(context.Background(), "prepare", options, dependencies); err != nil {
		t.Fatal(err)
	}
	if _, err := runMSITransaction(context.Background(), "commit", options, dependencies); err == nil {
		t.Fatal("unapplied MSI committed")
	}
	if _, err := runMSITransaction(context.Background(), "apply", options, dependencies); err != nil {
		t.Fatal(err)
	}
	dependencies.finish = func(msiJournal) error { return errors.New("health lost before commit") }
	if _, err := runMSITransaction(context.Background(), "commit", options, dependencies); err == nil {
		t.Fatal("unhealthy MSI committed")
	}
	if _, err := os.Stat(filepath.Join(options.DataDirectory, maintenancegate.JournalFileName)); err != nil {
		t.Fatal("recovery journal lost", err)
	}
	dependencies.finish = func(msiJournal) error { return nil }
	if _, err := runMSITransaction(context.Background(), "commit", options, dependencies); err != nil {
		t.Fatal(err)
	}
}

func msiTestFixture(t *testing.T) (MSIOptions, msiDependencies) {
	t.Helper()
	options := MSIOptions{InstallDirectory: `C:\Program Files\Desktop Guard Pro`, DataDirectory: t.TempDir(), TransactionID: "{12345678-1234-1234-1234-123456789012}", Version: "1.1.0"}
	signer := SignatureIdentity{SHA256: strings.Repeat("a", 64), Subject: "Test publisher"}
	return options, msiDependencies{
		backup:   func(*msiJournal) error { return nil },
		ownerSID: uninstallOwnerSID, signer: signer,
		snapshot: func() (msiSnapshot, error) {
			return msiSnapshot{OwnerSID: uninstallOwnerSID, Manifest: InstallManifest{ProductVersion: "1.0.0", SignerSHA256: signer.SHA256}, PolicyJSON: []byte("original policy"), ServiceExisted: true, ServiceRunning: true}, nil
		},
		checkSession: func(msiSnapshot) error { return nil }, stop: func() error { return nil },
		apply: func(msiJournal) error { return nil }, restore: func(msiJournal) error { return nil }, finish: func(msiJournal) error { return nil },
	}
}

func TestMSIWillNotApplyUntilStoppedDataBackupIsDurable(t *testing.T) {
	options, dependencies := msiTestFixture(t)
	stopped := false
	dependencies.stop = func() error { stopped = true; return nil }
	dependencies.backup = func(journal *msiJournal) error {
		if !stopped {
			t.Fatal("copied a live SQLite database")
		}
		return errors.New("backup failed")
	}
	if _, err := runMSITransaction(context.Background(), "prepare", options, dependencies); err == nil {
		t.Fatal("backup failure ignored")
	}
	dependencies.apply = func(msiJournal) error { t.Fatal("started new code without rollback data"); return nil }
	if _, err := runMSITransaction(context.Background(), "apply", options, dependencies); err == nil {
		t.Fatal("incomplete preparation accepted")
	}
}
