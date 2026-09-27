package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/maintenancegate"
)

func TestCreateSessionCannotRaceMaintenanceCheck(t *testing.T) {
	directory := t.TempDir()
	db, repository := openTestRepository(t, filepath.Join(directory, "guard.db"), bytes.Repeat([]byte{7}, 32))
	defer db.Close()
	lock, err := maintenancegate.Acquire(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, found, err := repository.FindRecoverableSession(context.Background()); err != nil || found {
		t.Fatalf("idle preflight: %v, %v", found, err)
	}
	session, _ := domain.NewSession("racing-session", "During maintenance")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := repository.CreateSession(ctx, *session, time.Now().UTC()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("session escaped maintenance freeze: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, maintenancegate.JournalFileName), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if err := repository.CreateSession(context.Background(), *session, time.Now().UTC()); !errors.Is(err, maintenancegate.ErrMaintenanceInProgress) {
		t.Fatalf("journal ignored: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, maintenancegate.JournalFileName)); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
