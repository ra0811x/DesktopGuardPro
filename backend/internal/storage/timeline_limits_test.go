package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/ipc"
)

func TestTimelineReadsAndVerifiesOnlyItsBoundedSequenceRange(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "paged.db"), bytes.Repeat([]byte{0x72}, payloadKeySize))
	defer db.Close()
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 10)
	if _, err := db.Exec("UPDATE audit_events SET action = 'tampered' WHERE sequence = 10"); err != nil {
		t.Fatal(err)
	}
	query := TimelineQuery{SessionID: "session-1", Limit: 2}
	page, err := repository.QueryTimeline(context.Background(), query)
	if err != nil {
		t.Fatalf("page scanned unrelated future events: %v", err)
	}
	encoded, _ := json.Marshal(page)
	var scope struct {
		IntegrityScope string `json:"integrityScope"`
		ScannedEvents  int    `json:"scannedEvents"`
	}
	if err := json.Unmarshal(encoded, &scope); err != nil {
		t.Fatal(err)
	}
	if scope.IntegrityScope != "page" || scope.ScannedEvents != 2 {
		t.Fatalf("unbounded or unspecified verification scope: %+v", scope)
	}
	for page.HasMore {
		query.Cursor = page.NextCursor
		page, err = repository.QueryTimeline(context.Background(), query)
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("tampered page escaped verification: %v", err)
	}
	if _, err := repository.ListEvents(context.Background(), query.SessionID); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("whole-session validation missed tampering: %v", err)
	}
}

func TestTimelineLargeEventFitsFrameAndPreservesFullStoredPayload(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "large.db"), bytes.Repeat([]byte{0x71}, payloadKeySize))
	defer db.Close()
	createTestSession(t, repository)
	event := testAuditEvent(1)
	payload := []byte(`{"data":"` + string(bytes.Repeat([]byte{'a'}, 1<<20)) + `"}`)
	if _, err := repository.AppendEvent(context.Background(), event, payload); err != nil {
		t.Fatal(err)
	}
	event.Sequence, event.EventID = 2, "following"
	if _, err := repository.AppendEvent(context.Background(), event, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	page, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: event.SessionID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	message, err := contracts.NewMessage("large", contracts.MessageTypeTimelineResult, time.Now().UTC().Add(time.Minute), page)
	if err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	if err := ipc.WriteMessage(&frame, message); err != nil {
		t.Fatalf("legal event cannot be transmitted: %v", err)
	}
	if !page.HasMore || len(page.Records) != 1 {
		t.Fatal("large event prevented pagination")
	}
	if !page.Records[0].PreviewTruncated || len(page.Records[0].Event.EncryptedPayload) != 0 {
		t.Fatal("preview must identify truncation and omit duplicate ciphertext")
	}
	following, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: event.SessionID, Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(following.Records) != 1 || following.Records[0].Event.Sequence != 2 {
		t.Fatalf("following page inaccessible: %v", err)
	}
	full, err := repository.ListEvents(context.Background(), event.SessionID)
	if err != nil || !bytes.Equal(full[0].Payload, payload) {
		t.Fatalf("stored payload was truncated: %v", err)
	}
}
