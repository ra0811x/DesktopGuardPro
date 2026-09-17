package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRestorationJournalTreatsMissingFileAsNoChanges(t *testing.T) {
	journal, err := LoadRestorationJournal(t.TempDir())
	if err != nil {
		t.Fatalf("LoadRestorationJournal() error = %v", err)
	}
	if journal.Version != restorationJournalVersion || len(journal.Entries) != 0 {
		t.Fatalf("journal = %#v", journal)
	}
}

func TestRestoreJournalUsesReverseChangeOrder(t *testing.T) {
	journal := RestorationJournal{Version: restorationJournalVersion, Entries: []RestorationEntry{
		{Kind: RestorationKindFileSACL, Target: `C:\Data\first`, OriginalSDDL: "S:"},
		{Kind: RestorationKindFileSACL, Target: `C:\Data\second`, OriginalSDDL: "S:"},
	}}
	var restored []string
	if err := restoreJournal(journal, func(entry RestorationEntry) error {
		restored = append(restored, entry.Target)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || restored[0] != `C:\Data\second` || restored[1] != `C:\Data\first` {
		t.Fatalf("restored order = %#v", restored)
	}
}

func TestRestoreJournalStopsAtFirstFailure(t *testing.T) {
	wantErr := errors.New("access denied")
	journal := RestorationJournal{Version: restorationJournalVersion, Entries: []RestorationEntry{
		{Kind: RestorationKindFileSACL, Target: `C:\Data\first`, OriginalSDDL: "S:"},
		{Kind: RestorationKindFileSACL, Target: `C:\Data\second`, OriginalSDDL: "S:"},
	}}
	calls := 0
	err := restoreJournal(journal, func(RestorationEntry) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) || calls != 1 {
		t.Fatalf("restoreJournal() error=%v calls=%d", err, calls)
	}
}

func TestLoadRestorationJournalRejectsUnknownFields(t *testing.T) {
	directory := t.TempDir()
	content := []byte(`{"version":1,"entries":[],"unknown":true}`)
	if err := os.WriteFile(filepath.Join(directory, RestorationJournalFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRestorationJournal(directory)
	if !errors.Is(err, ErrRestorationJournalInvalid) {
		t.Fatalf("LoadRestorationJournal() error = %v", err)
	}
}
