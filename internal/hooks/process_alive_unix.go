//go:build !windows

package hooks

import (
	"errors"
	"syscall"
)

// processAlive reports whether `pid` is still a live process on this
// host. Sends signal 0 — POSIX guarantees this performs error-checking
// without delivering a signal. ESRCH means "no such process" (dead);
// EPERM means "alive but we lack permission to signal" (treat as live).
// Any other error is conservatively treated as live so a transient
// kernel hiccup cannot reclaim a real holder's lock.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	return true
}
