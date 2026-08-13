// Package fslock provides a cross-platform exclusive file lock used to
// serialize read-modify-write cycles on the config and identity index files.
//
// The Unix implementation uses flock(2); the Windows implementation uses
// LockFileEx. Both are selected at build time via build tags so anp-cli builds
// on macOS, Linux, and Windows from a single code path.
package fslock

import (
	"os"
	"path/filepath"
)

// Acquire takes an exclusive lock on the file at path (creating it if needed)
// and returns the open handle. Call Release to unlock and close it.
func Acquire(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lock(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Release unlocks and closes a handle returned by Acquire. A nil handle is a
// no-op.
func Release(f *os.File) {
	if f == nil {
		return
	}
	_ = unlock(f)
	_ = f.Close()
}
