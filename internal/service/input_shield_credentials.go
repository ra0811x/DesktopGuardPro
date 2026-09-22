package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"desktopguardpro/internal/storage"
)

const (
	inputShieldPasswordIterations = 600_000
	inputShieldSaltSize           = 16
	inputShieldDigestSize         = 32
	inputShieldRecoveryLength     = 20
)

const inputShieldRecoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var (
	ErrInputShieldCredentialNotConfigured = errors.New("input shield local credential is not configured")
	ErrInputShieldCredentialInvalid       = errors.New("input shield credential is invalid")
	ErrInputShieldCredentialLocked        = errors.New("input shield credential verification is locked")
	ErrInputShieldPasswordInvalid         = errors.New("input shield password must contain 8 to 128 UTF-8 characters")
)

type InputShieldCredentialStatus struct {
	Configured          bool `json:"configured"`
	RecoveryCodeEnabled bool `json:"recoveryCodeEnabled"`
}

type inputShieldCredentialStore interface {
	SaveInputShieldCredentials(context.Context, storage.InputShieldCredentialRecord) error
	LoadInputShieldCredentials(context.Context) (storage.InputShieldCredentialRecord, bool, error)
	DeleteInputShieldCredentials(context.Context) error
}

type inputShieldCredentialFailure struct {
	count       int
	lockedUntil time.Time
}

type InputShieldCredentialManager struct {
	store      inputShieldCredentialStore
	now        func() time.Time
	random     io.Reader
	iterations int

	failureMu sync.Mutex
	failures  map[string]inputShieldCredentialFailure
}

func NewInputShieldCredentialManager(store inputShieldCredentialStore) *InputShieldCredentialManager {
	return &InputShieldCredentialManager{
		store: store, now: time.Now, random: rand.Reader, iterations: inputShieldPasswordIterations,
		failures: make(map[string]inputShieldCredentialFailure),
	}
}

func (manager *InputShieldCredentialManager) Status(ctx context.Context) (InputShieldCredentialStatus, error) {
	if manager == nil || manager.store == nil {
		return InputShieldCredentialStatus{}, ErrInputShieldCredentialNotConfigured
	}
	record, found, err := manager.store.LoadInputShieldCredentials(ctx)
	if err != nil {
		return InputShieldCredentialStatus{}, err
	}
	return InputShieldCredentialStatus{Configured: found, RecoveryCodeEnabled: found && record.RecoveryCode != nil}, nil
}

func (manager *InputShieldCredentialManager) SetPassword(
	ctx context.Context,
	password []byte,
	enableRecovery bool,
) (string, error) {
	defer clear(password)
	if manager == nil || manager.store == nil || manager.random == nil || ctx == nil || ctx.Err() != nil {
		return "", ErrInputShieldCredentialNotConfigured
	}
	if !validInputShieldPassword(password) {
		return "", ErrInputShieldPasswordInvalid
	}
	passwordVerifier, err := manager.createVerifier(password)
	if err != nil {
		return "", err
	}
	record := storage.InputShieldCredentialRecord{
		Version: storage.InputShieldCredentialVersion, Password: passwordVerifier, UpdatedUTC: manager.now().UTC(),
	}
	recoveryCode := ""
	if enableRecovery {
		recoveryCode, err = manager.generateRecoveryCode()
		if err != nil {
			return "", err
		}
		recoverySecret := []byte(normalizeInputShieldRecoveryCode(recoveryCode))
		verifier, verifierErr := manager.createVerifier(recoverySecret)
		clear(recoverySecret)
		if verifierErr != nil {
			return "", verifierErr
		}
		record.RecoveryCode = &verifier
	}
	if err := manager.store.SaveInputShieldCredentials(ctx, record); err != nil {
		return "", err
	}
	manager.failureMu.Lock()
	manager.failures = make(map[string]inputShieldCredentialFailure)
	manager.failureMu.Unlock()
	return recoveryCode, nil
}

func (manager *InputShieldCredentialManager) Delete(ctx context.Context) error {
	if manager == nil || manager.store == nil {
		return ErrInputShieldCredentialNotConfigured
	}
	if err := manager.store.DeleteInputShieldCredentials(ctx); err != nil {
		return err
	}
	manager.failureMu.Lock()
	manager.failures = make(map[string]inputShieldCredentialFailure)
	manager.failureMu.Unlock()
	return nil
}

func (manager *InputShieldCredentialManager) Verify(
	ctx context.Context,
	principal string,
	secret []byte,
	recovery bool,
	maximumFailures int,
	lockout time.Duration,
) error {
	return manager.verify(ctx, principal, secret, recovery, false, maximumFailures, lockout)
}

// VerifyPasswordOrRecovery follows Computer Security's single-field unlock flow
// while retaining the existing encrypted password store and one-time recovery.
func (manager *InputShieldCredentialManager) VerifyPasswordOrRecovery(
	ctx context.Context, principal string, secret []byte, allowRecovery bool,
	maximumFailures int, lockout time.Duration,
) error {
	return manager.verify(ctx, principal, secret, false, allowRecovery, maximumFailures, lockout)
}

