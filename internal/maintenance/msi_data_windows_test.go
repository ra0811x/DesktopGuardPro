package maintenance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMSIDataBackupRestoresPreMigrationDatabaseAndRejectsTamper(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "tamper"}[tamper], func(t *testing.T) {
			directory := t.TempDir()
			journal := msiJournal{Options: MSIOptions{DataDirectory: directory, TransactionID: "{1234}"}}
			for name, content := range map[string]string{"desktop-guard.db": "old schema and audit rows", "storage.key": "protected old key"} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := backupMSIData(&journal); err != nil {
				t.Fatal(err)
			}
			journal.DataBackupReady = true
			if err := os.WriteFile(filepath.Join(directory, "desktop-guard.db"), []byte("new migrated database"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "desktop-guard.db-wal"), []byte("new WAL"), 0600); err != nil {
				t.Fatal(err)
			}
			if tamper {
				if err := os.WriteFile(filepath.Join(msiBackupDirectory(journal), "desktop-guard.db"), []byte("corrupted backup"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := restoreMSIBackup(journal)
			if (err != nil) != tamper {
				t.Fatalf("restore error = %v", err)
			}
			got, readErr := os.ReadFile(filepath.Join(directory, "desktop-guard.db"))
			want := "old schema and audit rows"
			if tamper {
				want = "new migrated database"
			}
			if readErr != nil || string(got) != want {
				t.Fatalf("database=%q, %v", got, readErr)
			}
			if !tamper {
				if _, err := os.Stat(filepath.Join(directory, "desktop-guard.db-wal")); !os.IsNotExist(err) {
					t.Fatalf("new WAL survived old database restoration: %v", err)
				}
			}
		})
	}
}

func TestMSIDataBackupAllocatesNewDirectoryWhenTransactionIDRepeats(t *testing.T) {
	directory := t.TempDir()
	options := MSIOptions{DataDirectory: directory, TransactionID: "{1234}"}
	if err := os.WriteFile(filepath.Join(directory, "desktop-guard.db"), []byte("first install"), 0600); err != nil {
		t.Fatal(err)
	}
	first := msiJournal{Options: options}
	if err := backupMSIData(&first); err != nil {
		t.Fatal(err)
	}
	firstDirectory := msiBackupDirectory(first)
	if err := os.WriteFile(filepath.Join(directory, "desktop-guard.db"), []byte("reinstall"), 0600); err != nil {
		t.Fatal(err)
	}
	second := msiJournal{Options: options}
	if err := backupMSIData(&second); err != nil {
		t.Fatalf("reinstall backup failed: %v", err)
	}
	secondDirectory := msiBackupDirectory(second)
	if secondDirectory == firstDirectory {
		t.Fatalf("reinstall reused recovery directory %q", secondDirectory)
	}
	got, err := os.ReadFile(filepath.Join(secondDirectory, "desktop-guard.db"))
	if err != nil || string(got) != "reinstall" {
		t.Fatalf("reinstall backup = %q, %v", got, err)
	}
}

func TestPruneMSIRecoveryBackupsKeepsCurrentAndNewestProtectedCopies(t *testing.T) {
	directory := t.TempDir()
	names := []string{
		".msi-backup-oldest-a", ".msi-backup-oldest-b", ".msi-backup-recent-a", ".msi-backup-current",
	}
	for index, name := range names {
		path := filepath.Join(directory, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, time.September, 1+index, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := pruneMSIRecoveryBackups(directory, ".msi-backup-current", 3)
	if err != nil {
		t.Fatalf("pruneMSIRecoveryBackups() error = %v", err)
	}
	if len(removed) != 1 || removed[0] != ".msi-backup-oldest-a" {
		t.Fatalf("removed backups = %q", removed)
	}
	if _, err := os.Stat(filepath.Join(directory, ".msi-backup-current")); err != nil {
		t.Fatalf("current backup was removed: %v", err)
	}
}
