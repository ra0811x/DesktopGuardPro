package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type testKeyProtector struct{}

type synchronizedKeyProtector struct{ ready *sync.WaitGroup }

func (protector synchronizedKeyProtector) Protect(key []byte) ([]byte, error) {
	protector.ready.Done()
	protector.ready.Wait()
	return (testKeyProtector{}).Protect(key)
}

func (synchronizedKeyProtector) Unprotect(key []byte) ([]byte, error) {
	return (testKeyProtector{}).Unprotect(key)
}

func TestIndependentKeyStoresCannotOverwriteFirstKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.key")
	var ready sync.WaitGroup
	ready.Add(2)
	type result struct {
		key []byte
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		store, err := NewDataKeyStore(synchronizedKeyProtector{ready: &ready})
		if err != nil {
			t.Fatal(err)
		}
		go func() { key, err := store.LoadOrCreate(path); results <- result{key, err} }()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("create: %v / %v", first.err, second.err)
	}
	if !bytes.Equal(first.key, second.key) {
		t.Fatal("independent stores returned different storage keys")
	}
	restarted, _ := NewDataKeyStore(testKeyProtector{})
	loaded, err := restarted.LoadOrCreate(path)
	if err != nil || !bytes.Equal(loaded, first.key) {
		t.Fatalf("restart key mismatch: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".desktop-guard-key-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary keys remain: %v, %v", files, err)
	}
}

func (testKeyProtector) Protect(key []byte) ([]byte, error) {
	protected := append([]byte("protected:"), key...)
	for index := len("protected:"); index < len(protected); index++ {
		protected[index] ^= 0xa5
	}
	return protected, nil
}

func (testKeyProtector) Unprotect(protected []byte) ([]byte, error) {
	if !bytes.HasPrefix(protected, []byte("protected:")) {
		return nil, ErrKeyUnprotection
	}
	key := append([]byte(nil), protected[len("protected:"):]...)
	for index := range key {
		key[index] ^= 0xa5
	}
	return key, nil
}

func TestDataKeyStoreCreatesAndLoadsProtectedKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keys", "storage.key")
	store, err := NewDataKeyStore(testKeyProtector{})
	if err != nil {
		t.Fatalf("NewDataKeyStore() error = %v", err)
	}

	created, err := store.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate() create error = %v", err)
	}
	if len(created) != payloadKeySize {
		t.Fatalf("created key length = %d, want %d", len(created), payloadKeySize)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(contents, created) {
		t.Fatal("key file contains plaintext key material")
	}

	loaded, err := store.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate() load error = %v", err)
	}
	if !bytes.Equal(loaded, created) {
		t.Fatal("loaded key differs from created key")
	}
}

func TestDataKeyStoreConcurrentCreationReturnsOneKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "storage.key")
	store, err := NewDataKeyStore(testKeyProtector{})
	if err != nil {
		t.Fatalf("NewDataKeyStore() error = %v", err)
	}

	const workers = 8
	keys := make(chan []byte, workers)
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			key, loadErr := store.LoadOrCreate(path)
			if loadErr != nil {
				errorsChannel <- loadErr
				return
			}
			keys <- key
		}()
	}
	wait.Wait()
	close(keys)
	close(errorsChannel)

	for loadErr := range errorsChannel {
		t.Errorf("LoadOrCreate() error = %v", loadErr)
	}
	var first []byte
	for key := range keys {
		if first == nil {
			first = key
			continue
		}
		if !bytes.Equal(key, first) {
			t.Fatal("concurrent LoadOrCreate() calls returned different keys")
		}
	}
}

func TestDataKeyStoreRejectsCorruptedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "storage.key")
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store, err := NewDataKeyStore(testKeyProtector{})
	if err != nil {
		t.Fatalf("NewDataKeyStore() error = %v", err)
	}

	if _, err := store.LoadOrCreate(path); !errors.Is(err, ErrInvalidKeyFile) {
		t.Fatalf("LoadOrCreate() error = %v, want %v", err, ErrInvalidKeyFile)
	}
}

func TestDataKeyStoreValidatesConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := NewDataKeyStore(nil); !errors.Is(err, ErrKeyProtectorRequired) {
		t.Fatalf("NewDataKeyStore() error = %v, want %v", err, ErrKeyProtectorRequired)
	}
	store, err := NewDataKeyStore(testKeyProtector{})
	if err != nil {
		t.Fatalf("NewDataKeyStore() error = %v", err)
	}
	if _, err := store.LoadOrCreate("  "); !errors.Is(err, ErrKeyPathRequired) {
		t.Fatalf("LoadOrCreate() error = %v, want %v", err, ErrKeyPathRequired)
	}
}
