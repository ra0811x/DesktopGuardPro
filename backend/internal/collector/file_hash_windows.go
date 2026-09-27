package collector

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func init() {
	openFileForHash = openFileForHashWindows
}

func openFileForHashWindows(path string) (*os.File, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open file for hashing: %w", err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open file for hashing: create file wrapper")
	}
	return file, nil
}
