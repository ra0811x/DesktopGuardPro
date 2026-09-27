package maintenancegate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceLockSerializesAndHonorsCancellation(t *testing.T) {
	directory := t.TempDir()
	lock, err := Acquire(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, directory); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("competing lock = %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
}

func TestPersistedTransactionBlocksSessionsAfterLockReleased(t *testing.T) {
	directory := t.TempDir()
	lock, err := Acquire(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.CheckReady(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, JournalFileName), []byte("interrupted transaction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err = Acquire(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lock.CheckReady(); !errors.Is(err, ErrMaintenanceInProgress) {
		t.Fatalf("interrupted maintenance was ignored: %v", err)
	}
}
