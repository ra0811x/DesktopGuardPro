package service

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestExportStoreReadsSequentialChunksAndDeletesCompletedItem(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	store := testExportStore(t, &now, 4, 12)
	transfer, err := store.Put([]byte("abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	if transfer.Size != 10 || transfer.ChunkBytes != 4 || len(transfer.SHA256) != 64 {
		t.Fatalf("unexpected transfer: %+v", transfer)
	}

	first, err := store.Read(transfer.Token, 0)
	if err != nil || !bytes.Equal(first.Content, []byte("abcd")) || first.NextOffset != 4 || first.Done {
		t.Fatalf("unexpected first chunk: %+v error=%v", first, err)
	}
	if _, err := store.Read(transfer.Token, 0); !errors.Is(err, ErrExportOffsetInvalid) {
		t.Fatalf("expected invalid offset, got %v", err)
	}
	second, err := store.Read(transfer.Token, 4)
	if err != nil || !bytes.Equal(second.Content, []byte("efgh")) || second.Done {
		t.Fatalf("unexpected second chunk: %+v error=%v", second, err)
	}
	last, err := store.Read(transfer.Token, 8)
	if err != nil || !bytes.Equal(last.Content, []byte("ij")) || !last.Done {
		t.Fatalf("unexpected last chunk: %+v error=%v", last, err)
	}
	if _, err := store.Read(transfer.Token, 10); !errors.Is(err, ErrExportTokenInvalid) {
		t.Fatalf("completed transfer was retained: %v", err)
	}
}

func TestExportStoreExpiresAndEnforcesCapacity(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	store := testExportStore(t, &now, 4, 8)
	transfer, err := store.Put([]byte("12345678"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put([]byte("x")); !errors.Is(err, ErrExportCapacity) {
		t.Fatalf("expected total capacity error, got %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Read(transfer.Token, 0); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("expected expired export, got %v", err)
	}
	if _, err := store.Put([]byte("new")); err != nil {
		t.Fatalf("expired capacity was not released: %v", err)
	}
}

func TestExportStoreBindsTokenToClientPrincipal(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	store := testExportStore(t, &now, 4, 8)
	transfer, err := store.Put([]byte("private"), "S-1-5-21-owner\x002")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(transfer.Token, 0, "S-1-5-21-other\x002"); !errors.Is(err, ErrExportTokenInvalid) {
		t.Fatalf("another user read report token: %v", err)
	}
	if _, err := store.Read(transfer.Token, 0, "S-1-5-21-owner\x003"); !errors.Is(err, ErrExportTokenInvalid) {
		t.Fatalf("another Windows session read report token: %v", err)
	}
	chunk, err := store.Read(transfer.Token, 0, "S-1-5-21-owner\x002")
	if err != nil || !bytes.Equal(chunk.Content, []byte("priv")) {
		t.Fatalf("owner could not read report token: %+v %v", chunk, err)
	}
}

func testExportStore(t *testing.T, now *time.Time, chunkBytes, totalBytes int) *exportStore {
	t.Helper()
	tokenIndex := 0
	store, err := newExportStore(exportStoreOptions{
		ChunkBytes: chunkBytes, TTL: time.Minute, ItemBytes: totalBytes, TotalBytes: totalBytes,
		Now: func() time.Time { return *now },
		NewToken: func() (string, error) {
			tokenIndex++
			return "0123456789abcdef0123456789abcdef" + string(rune('0'+tokenIndex)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
