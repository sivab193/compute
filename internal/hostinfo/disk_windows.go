//go:build windows

package hostinfo

import (
	"golang.org/x/sys/windows"
)

// diskInfo reports total and available space (MB) for the volume holding path,
// using GetDiskFreeSpaceEx on Windows.
func diskInfo(path string) (totalMB, freeMB uint64) {
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	if err := windows.GetDiskFreeSpaceEx(p, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
		return 0, 0
	}
	return totalBytes / 1024 / 1024, freeBytesAvailable / 1024 / 1024
}
