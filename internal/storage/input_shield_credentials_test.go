package storage

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInputShieldCredentialsPersistEncryptedAndRejectTampering(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x73}, 32))
	defer db.Close()
	recovery := testInputShieldVerifier(0x32)
	record := InputShieldCredentialRecord{
		Version: InputShieldCredentialVersion, Password: testInputShieldVerifier(0x31), RecoveryCode: &recovery,
		UpdatedUTC: time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC),
	}
	if err := repository.SaveInputShieldCredentials(ctx, record); err != nil {
		t.Fatalf("SaveInputShieldCredentials() error = %v", err)
	}
	var stored []byte
	if err := db.QueryRowContext(ctx, "SELECT ciphertext FROM input_shield_credentials WHERE id = 1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, record.Password.Digest) || bytes.Contains(stored, record.Password.Salt) {
		t.Fatal("input shield verifier was stored without payload encryption")
	}
	loaded, found, err := repository.LoadInputShieldCredentials(ctx)
	if err != nil || !found || !reflect.DeepEqual(loaded, record) {
		t.Fatalf("LoadInputShieldCredentials() = %+v, %v, %v", loaded, found, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE input_shield_credentials SET ciphertext = zeroblob(length(ciphertext)) WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.LoadInputShieldCredentials(ctx); !errors.Is(err, ErrPayloadAuthentication) {
		t.Fatalf("tampered credentials error = %v", err)
	}
}

func TestInputShieldCredentialsDeleteAndValidation(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x74}, 32))
	defer db.Close()
	if _, found, err := repository.LoadInputShieldCredentials(ctx); err != nil || found {
		t.Fatalf("initial credentials found = %v, error = %v", found, err)
	}
	record := InputShieldCredentialRecord{
		Version: InputShieldCredentialVersion, Password: testInputShieldVerifier(0x41), UpdatedUTC: time.Now().UTC(),
	}
	if err := repository.SaveInputShieldCredentials(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteInputShieldCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.LoadInputShieldCredentials(ctx); err != nil || found {
		t.Fatalf("credentials after delete found = %v, error = %v", found, err)
	}
	record.Password.Iterations = 10
	if err := repository.SaveInputShieldCredentials(ctx, record); !errors.Is(err, ErrInputShieldCredentialRecordInvalid) {
		t.Fatalf("invalid record error = %v", err)
	}
}

func testInputShieldVerifier(fill byte) InputShieldSecretVerifier {
	return InputShieldSecretVerifier{
		Algorithm: InputShieldPBKDF2SHA256Algorithm, Iterations: 600_000,
		Salt: bytes.Repeat([]byte{fill}, 16), Digest: bytes.Repeat([]byte{fill + 1}, 32),
	}
}
