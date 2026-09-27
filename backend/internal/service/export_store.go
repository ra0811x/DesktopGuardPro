package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	defaultExportChunkBytes = 512 * 1024
	defaultExportTTL        = 5 * time.Minute
	defaultExportItemBytes  = 32 * 1024 * 1024
	defaultExportTotalBytes = 128 * 1024 * 1024
)

var (
	ErrExportTokenInvalid  = errors.New("report export token is invalid")
	ErrExportOffsetInvalid = errors.New("report export offset is invalid")
	ErrExportExpired       = errors.New("report export has expired")
	ErrExportCapacity      = errors.New("report export capacity exceeded")
)

type exportStoreOptions struct {
	ChunkBytes int
	TTL        time.Duration
	ItemBytes  int
	TotalBytes int
	Now        func() time.Time
	NewToken   func() (string, error)
}

type exportStore struct {
	mutex      sync.Mutex
	items      map[string]*exportItem
	chunkBytes int
	ttl        time.Duration
	itemBytes  int
	totalBytes int
	usedBytes  int
	now        func() time.Time
	newToken   func() (string, error)
}

type exportItem struct {
	content    []byte
	nextOffset int
	expiresUTC time.Time
	digest     string
	principal  string
}

type exportTransfer struct {
	Token      string    `json:"token"`
	Size       int       `json:"size"`
	SHA256     string    `json:"sha256"`
	ChunkBytes int       `json:"chunkBytes"`
	ExpiresUTC time.Time `json:"expiresUtc"`
}

type exportChunk struct {
	Content    []byte `json:"content"`
	NextOffset int    `json:"nextOffset"`
	Done       bool   `json:"done"`
}

func newExportStore(options exportStoreOptions) (*exportStore, error) {
	if options.ChunkBytes == 0 {
		options.ChunkBytes = defaultExportChunkBytes
	}
	if options.TTL == 0 {
		options.TTL = defaultExportTTL
	}
	if options.ItemBytes == 0 {
		options.ItemBytes = defaultExportItemBytes
	}
	if options.TotalBytes == 0 {
		options.TotalBytes = defaultExportTotalBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewToken == nil {
		options.NewToken = randomExportToken
	}
	if options.ChunkBytes < 1 || options.ChunkBytes >= maximumInlineReportBytes || options.TTL < time.Second ||
		options.ItemBytes < options.ChunkBytes || options.TotalBytes < options.ItemBytes {
		return nil, errors.New("invalid report export store configuration")
	}
	return &exportStore{
		items: make(map[string]*exportItem), chunkBytes: options.ChunkBytes, ttl: options.TTL,
		itemBytes: options.ItemBytes, totalBytes: options.TotalBytes,
		now: options.Now, newToken: options.NewToken,
	}, nil
}

func (store *exportStore) Put(content []byte, principals ...string) (exportTransfer, error) {
	if len(content) == 0 || len(content) > store.itemBytes {
		return exportTransfer{}, ErrExportCapacity
	}
	token, err := store.newToken()
	if err != nil {
		return exportTransfer{}, fmt.Errorf("create report export token: %w", err)
	}
	if len(token) < 32 {
		return exportTransfer{}, ErrExportTokenInvalid
	}
	now := store.now().UTC()
	digest := sha256.Sum256(content)
	item := &exportItem{
		content: append([]byte(nil), content...), expiresUTC: now.Add(store.ttl),
		digest: hex.EncodeToString(digest[:]), principal: exportPrincipal(principals),
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.removeExpiredLocked(now)
	if _, exists := store.items[token]; exists || store.usedBytes+len(content) > store.totalBytes {
		return exportTransfer{}, ErrExportCapacity
	}
	store.items[token] = item
	store.usedBytes += len(content)
	return exportTransfer{
		Token: token, Size: len(content), SHA256: item.digest,
		ChunkBytes: store.chunkBytes, ExpiresUTC: item.expiresUTC,
	}, nil
}

func (store *exportStore) Read(token string, offset int, principals ...string) (exportChunk, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	now := store.now().UTC()
	item, exists := store.items[token]
	if !exists {
		store.removeExpiredLocked(now)
		return exportChunk{}, ErrExportTokenInvalid
	}
	if item.principal != exportPrincipal(principals) {
		return exportChunk{}, ErrExportTokenInvalid
	}
	if !now.Before(item.expiresUTC) {
		store.removeLocked(token, item)
		return exportChunk{}, ErrExportExpired
	}
	if offset != item.nextOffset {
		return exportChunk{}, ErrExportOffsetInvalid
	}
	end := min(offset+store.chunkBytes, len(item.content))
	chunk := exportChunk{
		Content:    append([]byte(nil), item.content[offset:end]...),
		NextOffset: end, Done: end == len(item.content),
	}
	item.nextOffset = end
	if chunk.Done {
		store.removeLocked(token, item)
	}
	return chunk, nil
}

func exportPrincipal(principals []string) string {
	if len(principals) == 0 {
		return ""
	}
	return principals[0]
}

func (store *exportStore) removeExpiredLocked(now time.Time) {
	for token, item := range store.items {
		if !now.Before(item.expiresUTC) {
			store.removeLocked(token, item)
		}
	}
}

func (store *exportStore) removeLocked(token string, item *exportItem) {
	delete(store.items, token)
	store.usedBytes -= len(item.content)
	clear(item.content)
}

func randomExportToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
