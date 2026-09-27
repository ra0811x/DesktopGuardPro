package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const payloadKeySize = 32

var (
	ErrInvalidPayloadKey     = errors.New("payload encryption key must be 32 bytes")
	ErrInvalidPayloadNonce   = errors.New("invalid payload encryption nonce")
	ErrPayloadAuthentication = errors.New("payload authentication failed")
)

type PayloadCipher struct {
	aead cipher.AEAD
}

func NewPayloadCipher(key []byte) (*PayloadCipher, error) {
	if len(key) != payloadKeySize {
		return nil, ErrInvalidPayloadKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM cipher: %w", err)
	}
	return &PayloadCipher{aead: aead}, nil
}

func (cipher *PayloadCipher) Encrypt(plaintext, additionalData []byte) ([]byte, []byte, error) {
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate payload nonce: %w", err)
	}

	ciphertext := cipher.aead.Seal(nil, nonce, plaintext, additionalData)
	return nonce, ciphertext, nil
}

func (cipher *PayloadCipher) Decrypt(nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if len(nonce) != cipher.aead.NonceSize() {
		return nil, ErrInvalidPayloadNonce
	}

	plaintext, err := cipher.aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPayloadAuthentication, err)
	}
	return plaintext, nil
}
