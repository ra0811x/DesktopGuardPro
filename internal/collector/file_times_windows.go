package collector

import (
	"os"
	"syscall"
)

func fileBaselineTimestamps(info os.FileInfo) (createdUTC, accessedUTC int64) {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || data == nil {
		modified := info.ModTime().UTC().UnixNano()
		return modified, modified
	}
	return data.CreationTime.Nanoseconds(), data.LastAccessTime.Nanoseconds()
}
