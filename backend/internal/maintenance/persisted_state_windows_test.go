package maintenance

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/storage"
	platformwindows "desktopguardpro/internal/windows"
)

func TestPersistedMaintenanceGateWorksWithNoServiceAndRejectsActiveSession(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "service-data")
	if err := platformwindows.SecureServiceDataDirectory(directory, "EventLog"); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(context.Background(), filepath.Join(directory, "desktop-guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x76}, 32)
	cipher, _ := storage.NewPayloadCipher(key)
	integrity, _ := storage.NewEventIntegrity(key)
	repository, err := storage.NewRepository(db, cipher, integrity)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPersistedProtectionState(directory); err != nil {
		t.Fatalf("empty offline database rejected: %v", err)
	}
	session, _ := domain.NewSession("offline", "Offline protection")
	if err := repository.CreateSession(context.Background(), *session, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := checkPersistedProtectionState(directory); !errors.Is(err, ErrActiveProtectionSession) {
		t.Fatalf("unfinished offline session accepted: %v", err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateFailed} {
		if err := session.Transition(state); err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateSession(context.Background(), *session, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := checkPersistedProtectionState(directory); err != nil {
		t.Fatalf("completed offline maintenance gate failed: %v", err)
	}
}

func TestPersistedMaintenanceGateCompletesInterruptedFinalizingSession(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "service-data")
	if err := platformwindows.SecureServiceDataDirectory(directory, "EventLog"); err != nil {
		t.Fatal(err)
	}
	keyStore, err := storage.NewDataKeyStore(storage.NewDPAPIKeyProtector())
	if err != nil {
		t.Fatal(err)
	}
	key, err := keyStore.LoadOrCreate(filepath.Join(directory, "storage.key"))
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(directory, "desktop-guard.db")
	db, err := storage.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := storage.NewPayloadCipher(key)
	integrity, _ := storage.NewEventIntegrity(key)
	repository, err := storage.NewRepository(db, cipher, integrity)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := domain.NewSession("finalizing", "Interrupted finalization")
	if err := repository.CreateSession(context.Background(), *session, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{
		domain.SessionStatePreparing,
		domain.SessionStateActive,
		domain.SessionStateFinalizing,
	} {
		if err := session.Transition(state); err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateSession(context.Background(), *session, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := checkPersistedProtectionState(directory); err != nil {
		t.Fatalf("interrupted finalizing session was not completed: %v", err)
	}

	db, err = storage.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cipher, _ = storage.NewPayloadCipher(key)
	integrity, _ = storage.NewEventIntegrity(key)
	repository, err = storage.NewRepository(db, cipher, integrity)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repository.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != domain.SessionStateCompleted || completed.Revision != session.Revision+1 {
		t.Fatalf("completed session = %+v", completed)
	}
	records, err := repository.ListEvents(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Event.Action != "protection_ended" || records[0].Event.Source != "desktop_guard_maintenance" {
		t.Fatalf("recovery events = %+v", records)
	}
}
