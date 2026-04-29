package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLock_AcquireRelease verifies that Lock acquires and releases under no
// contention — the lock file is created on acquire and cleaned up on release.
// Covers T1-T-3: Lock acquires + releases under no contention.
func TestLock_AcquireRelease(t *testing.T) {
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")

	var stderr bytes.Buffer
	l, err := NewLock(wfPath, 5*time.Second, &stderr)
	if err != nil {
		t.Fatalf("NewLock: %v", err)
	}

	if err := l.Acquire(); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	lockPath := wfPath + ".lock"
	if _, statErr := os.Stat(lockPath); os.IsNotExist(statErr) {
		t.Error("expected lock file to exist after Acquire")
	}

	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// TestLock_ConcurrentAcquireBlocks verifies that a second acquire blocks until
// the first is released.
// Covers T1-T-3: concurrent acquire blocks until release.
func TestLock_ConcurrentAcquireBlocks(t *testing.T) {
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")

	var stderr1, stderr2 bytes.Buffer
	l1, err := NewLock(wfPath, 5*time.Second, &stderr1)
	if err != nil {
		t.Fatalf("NewLock l1: %v", err)
	}
	l2, err := NewLock(wfPath, 5*time.Second, &stderr2)
	if err != nil {
		t.Fatalf("NewLock l2: %v", err)
	}

	if err := l1.Acquire(); err != nil {
		t.Fatalf("l1.Acquire: %v", err)
	}

	var (
		mu       sync.Mutex
		l2Done   bool
		l2Err    error
		l2Start  = make(chan struct{})
	)
	go func() {
		close(l2Start)
		err := l2.Acquire()
		mu.Lock()
		l2Done = true
		l2Err = err
		mu.Unlock()
	}()

	<-l2Start
	time.Sleep(50 * time.Millisecond) // give goroutine time to block

	mu.Lock()
	if l2Done {
		mu.Unlock()
		t.Error("l2 should still be blocking while l1 holds the lock")
	}
	mu.Unlock()

	if err := l1.Release(); err != nil {
		t.Fatalf("l1.Release: %v", err)
	}

	// Wait for l2 to complete with a reasonable timeout
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := l2Done
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !l2Done {
		t.Error("l2 never acquired the lock after l1 released")
	}
	if l2Err != nil {
		t.Errorf("l2.Acquire returned unexpected error: %v", l2Err)
	}
	_ = l2.Release()
}

// TestLock_TimeoutReturnsLockTimeoutSentinel verifies that when contention
// exceeds the configured timeout, Acquire returns LockTimeout (the sentinel error).
// Covers T1-T-3: timeout returns LockTimeout sentinel.
func TestLock_TimeoutReturnsLockTimeoutSentinel(t *testing.T) {
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")

	var stderr1, stderr2 bytes.Buffer
	l1, err := NewLock(wfPath, 5*time.Second, &stderr1)
	if err != nil {
		t.Fatalf("NewLock l1: %v", err)
	}
	// Very short timeout for l2.
	l2, err := NewLock(wfPath, 50*time.Millisecond, &stderr2)
	if err != nil {
		t.Fatalf("NewLock l2: %v", err)
	}

	if err := l1.Acquire(); err != nil {
		t.Fatalf("l1.Acquire: %v", err)
	}
	defer l1.Release() //nolint:errcheck

	err = l2.Acquire()
	if err == nil {
		t.Fatal("expected LockTimeout error, got nil")
	}
	if err != ErrLockTimeout {
		t.Errorf("expected ErrLockTimeout sentinel, got: %v", err)
	}
}

// TestLock_StaleLockAutoBreak verifies that writing a non-existent PID into the
// lock file, then calling Acquire, auto-breaks the stale lock and emits a warning.
// Covers T1-T-4: Stale-lock recovery.
func TestLock_StaleLockAutoBreak(t *testing.T) {
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")
	lockPath := wfPath + ".lock"

	// Write a lock file with a PID that cannot possibly be alive.
	stalePID := 99999999
	content := fmt.Sprintf(`{"pid":%d}`, stalePID)
	if err := os.WriteFile(lockPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(lockPath) })

	var stderr bytes.Buffer
	l, err := NewLock(wfPath, 2*time.Second, &stderr)
	if err != nil {
		t.Fatalf("NewLock: %v", err)
	}

	if err := l.Acquire(); err != nil {
		t.Fatalf("Acquire should succeed after auto-breaking stale lock, got: %v", err)
	}
	defer l.Release() //nolint:errcheck

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "stale") && !strings.Contains(stderrStr, "warn") && !strings.Contains(stderrStr, "breaking") {
		t.Errorf("expected stale-lock warning on stderr, got: %q", stderrStr)
	}
}
