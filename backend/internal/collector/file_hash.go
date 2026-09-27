package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

const maximumFileHashBytes int64 = 16 * 1024 * 1024

const defaultLargeFileHashBytesPerSecond int64 = 8 * 1024 * 1024

var (
	openFileForHash            = os.Open
	immediateFileHashByteLimit = maximumFileHashBytes
)

type FileHashQueueOptions struct {
	Capacity       int
	BytesPerSecond int64
}

type fileHashRequest struct {
	ctx      context.Context
	path     string
	complete func(string, string)
}

type FileHashQueue struct {
	jobs           chan fileHashRequest
	bytesPerSecond int64
	cancel         context.CancelFunc
	done           chan struct{}
	mutex          sync.Mutex
	closed         bool
}

func NewFileHashQueue(options FileHashQueueOptions) (*FileHashQueue, error) {
	if options.Capacity <= 0 || options.BytesPerSecond <= 0 {
		return nil, errors.New("file hash queue options are invalid")
	}
	workerContext, cancel := context.WithCancel(context.Background())
	queue := &FileHashQueue{
		jobs: make(chan fileHashRequest, options.Capacity), bytesPerSecond: options.BytesPerSecond,
		cancel: cancel, done: make(chan struct{}),
	}
	go queue.run(workerContext)
	return queue, nil
}

func (queue *FileHashQueue) Enqueue(ctx context.Context, path string, complete func(string, string)) bool {
	if queue == nil || ctx == nil || path == "" || complete == nil {
		return false
	}
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	if queue.closed {
		return false
	}
	select {
	case queue.jobs <- fileHashRequest{ctx: ctx, path: path, complete: complete}:
		return true
	default:
		return false
	}
}

func (queue *FileHashQueue) Close() {
	if queue == nil {
		return
	}
	queue.mutex.Lock()
	if queue.closed {
		queue.mutex.Unlock()
		return
	}
	queue.closed = true
	close(queue.jobs)
	queue.cancel()
	queue.mutex.Unlock()
	<-queue.done
}

func (queue *FileHashQueue) run(workerContext context.Context) {
	defer close(queue.done)
	for request := range queue.jobs {
		if request.ctx.Err() != nil || workerContext.Err() != nil {
			continue
		}
		hash, status := hashFileContentRateLimited(workerContext, request.path, queue.bytesPerSecond)
		if workerContext.Err() == nil && request.ctx.Err() == nil {
			request.complete(hash, status)
		}
	}
}

func hashFileContent(path string) (string, string) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", "unavailable"
	}
	if err != nil {
		return "", "unavailable"
	}
	if !info.Mode().IsRegular() {
		return "", "not_regular_file"
	}
	if info.Size() > immediateFileHashByteLimit {
		return "", "skipped_size_limit"
	}
	file, err := openFileForHash(path)
	if err != nil {
		return "", "unavailable"
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() {
		return "", "unavailable"
	}
	if openedInfo.Size() > immediateFileHashByteLimit {
		return "", "skipped_size_limit"
	}
	digest := sha256.New()
	copied, err := io.Copy(digest, io.LimitReader(file, immediateFileHashByteLimit+1))
	if err != nil {
		return "", "unavailable"
	}
	if copied > immediateFileHashByteLimit {
		return "", "skipped_size_limit"
	}
	return hex.EncodeToString(digest.Sum(nil)), "available"
}

func hashFileContentRateLimited(ctx context.Context, path string, bytesPerSecond int64) (string, string) {
	if ctx == nil || bytesPerSecond <= 0 {
		return "", "unavailable"
	}
	file, err := openFileForHash(path)
	if err != nil {
		return "", "unavailable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", "unavailable"
	}
	digest := sha256.New()
	buffer := make([]byte, 64*1024)
	var copied int64
	started := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return "", "canceled"
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			if _, err := digest.Write(buffer[:count]); err != nil {
				return "", "unavailable"
			}
			copied += int64(count)
			if wait := time.Duration(copied*int64(time.Second)/bytesPerSecond) - time.Since(started); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return "", "canceled"
				case <-timer.C:
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "unavailable"
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), "available"
}
