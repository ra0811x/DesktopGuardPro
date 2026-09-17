package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestQueryTimelinePaginatesWithStableCursor(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa1}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 5)

	query := TimelineQuery{SessionID: "session-1", Limit: 2}
	first, err := repository.QueryTimeline(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !first.IntegrityVerified || !first.HasMore || len(first.Records) != 2 || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	query.Cursor = first.NextCursor
	second, err := repository.QueryTimeline(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 2 || second.Records[0].Event.Sequence != 3 || !second.HasMore {
		t.Fatalf("unexpected second page: %+v", second)
	}
	query.Cursor = second.NextCursor
	third, err := repository.QueryTimeline(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Records) != 1 || third.Records[0].Event.Sequence != 5 || third.HasMore || third.NextCursor != "" {
		t.Fatalf("unexpected third page: %+v", third)
	}
}

func TestQueryTimelineFiltersCategoryAndTime(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa2}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 6)
	from := timelineTestTime(2)
	to := timelineTestTime(5)
	page, err := repository.QueryTimeline(context.Background(), TimelineQuery{
		SessionID: "session-1", Limit: 10, Categories: []domain.EventCategory{domain.EventCategoryProcess},
		FromUTC: &from, ToUTC: &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 2 || page.Records[0].Event.Sequence != 3 || page.Records[1].Event.Sequence != 5 {
		t.Fatalf("unexpected filtered records: %+v", page.Records)
	}
}

func TestQueryTimelineRejectsCursorWithChangedFilter(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa3}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 3)
	first, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.QueryTimeline(context.Background(), TimelineQuery{
		SessionID: "session-1", Limit: 1, Cursor: first.NextCursor,
		Categories: []domain.EventCategory{domain.EventCategoryFile},
	})
	if !errors.Is(err, ErrTimelineCursorInvalid) {
		t.Fatalf("expected invalid cursor, got %v", err)
	}
}

func TestQueryTimelineFiltersSeverityUserProcessAndPath(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa7}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	digest := sha256.Sum256([]byte("S-1-5-21-1000"))
	event := testAuditEvent(1)
	event.EventID = "filtered-1"
	event.Severity = domain.EventSeverityHigh
	event.UserSIDHash = digest[:]
	event.ProcessKey = `C:\Apps\editor.exe`
	event.ObjectKey = `C:\Evidence\report.docx`
	if _, err := repository.AppendEvent(context.Background(), event, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	page, err := repository.QueryTimeline(context.Background(), TimelineQuery{
		SessionID: "session-1", Severities: []domain.EventSeverity{domain.EventSeverityHigh},
		UserSIDHash: hex.EncodeToString(digest[:]), Process: "EDITOR", Path: `evidence\report`,
	})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("filtered page=%+v error=%v", page, err)
	}
}

func TestQueryTimelineFiltersDecryptedPayloadByUsernameOrSID(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa8}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	event := testAuditEvent(1)
	event.EventID = "user-filter-1"
	if _, err := repository.AppendEvent(context.Background(), event, []byte(`{"userName":"WORKSTATION\\Raymond","userSid":"S-1-5-21-1000"}`)); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"raymond", "s-1-5-21-1000"} {
		page, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1", User: user})
		if err != nil || len(page.Records) != 1 {
			t.Fatalf("user %q page=%+v error=%v", user, page, err)
		}
	}
}

func TestQueryTimelineFiltersProcessAndPathFromDecryptedPayload(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa9}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	event := testAuditEvent(1)
	event.EventID, event.ProcessKey, event.ObjectKey = "detail-filter-1", "42:123", ""
	payload := []byte(`{"imagePath":"C:\\Apps\\editor.exe","newPath":"C:\\Evidence\\renamed.docx"}`)
	if _, err := repository.AppendEvent(context.Background(), event, payload); err != nil {
		t.Fatal(err)
	}
	page, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1", Process: "EDITOR.EXE", Path: `evidence\renamed`})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
}

func TestQueryTimelineVerifiesEventsBeforeCursor(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa4}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 3)
	first, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE audit_events SET action = 'tampered' WHERE sequence = 1"); err != nil {
		t.Fatal(err)
	}
	_, err = repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1", Limit: 1, Cursor: first.NextCursor})
	if !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("expected integrity error, got %v", err)
	}
}

func TestQueryTimelineDetectsTruncatedEventChain(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		deleteSQL  string
		deleteArgs []any
	}{
		{
			name:       "tail event",
			deleteSQL:  "DELETE FROM audit_events WHERE session_id = ? AND sequence = ?",
			deleteArgs: []any{"session-1", 3},
		},
		{
			name:       "all events",
			deleteSQL:  "DELETE FROM audit_events WHERE session_id = ?",
			deleteArgs: []any{"session-1"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa5}, payloadKeySize))
			t.Cleanup(func() { _ = db.Close() })
			createTestSession(t, repository)
			appendTimelineEvents(t, repository, 3)
			if _, err := db.ExecContext(context.Background(), testCase.deleteSQL, testCase.deleteArgs...); err != nil {
				t.Fatalf("truncate event chain: %v", err)
			}

			if _, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1"}); !errors.Is(err, ErrEventChain) {
				t.Fatalf("QueryTimeline() error = %v, want %v", err, ErrEventChain)
			}
		})
	}
}

func TestQueryTimelineDetectsForgedEventChainCheckpoint(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0xa6}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 3)

	var secondHash, originalCheckpoint []byte
	if err := db.QueryRowContext(context.Background(), "SELECT event_hash FROM audit_events WHERE session_id = ? AND sequence = ?", "session-1", 2).Scan(&secondHash); err != nil {
		t.Fatalf("query second event hash: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT tail_hash FROM event_chain_checkpoints WHERE session_id = ?", "session-1").Scan(&originalCheckpoint); err != nil {
		t.Fatalf("query event chain checkpoint: %v", err)
	}
	forgedCheckpoint := append(append([]byte(nil), secondHash...), originalCheckpoint[eventHashSize:]...)
	if _, err := db.ExecContext(context.Background(), "DELETE FROM audit_events WHERE session_id = ? AND sequence = ?", "session-1", 3); err != nil {
		t.Fatalf("truncate event chain: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
		UPDATE event_chain_checkpoints
		SET event_count = ?, tail_hash = ?
		WHERE session_id = ?
	`, 2, forgedCheckpoint, "session-1"); err != nil {
		t.Fatalf("forge event chain checkpoint: %v", err)
	}

	if _, err := repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: "session-1"}); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("QueryTimeline() error = %v, want %v", err, ErrEventIntegrity)
	}
}

func appendTimelineEvents(t *testing.T, repository *Repository, count int) {
	t.Helper()
	for sequence := 1; sequence <= count; sequence++ {
		event := testAuditEvent(uint64(sequence))
		event.EventID = fmt.Sprintf("timeline-%d", sequence)
		event.ObservedUTC = timelineTestTime(sequence)
		if sequence%2 == 1 {
			event.Category = domain.EventCategoryProcess
		} else {
			event.Category = domain.EventCategoryFile
		}
		if _, err := repository.AppendEvent(context.Background(), event, []byte(fmt.Sprintf(`{"sequence":%d}`, sequence))); err != nil {
			t.Fatalf("append event %d: %v", sequence, err)
		}
	}
}

func timelineTestTime(second int) time.Time {
	return time.Date(2026, 8, 23, 10, 0, second, 0, time.UTC)
}
