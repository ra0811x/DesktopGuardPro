package storage

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestMaintenanceReadsPersistedStateWithoutRunningService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offline.db")
	db, repository := openTestRepository(t, path, bytes.Repeat([]byte{0x75}, payloadKeySize))
	session := createTestSession(t, repository)
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		if err := session.Transition(state); err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateSession(context.Background(), *session, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	stored, active, err := ReadPersistedProtectionSession(context.Background(), path)
	if err != nil || !active || stored.State != domain.SessionStateActive {
		t.Fatalf("offline active session: %+v, %v, %v", stored, active, err)
	}
	db, repository = openTestRepository(t, path, bytes.Repeat([]byte{0x75}, payloadKeySize))
	for _, state := range []domain.SessionState{domain.SessionStateFinalizing, domain.SessionStateCompleted} {
		if err := session.Transition(state); err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateSession(context.Background(), *session, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	_, active, err = ReadPersistedProtectionSession(context.Background(), path)
	if err != nil || active {
		t.Fatalf("offline completed session: %v, %v", active, err)
	}
	if _, _, err := ReadPersistedProtectionSession(context.Background(), filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("missing database was treated as an inactive session")
	}
}

func TestMaintenanceReadsLegacySessionSchemaBeforeUpgradeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", databaseSourceName(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			state TEXT NOT NULL,
			revision INTEGER NOT NULL,
			created_utc TEXT NOT NULL,
			updated_utc TEXT NOT NULL
		);
		INSERT INTO sessions (id, name, state, revision, created_utc, updated_utc)
		VALUES ('legacy-active', 'Legacy active protection', 'active', 3, '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	session, active, err := ReadPersistedProtectionSession(context.Background(), path)
	if err != nil || !active || session.ID != "legacy-active" || session.MonitoringLevel != domain.MonitoringLevelStandard {
		t.Fatalf("legacy session = %+v, active=%t, error=%v", session, active, err)
	}
}
