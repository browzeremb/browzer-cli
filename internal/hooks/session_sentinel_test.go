package hooks

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func randomLabel(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return "test-" + hex.EncodeToString(b[:])
}

// withTempHome points $HOME at a temp dir for the duration of the test,
// so sentinel files don't pollute the real ~/.browzer/session/ tree.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Drop any inherited session ids so resolveSessionKey picks ppid.
	t.Setenv("CLAUDE_SESSION_ID", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	return tmp
}

func TestSessionBannerEmittedOnce_FirstCallTrue(t *testing.T) {
	home := withTempHome(t)
	label := randomLabel(t)

	if got := SessionBannerEmittedOnce(label); !got {
		t.Fatalf("first call should return true (first emission)")
	}
	// Sentinel file must exist under $HOME/.browzer/session/.
	matches, _ := filepath.Glob(filepath.Join(home, ".browzer", "session", label+"-*.flag"))
	if len(matches) == 0 {
		t.Fatalf("expected sentinel file under %s/.browzer/session/, found none", home)
	}
}

func TestSessionBannerEmittedOnce_SecondCallFalse(t *testing.T) {
	withTempHome(t)
	label := randomLabel(t)

	first := SessionBannerEmittedOnce(label)
	second := SessionBannerEmittedOnce(label)
	if !first {
		t.Fatalf("first should be true, got %v", first)
	}
	if second {
		t.Fatalf("second should be false (already emitted), got %v", second)
	}
}

func TestSessionBannerEmittedOnce_DifferentLabelsIndependent(t *testing.T) {
	withTempHome(t)
	labelA := randomLabel(t)
	labelB := randomLabel(t)
	if !SessionBannerEmittedOnce(labelA) {
		t.Fatalf("labelA first emission should be true")
	}
	if !SessionBannerEmittedOnce(labelB) {
		t.Fatalf("labelB first emission should be true (independent label)")
	}
}

// Required invariant test: under heavy concurrency, at least one
// emission must succeed (banner-never is NOT acceptable). Banner-twice
// is acceptable in degraded filesystems; banner-never is a regression.
func TestSessionBannerEmittedOnce_ConcurrentAtLeastOne(t *testing.T) {
	withTempHome(t)
	label := randomLabel(t)

	const N = 32
	var wg sync.WaitGroup
	var emissions atomic.Int32
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			if SessionBannerEmittedOnce(label) {
				emissions.Add(1)
			}
		}()
	}
	wg.Wait()

	if emissions.Load() < 1 {
		t.Fatalf("expected ≥1 emission across %d goroutines, got %d", N, emissions.Load())
	}
	// With O_EXCL semantics the typical outcome is exactly 1 emission,
	// but we accept any positive count to tolerate transient I/O errors
	// (err-toward-emission policy).
}

func TestSessionBannerEmittedOnce_LabelSanitization(t *testing.T) {
	home := withTempHome(t)
	// Label with separators that must be sanitised so they don't
	// punch holes in the path layout.
	label := "weird/label with spaces:and*chars"

	if !SessionBannerEmittedOnce(label) {
		t.Fatalf("first emission of weird label should succeed")
	}
	// The sentinel must land directly under .browzer/session/, NOT a
	// subdirectory created from the unsanitised label.
	entries, err := os.ReadDir(filepath.Join(home, ".browzer", "session"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one sentinel entry, got %d", len(entries))
	}
	if strings.ContainsAny(entries[0].Name(), `/\ *:`) {
		t.Fatalf("sentinel filename should be sanitised: %q", entries[0].Name())
	}
}

func TestSessionBannerEmittedOnce_DifferentSessionsIndependent(t *testing.T) {
	withTempHome(t)
	label := randomLabel(t)

	t.Setenv("CLAUDE_SESSION_ID", "session-A")
	firstA := SessionBannerEmittedOnce(label)
	secondA := SessionBannerEmittedOnce(label)

	t.Setenv("CLAUDE_SESSION_ID", "session-B")
	firstB := SessionBannerEmittedOnce(label)

	if !firstA {
		t.Fatalf("session A first emission should be true")
	}
	if secondA {
		t.Fatalf("session A second emission should be false")
	}
	if !firstB {
		t.Fatalf("session B first emission should be true (independent session)")
	}
}
