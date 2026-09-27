package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSensitiveEventKeysNeverReachDatabaseFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	db, repository := openTestRepository(t, path, bytes.Repeat([]byte{0x65}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	event := testAuditEvent(1)
	event.ObjectKey = `C:\Users\privacy-object-85f21\device-instance`
	event.ProcessKey = "privacy-process-458df"
	if _, err := repository.AppendEvent(context.Background(), event, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	var objectKey, processKey string
	if err := db.QueryRow("SELECT object_key, process_key FROM audit_events").Scan(&objectKey, &processKey); err != nil {
		t.Fatal(err)
	}
	if objectKey == event.ObjectKey || processKey == event.ProcessKey {
		t.Fatal("sensitive event keys are stored as plaintext")
	}
	for _, file := range []string{path, path + "-wal"} {
		contents, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte("privacy-object-85f21")) || bytes.Contains(contents, []byte(event.ProcessKey)) {
			t.Fatalf("plaintext key found in %s", file)
		}
	}
	for _, timeline := range []bool{false, true} {
		records, err := repository.ListEvents(context.Background(), event.SessionID)
		if timeline {
			var page TimelinePage
			page, err = repository.QueryTimeline(context.Background(), TimelineQuery{SessionID: event.SessionID})
			records = page.Records
		}
		if err != nil || len(records) != 1 {
			t.Fatalf("read: %v, %d", err, len(records))
		}
		if records[0].Event.ObjectKey != event.ObjectKey || records[0].Event.ProcessKey != event.ProcessKey {
			t.Fatal("authorized read did not restore sensitive keys")
		}
	}
}

func TestLegacySensitiveKeysAreMigratedWithoutChangingChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	key := bytes.Repeat([]byte{0x65}, payloadKeySize)
	db, repository := openTestRepository(t, path, key)
	createTestSession(t, repository)
	event := testAuditEvent(1)
	event.ObjectKey = `C:\Users\legacy-private-abc892\report.txt`
	event.ProcessKey = "legacy-private-process-924ba"
	stored, err := repository.AppendEvent(context.Background(), event, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the previous on-disk representation with the original HMAC.
	if _, err := db.Exec("UPDATE audit_events SET object_key = ?, process_key = ?, keys_encrypted = 0", event.ObjectKey, event.ProcessKey); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, repository = openTestRepository(t, path, key)
	t.Cleanup(func() { _ = db.Close() })
	records, err := repository.ListEvents(context.Background(), event.SessionID)
	if err != nil || len(records) != 1 {
		t.Fatalf("migrated read: %v", err)
	}
	if records[0].Event.ObjectKey != event.ObjectKey || !bytes.Equal(stored.EventHash, records[0].Event.EventHash) {
		t.Fatal("migration changed the logical event or its chain")
	}
	for _, file := range []string{path, path + "-wal"} {
		contents, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte("legacy-private-")) {
			t.Fatalf("legacy plaintext remains in %s", file)
		}
	}
}

func TestEncryptedKeysCannotBeMovedBetweenEvents(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "tamper.db"), bytes.Repeat([]byte{0x65}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	for sequence := uint64(1); sequence <= 2; sequence++ {
		event := testAuditEvent(sequence)
		event.EventID = fmt.Sprintf("event-%d", sequence)
		if _, err := repository.AppendEvent(context.Background(), event, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE audit_events SET object_key = (SELECT object_key FROM audit_events WHERE sequence = 1) WHERE sequence = 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListEvents(context.Background(), "session-1"); err == nil {
		t.Fatal("moved encrypted key was accepted")
	}
}
