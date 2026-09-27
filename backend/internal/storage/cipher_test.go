package storage

import (
	"bytes"
	"errors"
	"testing"
)

func TestPayloadCipherRoundTrip(t *testing.T) {
	t.Parallel()

	cipher, err := NewPayloadCipher(bytes.Repeat([]byte{0x42}, payloadKeySize))
	if err != nil {
		t.Fatalf("NewPayloadCipher() error = %v", err)
	}

	plaintext := []byte(`{"path":"C:\\evidence.txt"}`)
	additionalData := []byte("session-1:event-1:1")
	nonce, ciphertext, err := cipher.Encrypt(plaintext, additionalData)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	got, err := cipher.Decrypt(nonce, ciphertext, additionalData)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestPayloadCipherUsesUniqueNonce(t *testing.T) {
	t.Parallel()

	cipher, err := NewPayloadCipher(bytes.Repeat([]byte{0x24}, payloadKeySize))
	if err != nil {
		t.Fatalf("NewPayloadCipher() error = %v", err)
	}

	nonceA, _, err := cipher.Encrypt([]byte("same payload"), nil)
	if err != nil {
		t.Fatalf("Encrypt() first error = %v", err)
	}
	nonceB, _, err := cipher.Encrypt([]byte("same payload"), nil)
	if err != nil {
		t.Fatalf("Encrypt() second error = %v", err)
	}
	if bytes.Equal(nonceA, nonceB) {
		t.Fatal("Encrypt() reused a nonce")
	}
}

func TestPayloadCipherRejectsTampering(t *testing.T) {
	t.Parallel()

	cipher, err := NewPayloadCipher(bytes.Repeat([]byte{0x11}, payloadKeySize))
	if err != nil {
		t.Fatalf("NewPayloadCipher() error = %v", err)
	}

	nonce, ciphertext, err := cipher.Encrypt([]byte("sensitive"), []byte("event-1"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	tamperedCiphertext := append([]byte(nil), ciphertext...)
	tamperedCiphertext[0] ^= 0xff
	if _, err := cipher.Decrypt(nonce, tamperedCiphertext, []byte("event-1")); !errors.Is(err, ErrPayloadAuthentication) {
		t.Fatalf("Decrypt() ciphertext error = %v, want %v", err, ErrPayloadAuthentication)
	}

	if _, err := cipher.Decrypt(nonce, ciphertext, []byte("event-2")); !errors.Is(err, ErrPayloadAuthentication) {
		t.Fatalf("Decrypt() additional-data error = %v, want %v", err, ErrPayloadAuthentication)
	}
}

func TestPayloadCipherValidatesParameters(t *testing.T) {
	t.Parallel()

	if _, err := NewPayloadCipher(make([]byte, payloadKeySize-1)); !errors.Is(err, ErrInvalidPayloadKey) {
		t.Fatalf("NewPayloadCipher() error = %v, want %v", err, ErrInvalidPayloadKey)
	}

	cipher, err := NewPayloadCipher(make([]byte, payloadKeySize))
	if err != nil {
		t.Fatalf("NewPayloadCipher() error = %v", err)
	}
	if _, err := cipher.Decrypt([]byte{1}, []byte{2}, nil); !errors.Is(err, ErrInvalidPayloadNonce) {
		t.Fatalf("Decrypt() error = %v, want %v", err, ErrInvalidPayloadNonce)
	}
}
