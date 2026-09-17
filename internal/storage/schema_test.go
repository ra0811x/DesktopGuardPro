package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenInitializesDatabase(t *testing.T) {
	t.Parallel()

	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "data", "guard.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	assertPragmaText(t, db, "journal_mode", "wal")
	assertPragmaInt(t, db, "foreign_keys", 1)
	assertPragmaInt(t, db, "busy_timeout", busyTimeoutMilliseconds)
	assertPragmaInt(t, db, "user_version", currentSchemaVersion)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO sessions (id, name, state, revision, monitoring_level, created_utc, updated_utc)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "schema-session", "schema", "draft", 0, "standard", "2026-09-15T00:00:00Z", "2026-09-15T00:00:00Z"); err != nil {
		t.Fatalf("insert session with default policy: %v", err)
	}
	var fileActivityEnabled int
	if err := db.QueryRowContext(context.Background(), `
		SELECT json_extract(monitoring_policy, '$.fileActivityEnabled') FROM sessions WHERE id = ?
	`, "schema-session").Scan(&fileActivityEnabled); err != nil || fileActivityEnabled != 1 {
		t.Fatalf("default monitoring policy fileActivityEnabled = %d, error = %v", fileActivityEnabled, err)
	}

	for _, table := range []string{"sessions", "audit_events"} {
		var count int
		err = db.QueryRowContext(
			context.Background(),
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query table %q: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %q count = %d, want 1", table, count)
		}
	}

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO audit_events (
			event_id, session_id, sequence, category, action, severity,
			observed_utc, monotonic_ticks, source, confidence,
			payload_nonce, payload_ciphertext, previous_hash, event_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "event-1", "missing-session", 1, "file", "created", "low",
		"2026-08-23T00:00:00Z", 1, "test", "direct",
		[]byte{1}, []byte{2}, make([]byte, 32), make([]byte, 32))
	if err == nil {
		t.Fatal("insert without a parent session succeeded, want foreign-key failure")
	}
}

func TestOpenAppliesMigrationsIdempotently(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "guard.db")
	for attempt := 0; attempt < 2; attempt++ {
		db, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open() attempt %d error = %v", attempt+1, err)
		}
		assertPragmaInt(t, db, "user_version", currentSchemaVersion)
		if err := db.Close(); err != nil {
			t.Fatalf("Close() attempt %d error = %v", attempt+1, err)
		}
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	db, err := Open(context.Background(), "   ")
	if db != nil {
		_ = db.Close()
	}
	if !errors.Is(err, ErrDatabasePathRequired) {
		t.Fatalf("Open() error = %v, want %v", err, ErrDatabasePathRequired)
	}
}

func assertPragmaInt(t *testing.T, db *sql.DB, name string, want int) {
	t.Helper()

	var got int
	if err := db.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
	}
}

func assertPragmaText(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()

	var got string
	if err := db.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}
