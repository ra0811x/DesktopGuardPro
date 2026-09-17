package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	inputShieldCredentialsAAD        = "desktop-guard-pro:input-shield-credentials:v1"
	InputShieldCredentialVersion     = 1
	InputShieldPBKDF2SHA256Algorithm = "pbkdf2-sha256"
)

var ErrInputShieldCredentialRecordInvalid = errors.New("input shield credential record is invalid")

type InputShieldSecretVerifier struct {
	Algorithm  string `json:"algorithm"`
	Iterations int    `json:"iterations"`
	Salt       []byte `json:"salt"`
	Digest     []byte `json:"digest"`
}

type InputShieldCredentialRecord struct {
	Version      int                        `json:"version"`
	Password     InputShieldSecretVerifier  `json:"password"`
	RecoveryCode *InputShieldSecretVerifier `json:"recoveryCode,omitempty"`
	UpdatedUTC   time.Time                  `json:"updatedUtc"`
}

func (record InputShieldCredentialRecord) Validate() error {
	if record.Version != InputShieldCredentialVersion || record.UpdatedUTC.IsZero() ||
		!validInputShieldVerifier(record.Password) {
		return ErrInputShieldCredentialRecordInvalid
	}
	if record.RecoveryCode != nil && !validInputShieldVerifier(*record.RecoveryCode) {
		return ErrInputShieldCredentialRecordInvalid
	}
	return nil
}

func validInputShieldVerifier(verifier InputShieldSecretVerifier) bool {
	return verifier.Algorithm == InputShieldPBKDF2SHA256Algorithm &&
		verifier.Iterations >= 100_000 && verifier.Iterations <= 2_000_000 &&
		len(verifier.Salt) >= 16 && len(verifier.Salt) <= 64 && len(verifier.Digest) == 32
}

func (repository *Repository) SaveInputShieldCredentials(ctx context.Context, record InputShieldCredentialRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	plaintext, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode input shield credentials: %w", err)
	}
	defer clear(plaintext)
	nonce, ciphertext, err := repository.cipher.Encrypt(plaintext, []byte(inputShieldCredentialsAAD))
	if err != nil {
		return fmt.Errorf("encrypt input shield credentials: %w", err)
	}
	if _, err := repository.db.ExecContext(ctx, `
		INSERT INTO input_shield_credentials (id, nonce, ciphertext) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET nonce = excluded.nonce, ciphertext = excluded.ciphertext
	`, nonce, ciphertext); err != nil {
		return fmt.Errorf("save input shield credentials: %w", err)
	}
	return nil
}

func (repository *Repository) LoadInputShieldCredentials(ctx context.Context) (InputShieldCredentialRecord, bool, error) {
	var nonce, ciphertext []byte
	err := repository.db.QueryRowContext(ctx,
		"SELECT nonce, ciphertext FROM input_shield_credentials WHERE id = 1").Scan(&nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return InputShieldCredentialRecord{}, false, nil
	}
	if err != nil {
		return InputShieldCredentialRecord{}, false, fmt.Errorf("load input shield credentials: %w", err)
	}
	plaintext, err := repository.cipher.Decrypt(nonce, ciphertext, []byte(inputShieldCredentialsAAD))
	if err != nil {
		return InputShieldCredentialRecord{}, false, fmt.Errorf("decrypt input shield credentials: %w", err)
	}
	defer clear(plaintext)
	var record InputShieldCredentialRecord
	if err := json.Unmarshal(plaintext, &record); err != nil {
		return InputShieldCredentialRecord{}, false, fmt.Errorf("decode input shield credentials: %w", err)
	}
	if err := record.Validate(); err != nil {
		return InputShieldCredentialRecord{}, false, err
	}
	return record, true, nil
}

func (repository *Repository) DeleteInputShieldCredentials(ctx context.Context) error {
	if _, err := repository.db.ExecContext(ctx, "DELETE FROM input_shield_credentials WHERE id = 1"); err != nil {
		return fmt.Errorf("delete input shield credentials: %w", err)
	}
	return nil
}
