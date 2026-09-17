package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	keyFileVersion      = byte(1)
	keyFileHeaderSize   = 13
	maximumProtectedKey = 1 << 20
)

var keyFileMagic = [8]byte{'D', 'G', 'P', 'K', 'E', 'Y', '\r', '\n'}

var (
	ErrKeyProtectorRequired = errors.New("key protector is required")
	ErrKeyPathRequired      = errors.New("key path is required")
	ErrInvalidKeyFile       = errors.New("invalid protected key file")
)

type KeyProtector interface {
	Protect(key []byte) ([]byte, error)
	Unprotect(protected []byte) ([]byte, error)
}

type DataKeyStore struct {
	protector KeyProtector
	mutex     sync.Mutex
}

func NewDataKeyStore(protector KeyProtector) (*DataKeyStore, error) {
	if protector == nil {
		return nil, ErrKeyProtectorRequired
	}
	return &DataKeyStore{protector: protector}, nil
}

func (store *DataKeyStore) LoadOrCreate(path string) ([]byte, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrKeyPathRequired
	}

	key, err := store.load(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key = make([]byte, payloadKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate storage key: %w", err)
	}
	protected, err := store.protector.Protect(key)
	if err != nil {
		clear(key)
		return nil, err
	}
	encoded, err := encodeKeyFile(protected)
	if err != nil {
		clear(key)
		return nil, err
	}

	if err := writeKeyFileAtomically(path, encoded); err != nil {
		if errors.Is(err, os.ErrExist) {
			clear(key)
			return store.load(path)
		}
		clear(key)
		return nil, err
	}
	return key, nil
}

func (store *DataKeyStore) load(path string) ([]byte, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read protected key: %w", err)
	}
	protected, err := decodeKeyFile(encoded)
	if err != nil {
		return nil, err
	}
	key, err := store.protector.Unprotect(protected)
	if err != nil {
		return nil, err
	}
	if len(key) != payloadKeySize {
		clear(key)
		return nil, fmt.Errorf("%w: unexpected plaintext length", ErrInvalidKeyFile)
	}
	return key, nil
}

func encodeKeyFile(protected []byte) ([]byte, error) {
	if len(protected) == 0 || len(protected) > maximumProtectedKey {
		return nil, fmt.Errorf("%w: protected payload length", ErrInvalidKeyFile)
	}

	encoded := make([]byte, keyFileHeaderSize+len(protected))
	copy(encoded, keyFileMagic[:])
	encoded[8] = keyFileVersion
	binary.LittleEndian.PutUint32(encoded[9:13], uint32(len(protected)))
	copy(encoded[keyFileHeaderSize:], protected)
	return encoded, nil
}

func decodeKeyFile(encoded []byte) ([]byte, error) {
	if len(encoded) < keyFileHeaderSize || !bytes.Equal(encoded[:8], keyFileMagic[:]) {
		return nil, ErrInvalidKeyFile
	}
	if encoded[8] != keyFileVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidKeyFile, encoded[8])
	}
	length := int(binary.LittleEndian.Uint32(encoded[9:13]))
	if length == 0 || length > maximumProtectedKey || len(encoded) != keyFileHeaderSize+length {
		return nil, fmt.Errorf("%w: protected payload length", ErrInvalidKeyFile)
	}
	return encoded[keyFileHeaderSize:], nil
}

func writeKeyFileAtomically(path string, contents []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".desktop-guard-key-*")
	if err != nil {
		return fmt.Errorf("create temporary key file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary key file: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write temporary key file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary key file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary key file: %w", err)
	}
	// Publish a fully flushed inode without replacing a winner from another
	// instance. Both paths are in the same directory and therefore volume.
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("commit protected key file: %w", err)
	}
	return nil
}
