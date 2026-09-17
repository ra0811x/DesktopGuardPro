package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var msiDataFileNames = []string{"desktop-guard.db", "desktop-guard.db-wal", "storage.key"}

const maximumMSIRecoveryBackups = 3

func backupUpgradeData(directory string) (func() error, error) {
	journal := msiJournal{Options: MSIOptions{DataDirectory: directory, TransactionID: fmt.Sprintf("upgrade-%d", time.Now().UnixNano())}}
	if err := backupMSIData(&journal); err != nil {
		return nil, err
	}
	journal.DataBackupReady = true
	if err := saveMSIJournal(filepath.Join(msiBackupDirectory(journal), "data-recovery.json"), journal); err != nil {
		return nil, err
	}
	return func() error { return restoreMSIBackup(journal) }, nil
}

func msiBackupDirectory(journal msiJournal) string {
	name := journal.BackupDirectoryName
	if name == "" {
		// Journals created before unique backup directories used the transaction
		// identifier directly. Keep those rollback records readable.
		name = strings.TrimSuffix(msiBackupDirectoryPrefix(journal), "-")
	}
	return filepath.Join(journal.Options.DataDirectory, name)
}

func msiBackupDirectoryPrefix(journal msiJournal) string {
	return ".msi-backup-" + strings.Trim(journal.Options.TransactionID, "{}") + "-"
}

func validMSIBackupDirectoryName(journal msiJournal) bool {
	name := journal.BackupDirectoryName
	return name == filepath.Base(name) && strings.HasPrefix(name, msiBackupDirectoryPrefix(journal)) && len(name) > len(msiBackupDirectoryPrefix(journal))
}

type msiRecoveryBackupDirectory struct {
	name     string
	path     string
	modified time.Time
}

func pruneMSIRecoveryBackups(directory, protectedName string, maximum int) ([]string, error) {
	if maximum < 1 {
		return nil, errors.New("MSI recovery backup limit is invalid")
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve MSI recovery backup directory: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list MSI recovery backups: %w", err)
	}
	candidates := make([]msiRecoveryBackupDirectory, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), ".msi-backup-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		target := filepath.Join(root, entry.Name())
		relative, relativeErr := filepath.Rel(root, target)
		if relativeErr != nil || relative == "." || filepath.IsAbs(relative) || strings.Contains(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("invalid MSI recovery backup path")
		}
		candidates = append(candidates, msiRecoveryBackupDirectory{name: entry.Name(), path: target, modified: info.ModTime()})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].modified.Equal(candidates[right].modified) {
			return candidates[left].name > candidates[right].name
		}
		return candidates[left].modified.After(candidates[right].modified)
	})
	kept := make(map[string]struct{}, maximum)
	if protectedName != "" {
		kept[protectedName] = struct{}{}
	}
	for _, candidate := range candidates {
		if len(kept) >= maximum {
			break
		}
		kept[candidate.name] = struct{}{}
	}
	removed := make([]string, 0)
	for _, candidate := range candidates {
		if _, retain := kept[candidate.name]; retain {
			continue
		}
		relative, err := filepath.Rel(root, candidate.path)
		if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("refuse to remove MSI recovery backup outside data directory")
		}
		if err := os.RemoveAll(candidate.path); err != nil {
			return nil, fmt.Errorf("remove MSI recovery backup %s: %w", candidate.name, err)
		}
		removed = append(removed, candidate.name)
	}
	return removed, nil
}

// The service is stopped and new sessions are frozen before this is called.
// Keep the pre-migration database and DPAPI key in the protected data tree.
func backupMSIData(journal *msiJournal) error {
	if journal.BackupDirectoryName != "" {
		return errors.New("MSI backup directory is already allocated")
	}
	directory, err := os.MkdirTemp(journal.Options.DataDirectory, msiBackupDirectoryPrefix(*journal))
	if err != nil {
		return err
	}
	journal.BackupDirectoryName = filepath.Base(directory)
	for _, name := range msiDataFileNames {
		source := filepath.Join(journal.Options.DataDirectory, name)
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		}
		if err := copyMSIDataFile(source, filepath.Join(directory, name)); err != nil {
			return err
		}
		record, err := hashMSIDataFile(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		record.Name = name
		journal.DataFiles = append(journal.DataFiles, record)
	}
	return nil
}

func restoreMSIBackup(journal msiJournal) error {
	if !journal.DataBackupReady {
		return nil
	}
	records := map[string]msiDataCopy{}
	for _, record := range journal.DataFiles {
		if record.Name != "desktop-guard.db" && record.Name != "desktop-guard.db-wal" && record.Name != "storage.key" {
			return errors.New("invalid MSI backup file name")
		}
		if _, exists := records[record.Name]; exists {
			return errors.New("duplicate MSI backup file")
		}
		actual, err := hashMSIDataFile(filepath.Join(msiBackupDirectory(journal), record.Name))
		if err != nil || actual.Size != record.Size || actual.SHA256 != record.SHA256 {
			return fmt.Errorf("MSI data backup integrity failed: %s: %v", record.Name, err)
		}
		records[record.Name] = record
	}
	// A fresh-install rollback leaves its inert data directory available for
	// inspection. Existing audit data always has a pre-migration DB to restore.
	if _, exists := records["desktop-guard.db"]; !exists {
		return nil
	}
	for _, name := range msiDataFileNames {
		target := filepath.Join(journal.Options.DataDirectory, name)
		if _, exists := records[name]; !exists {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := copyMSIDataFile(filepath.Join(msiBackupDirectory(journal), name), target); err != nil {
			return err
		}
	}
	err := os.Remove(filepath.Join(journal.Options.DataDirectory, "desktop-guard.db-shm"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func hashMSIDataFile(path string) (msiDataCopy, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return msiDataCopy{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return msiDataCopy{}, errors.New("invalid MSI backup file")
	}
	file, err := os.Open(path)
	if err != nil {
		return msiDataCopy{}, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	return msiDataCopy{Size: size, SHA256: hex.EncodeToString(digest.Sum(nil))}, err
}

func copyMSIDataFile(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid MSI data source")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(target), ".msi-data-*")
	if err != nil {
		return err
	}
	defer output.Close()
	defer os.Remove(output.Name())
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Rename(output.Name(), target)
}
