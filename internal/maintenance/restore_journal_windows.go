package maintenance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	RestorationJournalFileName = "system-restore-journal.json"
	restorationJournalVersion  = 1
	maximumRestorationEntries  = 4096
	maximumRestorationSize     = 4 * 1024 * 1024
	RestorationKindFileSACL    = "file_sacl"
)

var ErrRestorationJournalInvalid = errors.New("system restoration journal is invalid")

type RestorationEntry struct {
	Kind         string `json:"kind"`
	Target       string `json:"target"`
	OriginalSDDL string `json:"originalSddl"`
}

type RestorationJournal struct {
	Version int                `json:"version"`
	Entries []RestorationEntry `json:"entries"`
}

type systemRestorer func(RestorationEntry) error

func LoadRestorationJournal(dataDirectory string) (RestorationJournal, error) {
	path := filepath.Join(dataDirectory, RestorationJournalFileName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return RestorationJournal{Version: restorationJournalVersion}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumRestorationSize {
		return RestorationJournal{}, ErrRestorationJournalInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return RestorationJournal{}, fmt.Errorf("open restoration journal: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maximumRestorationSize+1))
	if err != nil || len(encoded) > maximumRestorationSize {
		return RestorationJournal{}, ErrRestorationJournalInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var journal RestorationJournal
	if err := decoder.Decode(&journal); err != nil {
		return RestorationJournal{}, fmt.Errorf("%w: %v", ErrRestorationJournalInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return RestorationJournal{}, err
	}
	if err := journal.Validate(); err != nil {
		return RestorationJournal{}, err
	}
	return journal, nil
}

func SaveRestorationJournal(dataDirectory string, journal RestorationJournal) error {
	if err := journal.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(journal, "", "  ")
	if err != nil || len(encoded) > maximumRestorationSize {
		return ErrRestorationJournalInvalid
	}
	encoded = append(encoded, '\n')
	return writeAtomicMaintenanceFile(dataDirectory, RestorationJournalFileName, encoded)
}

func (journal RestorationJournal) Validate() error {
	if journal.Version != restorationJournalVersion || len(journal.Entries) > maximumRestorationEntries {
		return ErrRestorationJournalInvalid
	}
	seen := make(map[string]struct{}, len(journal.Entries))
	for _, entry := range journal.Entries {
		entry.Target = strings.TrimSpace(entry.Target)
		if entry.Kind != RestorationKindFileSACL || entry.Target == "" || !filepath.IsAbs(entry.Target) || strings.IndexByte(entry.Target, 0) >= 0 || strings.TrimSpace(entry.OriginalSDDL) == "" {
			return ErrRestorationJournalInvalid
		}
		key := strings.ToLower(entry.Kind + "\x00" + filepath.Clean(entry.Target))
		if _, ok := seen[key]; ok {
			return ErrRestorationJournalInvalid
		}
		seen[key] = struct{}{}
	}
	return nil
}

func restoreJournal(journal RestorationJournal, restore systemRestorer) error {
	if err := journal.Validate(); err != nil {
		return err
	}
	for index := len(journal.Entries) - 1; index >= 0; index-- {
		entry := journal.Entries[index]
		if err := restore(entry); err != nil {
			return fmt.Errorf("restore %s %q: %w", entry.Kind, entry.Target, err)
		}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrRestorationJournalInvalid
	}
	return nil
}

func writeAtomicMaintenanceFile(directory, name string, content []byte) error {
	temporary, err := os.CreateTemp(directory, ".maintenance-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := true
	defer func() {
		_ = temporary.Close()
		if keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, name)); err != nil {
		return err
	}
	keep = false
	return nil
}
