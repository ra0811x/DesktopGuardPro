package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/storage"
)

func TestInputShieldCredentialManagerPasswordAndOneTimeRecovery(t *testing.T) {
	store := &memoryInputShieldCredentialStore{}
	now := time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC)
	manager := testInputShieldCredentialManager(store, &now)
	password := []byte("correct-password")
	recoveryCode, err := manager.SetPassword(context.Background(), password, true)
	if err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}
	if strings.Count(recoveryCode, "-") != 4 || len(recoveryCode) != 24 {
		t.Fatalf("recovery code = %q", recoveryCode)
	}
	for _, value := range password {
		if value != 0 {
			t.Fatal("password input was not cleared")
		}
	}
	if err := manager.Verify(context.Background(), "owner", []byte("correct-password"), false, 5, time.Minute); err != nil {
		t.Fatalf("password Verify() error = %v", err)
	}
	formattedRecovery := []byte(strings.ToLower(strings.ReplaceAll(recoveryCode, "-", " ")))
	if err := manager.Verify(context.Background(), "owner", formattedRecovery, true, 5, time.Minute); err != nil {
		t.Fatalf("recovery Verify() error = %v", err)
	}
	if store.record.RecoveryCode != nil {
		t.Fatal("successful recovery code was not invalidated")
	}
	if err := manager.Verify(context.Background(), "owner", []byte(recoveryCode), true, 5, time.Minute); !errors.Is(err, ErrInputShieldCredentialInvalid) {
		t.Fatalf("reused recovery code error = %v", err)
	}
}

func TestInputShieldCredentialManagerLocksAndRecoversAfterTimeout(t *testing.T) {
	store := &memoryInputShieldCredentialStore{}
	now := time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC)
	manager := testInputShieldCredentialManager(store, &now)
	if _, err := manager.SetPassword(context.Background(), []byte("correct-password"), false); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		err := manager.Verify(context.Background(), "owner", []byte("wrong-password"), false, 3, time.Minute)
		if attempt < 3 && !errors.Is(err, ErrInputShieldCredentialInvalid) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if attempt == 3 && !errors.Is(err, ErrInputShieldCredentialLocked) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
	}
	if err := manager.Verify(context.Background(), "owner", []byte("correct-password"), false, 3, time.Minute); !errors.Is(err, ErrInputShieldCredentialLocked) {
		t.Fatalf("locked Verify() error = %v", err)
	}
	now = now.Add(time.Minute)
	if err := manager.Verify(context.Background(), "owner", []byte("correct-password"), false, 3, time.Minute); err != nil {
		t.Fatalf("Verify() after lockout error = %v", err)
	}
}

func TestInputShieldCredentialManagerRejectsWeakPassword(t *testing.T) {
	store := &memoryInputShieldCredentialStore{}
	now := time.Now().UTC()
	manager := testInputShieldCredentialManager(store, &now)
	if _, err := manager.SetPassword(context.Background(), []byte("short"), false); !errors.Is(err, ErrInputShieldPasswordInvalid) {
		t.Fatalf("SetPassword() error = %v", err)
	}
	if store.found {
		t.Fatal("weak password was stored")
	}
}

func testInputShieldCredentialManager(store inputShieldCredentialStore, now *time.Time) *InputShieldCredentialManager {
	manager := NewInputShieldCredentialManager(store)
	manager.now = func() time.Time { return *now }
	manager.random = strings.NewReader(strings.Repeat("A", 256))
	manager.iterations = 100_000
	return manager
}

type memoryInputShieldCredentialStore struct {
	record storage.InputShieldCredentialRecord
	found  bool
}

func (store *memoryInputShieldCredentialStore) SaveInputShieldCredentials(_ context.Context, record storage.InputShieldCredentialRecord) error {
	store.record = record
	store.found = true
	return nil
}

func (store *memoryInputShieldCredentialStore) LoadInputShieldCredentials(context.Context) (storage.InputShieldCredentialRecord, bool, error) {
	return store.record, store.found, nil
}

func (store *memoryInputShieldCredentialStore) DeleteInputShieldCredentials(context.Context) error {
	store.record = storage.InputShieldCredentialRecord{}
	store.found = false
	return nil
}
