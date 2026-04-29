package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ErrLockTimeout is returned by Lock.Acquire when the advisory lock cannot be
// obtained within the configured timeout.
var ErrLockTimeout = errors.New("workflow lock timeout: another process holds the lock")

// inProcMu guards inProcLocks.
// inProcLocks provides intra-process mutual exclusion keyed by canonical
// workflow path.  syscall.Flock is reentrant within a single process on macOS
// and Linux (the kernel grants the lock to any open-file-description in the
// same process), so without an in-process layer goroutines that each call
// lock.Acquire can all succeed simultaneously, breaking the RMW guarantee.
var (
	inProcMu    sync.Mutex
	inProcLocks = map[string]*sync.Mutex{}
)

// inProcAcquire acquires the in-process mutex for path within deadline.
// It polls using TryLock (available since Go 1.18) with the same 10 ms cadence
// as the flock polling loop to avoid blocking the goroutine scheduler.
// Returns true on success, false on timeout.
func inProcAcquire(path string, deadline time.Time) bool {
	inProcMu.Lock()
	mu, ok := inProcLocks[path]
	if !ok {
		mu = &sync.Mutex{}
		inProcLocks[path] = mu
	}
	inProcMu.Unlock()

	for {
		if mu.TryLock() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// inProcRelease releases the in-process mutex for path.
func inProcRelease(path string) {
	inProcMu.Lock()
	mu, ok := inProcLocks[path]
	inProcMu.Unlock()
	if ok {
		mu.Unlock()
	}
}

// lockPayload is the JSON written into the lock file.
type lockPayload struct {
	PID int `json:"pid"`
}

// Lock is an advisory file lock for a workflow.json file.
// The lock file is created at <path>.lock and contains the PID of the holder.
// It is safe to create multiple Lock values for the same path (e.g. in tests);
// they contend via OS-level exclusive file locking AND an in-process mutex
// (because syscall.Flock is reentrant within a single process).
type Lock struct {
	path       string
	timeout    time.Duration
	stderr     io.Writer
	f          *os.File
	inProcHeld bool
}

// NewLock creates a new Lock for the workflow file at path.
// timeout controls how long Acquire polls before returning ErrLockTimeout.
// A zero timeout defaults to 5 seconds.
// stderr receives diagnostic messages (stale-lock warnings, etc.).
func NewLock(path string, timeout time.Duration, stderr io.Writer) (*Lock, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Lock{
		path:    path,
		timeout: timeout,
		stderr:  stderr,
	}, nil
}

// Acquire obtains the advisory lock.  It writes the current PID into the lock
// file.  If a lock file exists with a dead PID it is broken automatically with
// a warning to stderr.  If a live holder exists, Acquire polls until the
// timeout elapses and then returns ErrLockTimeout.
//
// Acquire holds BOTH an in-process mutex (to exclude other goroutines in the
// same process) and an OS-level flock (to exclude other processes).
// syscall.Flock is reentrant within a single process on macOS and Linux, so
// the flock alone is not sufficient for intra-process mutual exclusion.
func (l *Lock) Acquire() error {
	lockPath := l.path + ".lock"
	deadline := time.Now().Add(l.timeout)

	// Acquire the in-process mutex first.  This serialises goroutines within
	// the current process before we even touch the flock.
	if !inProcAcquire(l.path, deadline) {
		return ErrLockTimeout
	}
	l.inProcHeld = true

	for {
		// Try to break a stale lock if present.
		if err := l.maybeBreakStaleLock(lockPath); err != nil {
			// Could not read / decide — just try to acquire anyway.
			_ = err
		}

		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			if time.Now().After(deadline) {
				inProcRelease(l.path)
				l.inProcHeld = false
				return ErrLockTimeout
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// Try to acquire an exclusive OS-level lock (non-blocking).
		if acquireErr := acquireExclusive(f.Fd()); acquireErr != nil {
			_ = f.Close()
			if time.Now().After(deadline) {
				inProcRelease(l.path)
				l.inProcHeld = false
				return ErrLockTimeout
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// We hold the lock — write our PID.
		if err := f.Truncate(0); err != nil {
			_ = releaseExclusive(f.Fd())
			_ = f.Close()
			inProcRelease(l.path)
			l.inProcHeld = false
			return fmt.Errorf("lock truncate: %w", err)
		}
		payload, _ := json.Marshal(lockPayload{PID: os.Getpid()})
		if _, err := f.WriteAt(payload, 0); err != nil {
			_ = releaseExclusive(f.Fd())
			_ = f.Close()
			inProcRelease(l.path)
			l.inProcHeld = false
			return fmt.Errorf("lock write pid: %w", err)
		}

		l.f = f
		return nil
	}
}

// Release releases the advisory lock and removes the lock file.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	lockPath := l.path + ".lock"
	releaseErr := releaseExclusive(l.f.Fd())
	closeErr := l.f.Close()
	l.f = nil
	// Best-effort removal.
	_ = os.Remove(lockPath)
	// Release the in-process mutex so the next waiting goroutine can proceed.
	if l.inProcHeld {
		inProcRelease(l.path)
		l.inProcHeld = false
	}
	if releaseErr != nil {
		return releaseErr
	}
	return closeErr
}

// maybeBreakStaleLock inspects an existing lock file and removes it if the
// recorded PID is no longer alive.
func (l *Lock) maybeBreakStaleLock(lockPath string) error {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		// No lock file or unreadable — nothing to break.
		return err
	}
	var payload lockPayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.PID == 0 {
		// Corrupt lock file — break it.
		_, _ = fmt.Fprintf(l.stderr, "warn: breaking corrupt lock file %s\n", lockPath)
		_ = os.Remove(lockPath)
		return nil
	}
	if !pidIsAlive(payload.PID) {
		_, _ = fmt.Fprintf(l.stderr, "warn: breaking stale lock (pid %d) for %s\n", payload.PID, lockPath)
		_ = os.Remove(lockPath)
	}
	return nil
}