func (manager *InputShieldCredentialManager) verify(
	ctx context.Context, principal string, secret []byte, recovery, acceptRecovery bool,
	maximumFailures int, lockout time.Duration,
) error {
	defer clear(secret)
	if manager == nil || manager.store == nil || ctx == nil || ctx.Err() != nil || strings.TrimSpace(principal) == "" {
		return ErrInputShieldCredentialInvalid
	}
	if maximumFailures < 1 || maximumFailures > 10 || lockout < 10*time.Second || lockout > time.Hour {
		return ErrInputShieldCredentialInvalid
	}
	now := manager.now().UTC()
	if manager.verificationLocked(principal, now) {
		return ErrInputShieldCredentialLocked
	}
	record, found, err := manager.store.LoadInputShieldCredentials(ctx)
	if err != nil {
		return err
	}
	if !found {
		return ErrInputShieldCredentialNotConfigured
	}
	verifier := record.Password
	if recovery {
		if record.RecoveryCode == nil {
			manager.recordFailure(principal, maximumFailures, lockout, now)
			return ErrInputShieldCredentialInvalid
		}
		verifier = *record.RecoveryCode
		secret = []byte(normalizeInputShieldRecoveryCode(string(secret)))
		defer clear(secret)
	}
	matched := verifyInputShieldSecret(secret, verifier)
	if !matched && !recovery && acceptRecovery && record.RecoveryCode != nil {
		recoverySecret := []byte(normalizeInputShieldRecoveryCode(string(secret)))
		matched = verifyInputShieldSecret(recoverySecret, *record.RecoveryCode)
		clear(recoverySecret)
		recovery = matched
	}
	if !matched {
		if manager.recordFailure(principal, maximumFailures, lockout, now) {
			return ErrInputShieldCredentialLocked
		}
		return ErrInputShieldCredentialInvalid
	}
	manager.clearFailure(principal)
	if recovery {
		record.RecoveryCode = nil
		record.UpdatedUTC = now
		if err := manager.store.SaveInputShieldCredentials(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (manager *InputShieldCredentialManager) createVerifier(secret []byte) (storage.InputShieldSecretVerifier, error) {
	salt := make([]byte, inputShieldSaltSize)
	if _, err := io.ReadFull(manager.random, salt); err != nil {
		return storage.InputShieldSecretVerifier{}, fmt.Errorf("generate input shield credential salt: %w", err)
	}
	return storage.InputShieldSecretVerifier{
		Algorithm: storage.InputShieldPBKDF2SHA256Algorithm, Iterations: manager.iterations,
		Salt: salt, Digest: deriveInputShieldSecret(secret, salt, manager.iterations),
	}, nil
}

func (manager *InputShieldCredentialManager) generateRecoveryCode() (string, error) {
	randomBytes := make([]byte, inputShieldRecoveryLength)
	if _, err := io.ReadFull(manager.random, randomBytes); err != nil {
		return "", fmt.Errorf("generate input shield recovery code: %w", err)
	}
	var builder strings.Builder
	for index, value := range randomBytes {
		if index > 0 && index%4 == 0 {
			builder.WriteByte('-')
		}
		builder.WriteByte(inputShieldRecoveryAlphabet[int(value)&31])
	}
	clear(randomBytes)
	return builder.String(), nil
}

func (manager *InputShieldCredentialManager) verificationLocked(principal string, now time.Time) bool {
	manager.failureMu.Lock()
	defer manager.failureMu.Unlock()
	failure := manager.failures[principal]
	if failure.lockedUntil.IsZero() || !now.Before(failure.lockedUntil) {
		if !failure.lockedUntil.IsZero() {
			delete(manager.failures, principal)
		}
		return false
	}
	return true
}

func (manager *InputShieldCredentialManager) recordFailure(
	principal string,
	maximumFailures int,
	lockout time.Duration,
	now time.Time,
) bool {
	manager.failureMu.Lock()
	defer manager.failureMu.Unlock()
	failure := manager.failures[principal]
	failure.count++
	locked := failure.count >= maximumFailures
	if locked {
		failure.lockedUntil = now.Add(lockout)
	}
	manager.failures[principal] = failure
	return locked
}

func (manager *InputShieldCredentialManager) clearFailure(principal string) {
	manager.failureMu.Lock()
	delete(manager.failures, principal)
	manager.failureMu.Unlock()
}

func validInputShieldPassword(password []byte) bool {
	if !utf8.Valid(password) {
		return false
	}
	length := utf8.RuneCount(password)
	return length >= 8 && length <= 128
}

func normalizeInputShieldRecoveryCode(value string) string {
	value = strings.ToUpper(value)
	return strings.Map(func(character rune) rune {
		if character == '-' || character == ' ' || character == '\t' || character == '\r' || character == '\n' {
			return -1
		}
		return character
	}, value)
}

func verifyInputShieldSecret(secret []byte, verifier storage.InputShieldSecretVerifier) bool {
	if !validInputShieldVerifier(verifier) {
		return false
	}
	digest := deriveInputShieldSecret(secret, verifier.Salt, verifier.Iterations)
	defer clear(digest)
	return subtle.ConstantTimeCompare(digest, verifier.Digest) == 1
}

func validInputShieldVerifier(verifier storage.InputShieldSecretVerifier) bool {
	return verifier.Algorithm == storage.InputShieldPBKDF2SHA256Algorithm &&
		verifier.Iterations >= 100_000 && verifier.Iterations <= 2_000_000 &&
		len(verifier.Salt) >= 16 && len(verifier.Salt) <= 64 && len(verifier.Digest) == inputShieldDigestSize
}

func deriveInputShieldSecret(secret, salt []byte, iterations int) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(salt)
	_, _ = mac.Write([]byte{0, 0, 0, 1})
	current := mac.Sum(nil)
	result := append([]byte(nil), current...)
	for iteration := 1; iteration < iterations; iteration++ {
		mac.Reset()
		_, _ = mac.Write(current)
		next := mac.Sum(nil)
		for index := range result {
			result[index] ^= next[index]
		}
		clear(current)
		current = next
	}
	clear(current)
	return result
}
