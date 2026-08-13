//go:build windows

package fslock

import (
	"os"

	"golang.org/x/sys/windows"
)

// lock takes an exclusive lock on the whole file (1 byte at offset 0) without
// blocking: LockFileEx with LOCKFILE_FAIL_IMMEDIATELY returns immediately when
// the lock is held elsewhere.
func lock(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol,
	)
}

func unlock(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
