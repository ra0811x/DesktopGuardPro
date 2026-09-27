package storage

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestReportRangeRetainsSelectedEventsAndVerifiesWholeChain(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "range.db"), bytes.Repeat([]byte{0x74}, payloadKeySize))
	defer db.Close()
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 5)
	records, total, err := repository.ListEventsRange(context.Background(), "session-1", 2, 4)
	if err != nil || total != 5 || len(records) != 3 || records[0].Event.Sequence != 2 || records[2].Event.Sequence != 4 {
		t.Fatalf("range: %d, %d, %v", total, len(records), err)
	}
	if _, err := db.Exec("UPDATE audit_events SET action = 'tampered' WHERE sequence = 1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ListEventsRange(context.Background(), "session-1", 2, 4); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("range ignored damage elsewhere: %v", err)
	}
}
