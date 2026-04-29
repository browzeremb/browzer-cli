//go:build !windows

package workflow

import (
	"os"
	"syscall"
)

// acquireExclusive attempts a non-blocking exclusive flock on the file
// descriptor.  Returns an error if the lock is held by another process.
func acquireExclusive(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
}

// releaseExclusive releases an flock held on the file descriptor.
func releaseExclusive(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_UN)
}

// pidIsAlive reports whether the process with the given PID is still running.
// It uses kill(pid, 0) — no signal is sent; this just tests process existence.
func pidIsAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}
