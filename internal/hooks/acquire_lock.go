package hooks

import (
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockReleaser releases an acquired lock. Safe to call once; subsequent
// calls return nil so callers can `defer release()` without bookkeeping.
type LockReleaser func() error

// AcquireLock attempts to take a named PID-based advisory lock under
// $HOME/.browzer/<name>.lock. Semantics mirror the Node guard helper in
// packages/skills/hooks/guards/_util.mjs:
//
//   - The lock file contains the holder's PID as ASCII decimal.
//   - When the file already exists, the contents are parsed and
//     `processAlive(pid)` is consulted. A dead holder (signal 0 yields
//     ESRCH on Unix; PID 0 or negative is also treated as stale) lets
//     us unlink and retry once.
//   - A live holder returns (nil, false, nil) so the caller can skip
//     the protected region without raising an error.
//
// Errors are reserved for unexpected I/O (e.g. cannot create the
// .browzer directory, cannot stat a partial file). PID-file contents
// that fail to parse are treated as stale rather than fatal.
//
// `name` is the lock identifier (e.g. "telemetry-flush"). Filesystem
// separators in `name` are rejected to keep all locks under a single
// directory; this matches the Node helper's flat layout.
func AcquireLock(name string) (LockReleaser, bool, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, false, fmt.Errorf("acquire lock: invalid lock name %q", name)
	}

	dir := browzerHomeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, fmt.Errorf("ensure lock dir: %w", err)
	}
	lockPath := filepath.Join(dir, name+".lock")

	// Up to 3 attempts: if the existing holder is stale, unlink and retry.
	// Attempts 2 and 3 are preceded by a 1–3 ms jittered sleep so that two
	// concurrent reclaimers do not collide on the re-create in lock-step.
	for attempt := range 3 {
		if attempt > 0 {
			// 1 ms base + up to 2 ms jitter → 1–3 ms backoff.
			time.Sleep(time.Millisecond + time.Duration(mrand.N(2_000_000)))
		}
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return writePIDAndBuildReleaser(f, lockPath)
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, fmt.Errorf("create lock: %w", err)
		}

		// Holder present: classify as live or stale.
		stale, classifyErr := isLockStale(lockPath)
		if classifyErr != nil {
			return nil, false, classifyErr
		}
		if !stale {
			return nil, false, nil
		}
		// Stale: drop and retry. A concurrent reclaimer may unlink first;
		// ENOENT here is fine — the next loop iteration will create afresh.
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, false, fmt.Errorf("reclaim stale lock: %w", err)
		}
	}
	// Three attempts all collided with a live holder racing in between —
	// treat as "could not acquire" rather than spinning indefinitely.
	return nil, false, nil
}

// writePIDAndBuildReleaser writes the current PID into the freshly-opened
// lock file and returns a one-shot releaser that unlinks the file. The
// releaser is idempotent: a second invocation returns nil without
// retrying the syscall.
func writePIDAndBuildReleaser(f *os.File, lockPath string) (LockReleaser, bool, error) {
	pid := os.Getpid()
	if _, err := f.WriteString(strconv.Itoa(pid)); err != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, false, fmt.Errorf("write lock pid: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(lockPath)
		return nil, false, fmt.Errorf("close lock file: %w", err)
	}

	released := false
	release := func() error {
		if released {
			return nil
		}
		released = true
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("release lock: %w", err)
		}
		return nil
	}
	return release, true, nil
}

// isLockStale reads the PID from `lockPath` and returns true when the
// holder is no longer running. Treats unreadable, empty, malformed, or
// non-positive PID files as stale so a partial write from a crashed
// writer cannot deadlock the lock indefinitely.
func isLockStale(lockPath string) (bool, error) {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Lock vanished between OpenFile(EXCL) and ReadFile — the
			// next loop iteration will recreate it. Treat as stale so
			// the retry happens.
			return true, nil
		}
		return false, fmt.Errorf("read lock pid: %w", err)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return true, nil
	}
	pid, parseErr := strconv.Atoi(s)
	if parseErr != nil {
		return true, nil
	}
	if pid <= 0 {
		return true, nil
	}
	return !processAlive(pid), nil
}
