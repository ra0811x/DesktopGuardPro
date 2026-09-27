package collector

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const (
	directoryChangeBufferSize = 64 * 1024
	directoryWaitMilliseconds = 100
	fileNotifyHeaderSize      = 12

	fileActionAdded          = windows.FILE_ACTION_ADDED
	fileActionRemoved        = windows.FILE_ACTION_REMOVED
	fileActionModified       = windows.FILE_ACTION_MODIFIED
	fileActionRenamedOldName = windows.FILE_ACTION_RENAMED_OLD_NAME
	fileActionRenamedNewName = windows.FILE_ACTION_RENAMED_NEW_NAME
)

const directoryNotifyMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME |
	windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_ATTRIBUTES |
	windows.FILE_NOTIFY_CHANGE_SIZE |
	windows.FILE_NOTIFY_CHANGE_LAST_WRITE |
	windows.FILE_NOTIFY_CHANGE_CREATION |
	windows.FILE_NOTIFY_CHANGE_SECURITY

func watchWindowsDirectory(
	ctx context.Context,
	root string,
	watchSubtree bool,
	handleChanges func([]FileChange) error,
) error {
	rootPointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return fmt.Errorf("encode watched directory path: %w", err)
	}
	directory, err := windows.CreateFile(
		rootPointer,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return fmt.Errorf("open watched directory: %w", err)
	}
	defer windows.CloseHandle(directory)

	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return fmt.Errorf("create directory change event: %w", err)
	}
	defer windows.CloseHandle(event)
	buffer := make([]byte, directoryChangeBufferSize)
	armed := false

	for {
		overlapped := windows.Overlapped{HEvent: event}
		var returned uint32
		err := windows.ReadDirectoryChanges(
			directory,
			&buffer[0],
			uint32(len(buffer)),
			watchSubtree,
			directoryNotifyMask,
			&returned,
			&overlapped,
			0,
		)
		if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
			if errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				return ErrDirectoryChangeOverflow
			}
			return fmt.Errorf("read directory changes: %w", err)
		}
		cancelPending := func() {
			_ = windows.CancelIoEx(directory, &overlapped)
			_ = windows.GetOverlappedResult(directory, &overlapped, &returned, true)
		}
		if !armed {
			armed = true
			// Reconcile the baseline only after an actual read has been posted.
			if err := handleChanges(nil); err != nil {
				cancelPending()
				return err
			}
		}

		for {
			if ctx.Err() != nil {
				cancelPending()
				return ctx.Err()
			}
			waitResult, waitErr := windows.WaitForSingleObject(event, directoryWaitMilliseconds)
			if waitErr != nil {
				cancelPending()
				return fmt.Errorf("wait for directory changes: %w", waitErr)
			}
			if waitResult == uint32(windows.WAIT_TIMEOUT) {
				if err := handleChanges(nil); err != nil {
					cancelPending()
					return err
				}
				continue
			}
			if waitResult != windows.WAIT_OBJECT_0 {
				cancelPending()
				return fmt.Errorf("unexpected directory wait result %d", waitResult)
			}
			break
		}

		if err := windows.GetOverlappedResult(directory, &overlapped, &returned, false); err != nil {
			if errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				return ErrDirectoryChangeOverflow
			}
			return fmt.Errorf("complete directory change read: %w", err)
		}
		if returned == 0 {
			return ErrDirectoryChangeOverflow
		}
		changes, err := parseFileNotifyInformation(buffer[:returned])
		if err != nil {
			return err
		}
		if err := handleChanges(changes); err != nil {
			return err
		}
	}
}

func parseFileNotifyInformation(buffer []byte) ([]FileChange, error) {
	changes := make([]FileChange, 0, 8)
	for offset := 0; ; {
		if len(buffer)-offset < fileNotifyHeaderSize {
			return nil, errors.New("truncated FILE_NOTIFY_INFORMATION header")
		}
		nextOffset := int(binary.LittleEndian.Uint32(buffer[offset : offset+4]))
		action := binary.LittleEndian.Uint32(buffer[offset+4 : offset+8])
		nameBytes := int(binary.LittleEndian.Uint32(buffer[offset+8 : offset+12]))
		if nameBytes == 0 || nameBytes%2 != 0 || nameBytes > len(buffer)-offset-fileNotifyHeaderSize {
			return nil, errors.New("invalid FILE_NOTIFY_INFORMATION name length")
		}
		nameData := buffer[offset+fileNotifyHeaderSize : offset+fileNotifyHeaderSize+nameBytes]
		name := make([]uint16, nameBytes/2)
		for index := range name {
			name[index] = binary.LittleEndian.Uint16(nameData[index*2 : index*2+2])
		}
		changes = append(changes, FileChange{Action: action, RelativePath: windows.UTF16ToString(name)})

		if nextOffset == 0 {
			break
		}
		if nextOffset < fileNotifyHeaderSize || nextOffset%4 != 0 || offset+nextOffset >= len(buffer) {
			return nil, errors.New("invalid FILE_NOTIFY_INFORMATION next offset")
		}
		offset += nextOffset
	}
	return changes, nil
}
