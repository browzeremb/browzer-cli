package workflow

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path atomically by first writing to a unique
// temporary file in the same directory as path and then renaming it over the
// target.  Using a unique temp name (via os.CreateTemp) means two concurrent
// callers never collide on the same .tmp file, even if the caller's locking
// layer is bypassed or reentrant (e.g. in-process tests on macOS where
// syscall.Flock is reentrant within a process).
// If the write or rename fails the original file (if any) is left untouched
// and the temporary file is removed.
func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	f, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return fmt.Errorf("atomic write open tmp: %w", err)
	}
	tmpPath := f.Name()

	_, writeErr := f.Write(data)
	closeErr := f.Close()

	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write data: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write close: %w", closeErr)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write rename: %w", err)
	}
	return nil
}
