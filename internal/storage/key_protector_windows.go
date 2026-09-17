package storage

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ErrInvalidProtectedKey = errors.New("protected key is empty")
	ErrKeyProtection       = errors.New("protect storage key")
	ErrKeyUnprotection     = errors.New("unprotect storage key")
)

var dpapiEntropy = []byte("Desktop Guard Pro|storage-key|v1")

type DPAPIKeyProtector struct{}

func NewDPAPIKeyProtector() *DPAPIKeyProtector {
	return &DPAPIKeyProtector{}
}

func (*DPAPIKeyProtector) Protect(key []byte) ([]byte, error) {
	if len(key) != payloadKeySize {
		return nil, ErrInvalidPayloadKey
	}

	input := dataBlob(key)
	entropy := dataBlob(dpapiEntropy)
	var output windows.DataBlob
	if err := windows.CryptProtectData(
		&input,
		nil,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyProtection, err)
	}
	runtime.KeepAlive(key)
	runtime.KeepAlive(dpapiEntropy)

	return copyAndFreeBlob(output, false)
}

func (*DPAPIKeyProtector) Unprotect(protected []byte) ([]byte, error) {
	if len(protected) == 0 {
		return nil, ErrInvalidProtectedKey
	}

	input := dataBlob(protected)
	entropy := dataBlob(dpapiEntropy)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(
		&input,
		nil,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnprotection, err)
	}
	runtime.KeepAlive(protected)
	runtime.KeepAlive(dpapiEntropy)

	key, err := copyAndFreeBlob(output, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnprotection, err)
	}
	if len(key) != payloadKeySize {
		clear(key)
		return nil, fmt.Errorf("%w: unexpected key length %d", ErrKeyUnprotection, len(key))
	}
	return key, nil
}

func dataBlob(data []byte) windows.DataBlob {
	if len(data) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

func copyAndFreeBlob(blob windows.DataBlob, sensitive bool) ([]byte, error) {
	if blob.Size == 0 || blob.Data == nil {
		return nil, errors.New("DPAPI returned an empty blob")
	}

	buffer := unsafe.Slice(blob.Data, int(blob.Size))
	result := append([]byte(nil), buffer...)
	if sensitive {
		clear(buffer)
	}
	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data))); err != nil {
		if sensitive {
			clear(result)
		}
		return nil, fmt.Errorf("free DPAPI output: %w", err)
	}
	return result, nil
}
