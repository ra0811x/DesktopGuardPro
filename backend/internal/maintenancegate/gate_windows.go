// Package maintenancegate serializes session creation with privileged
// maintenance using a handle in the same protected service data directory.
package maintenancegate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	winapi "golang.org/x/sys/windows"
)

const JournalFileName = "maintenance-transaction.json"

var ErrMaintenanceInProgress = errors.New("maintenance is in progress; finish or recover the pending maintenance transaction before starting a session")

type Lock struct {
	handle    winapi.Handle
	directory string
}

func Acquire(ctx context.Context, directory string) (*Lock, error) {
	path, err := winapi.UTF16PtrFromString(filepath.Join(directory, "maintenance.lock"))
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Share-delete allows an authorized uninstall to remove the data tree
		// after stopping/deleting the service. Other readers/writers remain excluded.
		handle, err := winapi.CreateFile(path, winapi.GENERIC_READ|winapi.GENERIC_WRITE, winapi.FILE_SHARE_DELETE,
			nil, winapi.OPEN_ALWAYS, winapi.FILE_ATTRIBUTE_NORMAL|winapi.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err == nil {
			var info winapi.ByHandleFileInformation
			if err := winapi.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&winapi.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				winapi.CloseHandle(handle)
				return nil, fmt.Errorf("invalid maintenance lock file: %v", err)
			}
			return &Lock{handle: handle, directory: directory}, nil
		}
		if !errors.Is(err, winapi.ERROR_SHARING_VIOLATION) {
			return nil, fmt.Errorf("acquire maintenance lock: %w", err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (lock *Lock) CheckReady() error {
	_, err := os.Lstat(filepath.Join(lock.directory, JournalFileName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrMaintenanceInProgress
}

func (lock *Lock) Close() error {
	if lock == nil || lock.handle == 0 {
		return nil
	}
	handle := lock.handle
	lock.handle = 0
	return winapi.CloseHandle(handle)
}
