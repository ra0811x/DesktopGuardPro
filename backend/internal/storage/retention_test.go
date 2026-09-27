package storage

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestRepositoryPrunesOnlyUnlockedTerminalSessions(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x47}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	old := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"expired", "locked"} {
		session, err := domain.NewSession(id, id+" session")
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateSession(ctx, *session, old); err != nil {
			t.Fatal(err)
		}
		for _, state := range []domain.SessionState{
			domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStateFinalizing, domain.SessionStateCompleted,
		} {
			if err := session.Transition(state); err != nil {
				t.Fatal(err)
			}
			if err := repository.UpdateSession(ctx, *session, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repository.SetSessionRetentionLock(ctx, "locked", true); err != nil {
		t.Fatalf("SetSessionRetentionLock() error = %v", err)
	}

	deleted, err := repository.PruneTerminalSessions(ctx, old.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("PruneTerminalSessions() error = %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "expired" {
		t.Fatalf("deleted sessions = %q, want only expired", deleted)
	}
	if _, err := repository.GetSession(ctx, "expired"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired session lookup error = %v, want %v", err, ErrSessionNotFound)
	}
	if session, err := repository.GetSession(ctx, "locked"); err != nil || session.State != domain.SessionStateCompleted {
		t.Fatalf("locked session = %+v, error = %v", session, err)
	}
}
