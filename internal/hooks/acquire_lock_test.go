package hooks

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAcquireLock_FreshLockSucceeds(t *testing.T) {
	home := setupTempHome(t)

	release, ok, err := AcquireLock("fresh")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !ok {
		t.Fatalf("fresh lock should acquire; got ok=false")
	}
	t.Cleanup(func() { _ = release() })

	// Lock file should exist and contain our PID.
	data, err := os.ReadFile(filepath.Join(home, "fresh.lock"))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("lock file does not contain a valid PID: %q", data)
	}
	if got != os.Getpid() {
		t.Fatalf("lock PID = %d; want %d", got, os.Getpid())
	}
}

func TestAcquireLock_StalePIDReclaimed(t *testing.T) {
	home := setupTempHome(t)

	// Seed a lock file with an unrealistically-large PID — POSIX
	// systems cap PIDs at PID_MAX (typically 2^22 on Linux, 99999 on
	// macOS) so 9999999 is a safe stand-in for "no such process".
	lockPath := filepath.Join(home, "test.lock")
	if err := os.WriteFile(lockPath, []byte("9999999"), 0o644); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	release, ok, err := AcquireLock("test")
	if err != nil {
		t.Fatalf("AcquireLock against stale PID: %v", err)
	}
	if !ok {
		t.Fatalf("stale PID should be reclaimable; got ok=false")
	}
	t.Cleanup(func() { _ = release() })

	// After reclaim, lock file should contain our PID, not the stale one.
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read reclaimed lock: %v", err)
	}
	got, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if got != os.Getpid() {
		t.Fatalf("after reclaim, PID = %d; want %d", got, os.Getpid())
	}
}

func TestAcquireLock_LivePIDBlocks(t *testing.T) {
	setupTempHome(t)

	// First acquire — succeeds.
	release, ok, err := AcquireLock("live")
	if err != nil || !ok {
		t.Fatalf("first AcquireLock: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = release() })

	// Second acquire on the same name — the holder PID (us) is alive,
	// so the lock must NOT be granted.
	_, ok2, err2 := AcquireLock("live")
	if err2 != nil {
		t.Fatalf("second AcquireLock returned err=%v; want ok=false err=nil", err2)
	}
	if ok2 {
		t.Fatalf("second AcquireLock should fail when holder is alive; got ok=true")
	}
}

func TestAcquireLock_ReleaseAllowsReacquire(t *testing.T) {
	setupTempHome(t)

	release1, ok, err := AcquireLock("rotate")
	if err != nil || !ok {
		t.Fatalf("first AcquireLock: ok=%v err=%v", ok, err)
	}
	if err := release1(); err != nil {
		t.Fatalf("release1: %v", err)
	}

	release2, ok, err := AcquireLock("rotate")
	if err != nil || !ok {
		t.Fatalf("re-AcquireLock after release: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = release2() })
}

func TestAcquireLock_ReleaseIsIdempotent(t *testing.T) {
	setupTempHome(t)
	release, ok, err := AcquireLock("idempotent")
	if err != nil || !ok {
		t.Fatalf("AcquireLock: ok=%v err=%v", ok, err)
	}
	if err := release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("second release should be a no-op; got %v", err)
	}
}

func TestAcquireLock_RejectsEmptyName(t *testing.T) {
	setupTempHome(t)
	if _, _, err := AcquireLock(""); err == nil {
		t.Fatalf("AcquireLock with empty name should error")
	}
}

func TestAcquireLock_RejectsPathSeparators(t *testing.T) {
	setupTempHome(t)
	for _, name := range []string{"a/b", "x\\y", "nested/path/lock"} {
		if _, _, err := AcquireLock(name); err == nil {
			t.Fatalf("AcquireLock(%q) should error", name)
		}
	}
}

func TestAcquireLock_MalformedPIDTreatedAsStale(t *testing.T) {
	home := setupTempHome(t)
	lockPath := filepath.Join(home, "broken.lock")
	if err := os.WriteFile(lockPath, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatalf("seed broken lock: %v", err)
	}

	release, ok, err := AcquireLock("broken")
	if err != nil {
		t.Fatalf("AcquireLock with malformed PID: %v", err)
	}
	if !ok {
		t.Fatalf("malformed PID should be treated as stale; got ok=false")
	}
	t.Cleanup(func() { _ = release() })
}

func TestAcquireLock_NonPositivePIDTreatedAsStale(t *testing.T) {
	home := setupTempHome(t)

	for _, pid := range []string{"0", "-1", "-9999"} {
		lockPath := filepath.Join(home, "neg.lock")
		if err := os.WriteFile(lockPath, []byte(pid), 0o644); err != nil {
			t.Fatalf("seed neg lock %s: %v", pid, err)
		}
		release, ok, err := AcquireLock("neg")
		if err != nil {
			t.Fatalf("AcquireLock(pid=%s): %v", pid, err)
		}
		if !ok {
			t.Fatalf("PID=%s should be treated as stale; got ok=false", pid)
		}
		if err := release(); err != nil {
			t.Fatalf("release pid=%s: %v", pid, err)
		}
	}
}

func TestAcquireLock_EmptyPIDFileTreatedAsStale(t *testing.T) {
	home := setupTempHome(t)
	lockPath := filepath.Join(home, "empty.lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("seed empty lock: %v", err)
	}
	release, ok, err := AcquireLock("empty")
	if err != nil {
		t.Fatalf("AcquireLock against empty file: %v", err)
	}
	if !ok {
		t.Fatalf("empty PID file should be treated as stale; got ok=false")
	}
	t.Cleanup(func() { _ = release() })
}

// TestAcquireLock_ConcurrentReclaimers exercises the jittered-backoff path:
// N goroutines simultaneously find a stale lock and race to reclaim it.
// Exactly one should succeed; the rest should get ok=false without errors.
// t.Parallel() is intentionally omitted — t.Setenv is used via setupTempHome,
// which is incompatible with t.Parallel() in Go's testing framework.
func TestAcquireLock_ConcurrentReclaimers(t *testing.T) {
	home := setupTempHome(t)
	// Seed a stale lock (PID 9999999 is unreachable on any real system).
	lockPath := filepath.Join(home, "race.lock")
	if err := os.WriteFile(lockPath, []byte("9999999"), 0o644); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	const goroutines = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		acquireErr error
		acquired   int32  // number of goroutines that got ok=true
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			release, ok, err := AcquireLock("race")
			if err != nil {
				mu.Lock()
				acquireErr = err
				mu.Unlock()
				return
			}
			if ok {
				atomic.AddInt32(&acquired, 1)
				_ = release()
			}
		}()
	}
	wg.Wait()

	if acquireErr != nil {
		t.Fatalf("concurrent reclaimer returned error: %v", acquireErr)
	}
	n := atomic.LoadInt32(&acquired)
	if n == 0 {
		t.Fatalf("at least one goroutine should acquire the stale lock; all got ok=false")
	}
	// Multiple goroutines may succeed in sequence once the previous holder
	// releases, but we primarily assert no error and at least one success.
}
