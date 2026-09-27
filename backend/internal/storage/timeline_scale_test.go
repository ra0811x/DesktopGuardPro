package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt in because the fixture contains one million authenticated encrypted rows.
func TestTimelineScaleAtHundredThousandAndMillionEvents(t *testing.T) {
	if os.Getenv("DGP_STORAGE_SCALE_TEST") != "1" {
		t.Skip("set DGP_STORAGE_SCALE_TEST=1 for large-database verification")
	}
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "scale.db"), bytes.Repeat([]byte{0x73}, payloadKeySize))
	defer db.Close()
	createTestSession(t, repository)
	for _, count := range []uint64{100_000, 1_000_000} {
		started := time.Now()
		appendScaleFixture(t, repository, count)
		t.Logf("prepared %d encrypted events in %s", count, time.Since(started))
		for _, after := range []uint64{0, count / 2, count - 100} {
			query := TimelineQuery{SessionID: "session-1", Limit: 100}
			if after > 0 {
				query.Cursor, _ = encodeTimelineCursor(timelineCursor{Version: timelineCursorVersion, SessionID: query.SessionID, LastSequence: after, QueryHash: timelineQueryHash(query)})
			}
			started = time.Now()
			page, err := repository.QueryTimeline(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 100 || page.ScannedEvents != 100 || page.Records[0].Event.Sequence != after+1 {
				t.Fatalf("unbounded scale page: count=%d after=%d scanned=%d records=%d", count, after, page.ScannedEvents, len(page.Records))
			}
			t.Logf("count=%d after=%d scanned=%d latency=%s", count, after, page.ScannedEvents, time.Since(started))
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := repository.QueryTimeline(ctx, TimelineQuery{SessionID: "session-1"}); err == nil {
			t.Fatal("canceled query succeeded")
		}
		if db.Stats().InUse != 0 {
			t.Fatal("canceled query retained a database connection")
		}
	}
}

func appendScaleFixture(t *testing.T, repository *Repository, count uint64) {
	t.Helper()
	ctx := context.Background()
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	checkpoint, err := loadEventChainCheckpoint(ctx, tx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO audit_events (
		event_id, session_id, sequence, category, action, severity, observed_utc,
		monotonic_ticks, process_key, object_key, source, confidence,
		payload_nonce, payload_ciphertext, previous_hash, event_hash, keys_encrypted
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	previous := checkpoint.tailHash
	for sequence := checkpoint.eventCount + 1; sequence <= count; sequence++ {
		event := testAuditEvent(sequence)
		event.EventID = fmt.Sprintf("scale-%d", sequence)
		event.PreviousHash = previous
		aad, err := repository.integrity.AssociatedData(event)
		if err != nil {
			t.Fatal(err)
		}
		nonce, ciphertext, err := repository.cipher.Encrypt([]byte(`{}`), aad)
		if err != nil {
			t.Fatal(err)
		}
		event.EncryptedPayload = ciphertext
		event.EventHash, err = repository.integrity.Compute(event, nonce)
		if err != nil {
			t.Fatal(err)
		}
		keys, err := repository.sealEventKeys(event)
		if err != nil {
			t.Fatal(err)
		}
		_, err = statement.ExecContext(ctx, event.EventID, event.SessionID, sequence, event.Category, event.Action, event.Severity,
			formatTimestamp(event.ObservedUTC), event.MonotonicTicks, keys.ProcessKey, keys.ObjectKey, event.Source, event.Confidence,
			nonce, ciphertext, previous, event.EventHash)
		if err != nil {
			t.Fatal(err)
		}
		previous = event.EventHash
	}
	next, err := repository.newEventChainCheckpoint("session-1", count, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE event_chain_checkpoints SET event_count = ?, tail_hash = ? WHERE session_id = ?", count, next.databaseValue(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
