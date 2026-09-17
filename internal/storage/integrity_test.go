package storage

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestEventIntegrityComputesAndVerifiesHash(t *testing.T) {
	t.Parallel()

	integrity, err := NewEventIntegrity(bytes.Repeat([]byte{0x61}, payloadKeySize))
	if err != nil {
		t.Fatalf("NewEventIntegrity() error = %v", err)
	}
	event := testAuditEvent(1)
	event.EncryptedPayload = []byte("ciphertext")
	event.PreviousHash = make([]byte, eventHashSize)
	nonce := []byte("123456789012")

	hash, err := integrity.Compute(event, nonce)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if len(hash) != eventHashSize {
		t.Fatalf("Compute() hash length = %d, want %d", len(hash), eventHashSize)
	}
	event.EventHash = hash
	if err := integrity.Verify(event, nonce); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	again, err := integrity.Compute(event, nonce)
	if err != nil {
		t.Fatalf("Compute() second error = %v", err)
	}
	if !bytes.Equal(again, hash) {
		t.Fatal("Compute() is not deterministic")
	}
}

func TestEventIntegrityRejectsEventAndCiphertextTampering(t *testing.T) {
	t.Parallel()

	integrity, err := NewEventIntegrity(bytes.Repeat([]byte{0x72}, payloadKeySize))
	if err != nil {
		t.Fatalf("NewEventIntegrity() error = %v", err)
	}
	event := testAuditEvent(1)
	event.EncryptedPayload = []byte("ciphertext")
	event.PreviousHash = make([]byte, eventHashSize)
	nonce := []byte("123456789012")
	event.EventHash, err = integrity.Compute(event, nonce)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}

	tamperedField := event
	tamperedField.Action = "deleted"
	if err := integrity.Verify(tamperedField, nonce); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("Verify() field error = %v, want %v", err, ErrEventIntegrity)
	}

	tamperedPayload := event
	tamperedPayload.EncryptedPayload = append([]byte(nil), event.EncryptedPayload...)
	tamperedPayload.EncryptedPayload[0] ^= 0xff
	if err := integrity.Verify(tamperedPayload, nonce); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("Verify() payload error = %v, want %v", err, ErrEventIntegrity)
	}
}

func TestEventIntegrityChainsEvents(t *testing.T) {
	t.Parallel()

	integrity, err := NewEventIntegrity(bytes.Repeat([]byte{0x83}, payloadKeySize))
	if err != nil {
		t.Fatalf("NewEventIntegrity() error = %v", err)
	}
	nonce := []byte("123456789012")
	first := testAuditEvent(1)
	first.EncryptedPayload = []byte("first")
	first.PreviousHash = make([]byte, eventHashSize)
	first.EventHash, err = integrity.Compute(first, nonce)
	if err != nil {
		t.Fatalf("Compute() first error = %v", err)
	}

	second := testAuditEvent(2)
	second.EventID = "event-2"
	second.EncryptedPayload = []byte("second")
	second.PreviousHash = append([]byte(nil), first.EventHash...)
	second.EventHash, err = integrity.Compute(second, nonce)
	if err != nil {
		t.Fatalf("Compute() second error = %v", err)
	}
	if err := integrity.Verify(second, nonce); err != nil {
		t.Fatalf("Verify() second error = %v", err)
	}

	second.PreviousHash[0] ^= 0xff
	if err := integrity.Verify(second, nonce); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("Verify() chain error = %v, want %v", err, ErrEventIntegrity)
	}
}

func TestEventIntegrityValidatesKeyAndStoredHash(t *testing.T) {
	t.Parallel()

	if _, err := NewEventIntegrity(make([]byte, payloadKeySize-1)); !errors.Is(err, ErrInvalidPayloadKey) {
		t.Fatalf("NewEventIntegrity() error = %v, want %v", err, ErrInvalidPayloadKey)
	}
	integrity, err := NewEventIntegrity(make([]byte, payloadKeySize))
	if err != nil {
		t.Fatalf("NewEventIntegrity() error = %v", err)
	}
	event := testAuditEvent(1)
	event.EncryptedPayload = []byte("ciphertext")
	if err := integrity.Verify(event, []byte("123456789012")); !errors.Is(err, ErrInvalidEventHash) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrInvalidEventHash)
	}
}

func testAuditEvent(sequence uint64) domain.AuditEvent {
	return domain.AuditEvent{
		EventID:        "event-1",
		SessionID:      "session-1",
		Sequence:       sequence,
		Category:       domain.EventCategoryFile,
		Action:         "created",
		Severity:       domain.EventSeverityLow,
		ObservedUTC:    time.Date(2026, 8, 23, 1, 2, 3, 4, time.UTC),
		MonotonicTicks: 12345,
		ProcessKey:     "process-1",
		ObjectKey:      "object-1",
		Source:         "test",
		Confidence:     domain.EventConfidenceDirect,
	}
}
