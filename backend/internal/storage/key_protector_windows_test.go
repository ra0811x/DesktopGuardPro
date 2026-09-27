package storage

import (
	"bytes"
	"errors"
	"testing"
)

func TestDPAPIKeyProtectorRoundTrip(t *testing.T) {
	t.Parallel()

	protector := NewDPAPIKeyProtector()
	key := bytes.Repeat([]byte{0x5a}, payloadKeySize)
	protected, err := protector.Protect(key)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	if bytes.Equal(protected, key) {
		t.Fatal("Protect() returned plaintext key material")
	}

	got, err := protector.Unprotect(protected)
	if err != nil {
		t.Fatalf("Unprotect() error = %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("Unprotect() returned a different key")
	}
}

func TestDPAPIKeyProtectorUsesRandomizedProtection(t *testing.T) {
	t.Parallel()

	protector := NewDPAPIKeyProtector()
	key := bytes.Repeat([]byte{0x3c}, payloadKeySize)
	first, err := protector.Protect(key)
	if err != nil {
		t.Fatalf("Protect() first error = %v", err)
	}
	second, err := protector.Protect(key)
	if err != nil {
		t.Fatalf("Protect() second error = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("Protect() returned identical protected blobs")
	}
}

func TestDPAPIKeyProtectorRejectsTampering(t *testing.T) {
	t.Parallel()

	protector := NewDPAPIKeyProtector()
	protected, err := protector.Protect(bytes.Repeat([]byte{0x19}, payloadKeySize))
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	protected[len(protected)-1] ^= 0xff

	if _, err := protector.Unprotect(protected); !errors.Is(err, ErrKeyUnprotection) {
		t.Fatalf("Unprotect() error = %v, want %v", err, ErrKeyUnprotection)
	}
}

func TestDPAPIKeyProtectorValidatesInput(t *testing.T) {
	t.Parallel()

	protector := NewDPAPIKeyProtector()
	if _, err := protector.Protect(make([]byte, payloadKeySize-1)); !errors.Is(err, ErrInvalidPayloadKey) {
		t.Fatalf("Protect() error = %v, want %v", err, ErrInvalidPayloadKey)
	}
	if _, err := protector.Unprotect(nil); !errors.Is(err, ErrInvalidProtectedKey) {
		t.Fatalf("Unprotect() error = %v, want %v", err, ErrInvalidProtectedKey)
	}
}
