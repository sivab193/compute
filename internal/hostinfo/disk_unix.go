//go:build !windows

package hostinfo

import "syscall"

// diskInfo reports total and available space (MB) for the filesystem holding
// path, using statfs on Unix-like systems (Linux, macOS).
func diskInfo(path string) (totalMB, freeMB uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	totalMB = st.Blocks * bs / 1024 / 1024
	freeMB = st.Bavail * bs / 1024 / 1024
	return totalMB, freeMB
}
