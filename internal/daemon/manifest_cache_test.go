package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManifestCache_LoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	body := `{
	  "workspaceId": "ws_1",
	  "indexedAt": "2026-04-15T10:00:00Z",
	  "files": {
	    "src/foo.ts": {
	      "indexedAt": "2026-04-15T10:00:00Z",
	      "language": "typescript",
	      "lineCount": 80,
	      "symbols": [{"name":"foo","kind":"function","startLine":10,"endLine":25,"signature":"export function foo()","doc":""}],
	      "imports": ["./bar.ts"],
	      "exports": ["foo"]
	    }
	  }
	}`
	if err := os.WriteFile(manifestPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewManifestCache(func(string) string { return manifestPath })
	m, err := c.Get("ws_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := m.Files["src/foo.ts"].Symbols[0].Name; got != "foo" {
		t.Fatalf("Symbols[0].Name = %q, want foo", got)
	}
}

func TestManifestCache_Miss(t *testing.T) {
	c := NewManifestCache(func(string) string { return "/nonexistent/manifest.json" })
	if _, err := c.Get("ws_1"); err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

// TestManifestCacheSingleFlight_RecoversOnENOENT verifies FR-4: a missing
// manifest triggers a single-flight background fetch. N concurrent Gets
// coalesce into ONE fetcher call; after the fetcher completes the next Get
// hits the warmed cache without disk I/O.
func TestManifestCacheSingleFlight_RecoversOnENOENT(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	var calls int32
	fetchStarted := make(chan struct{}, 1)
	fetchUnblock := make(chan struct{})
	fetcher := FetchFn(func(_ context.Context, workspaceID string) (*Manifest, error) {
		atomic.AddInt32(&calls, 1)
		select {
		case fetchStarted <- struct{}{}:
		default:
		}
		<-fetchUnblock
		return &Manifest{
			WorkspaceID: workspaceID,
			IndexedAt:   "2026-05-07T00:00:00Z",
			Files: map[string]ManifestFile{
				"src/foo.ts": {
					IndexedAt: "2026-05-07T00:00:00Z", Language: "typescript", LineCount: 1,
					Symbols: []ManifestSymbol{}, Imports: []string{}, Exports: []string{},
				},
			},
		}, nil
	})
	c := NewManifestCacheWithFetch(func(string) string { return manifestPath }, fetcher)

	// Fire N concurrent Gets — each must error (file missing) but all should
	// coalesce into ONE fetcher call.
	const N = 5
	var wg sync.WaitGroup
	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Get("ws_1")
			if err == nil {
				t.Errorf("Get on missing file: want error, got nil")
			}
		}()
	}
	wg.Wait()

	// Wait for the in-flight fetcher to be observed, then unblock it.
	select {
	case <-fetchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("fetcher never started within 2s")
	}
	close(fetchUnblock)

	// Allow the singleflight goroutine to persist + warm the cache.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(manifestPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("fetcher calls = %d, want 1 (singleflight dedupe)", got)
	}

	// Next Get must hit the warmed cache.
	m, err := c.Get("ws_1")
	if err != nil {
		t.Fatalf("Get after recovery: %v", err)
	}
	if m == nil || m.WorkspaceID != "ws_1" {
		t.Fatalf("warmed manifest unexpected: %#v", m)
	}
}

// TestManifestCacheSingleFlight_NilFetcherPreservesENOENT verifies that the
// classic constructor (no fetcher) keeps the legacy behaviour: ENOENT
// returns immediately, no recovery is attempted.
func TestManifestCacheSingleFlight_NilFetcherPreservesENOENT(t *testing.T) {
	c := NewManifestCache(func(string) string { return "/definitely/nonexistent/manifest.json" })
	_, err := c.Get("ws_x")
	if err == nil {
		t.Fatal("expected ENOENT-wrapped error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error chain missing os.ErrNotExist: %v", err)
	}
}

// TestManifestCacheSingleFlight_FetcherTimeoutCancels verifies that a
// fetcher slower than the configured fetchTimeout is cancelled via context
// AND that the cancellation observably propagates to the fetcher (it
// returns context.DeadlineExceeded), AND no on-disk manifest gets written.
//
// Driven via NewManifestCacheWithFetchOptions{FetchTimeout: 100ms} to
// keep the test CI-fast (~100ms) without sleeping the production 5s.
// Closes F-013 (CODE_REVIEW deferral): the previous version only counted
// fetcher invocations and never asserted the timeout-induced cancellation
// path actually fired or that the on-disk manifest stayed untouched.
func TestManifestCacheSingleFlight_FetcherTimeoutCancels(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")

	var observedErr atomic.Value // stores error
	var calls atomic.Int32
	fetcher := FetchFn(func(ctx context.Context, _ string) (*Manifest, error) {
		calls.Add(1)
		select {
		case <-ctx.Done():
			observedErr.Store(ctx.Err())
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			// Should never be reached when timeout is 100ms — guard against
			// a regression where timeout is no longer honoured.
			t.Errorf("fetcher returned its 10s manifest before context deadline fired")
			return &Manifest{WorkspaceID: "ws_timeout", IndexedAt: "2026-05-07T00:00:00Z"}, nil
		}
	})

	done := make(chan struct{})
	c := NewManifestCacheWithFetchOptions(
		func(string) string { return manifestPath },
		fetcher,
		// FetchTimeout is package-private but accepted via the in-package
		// options struct. We also smuggle the done channel via the
		// unexported field — only legal because this test lives in
		// `package daemon` (white-box).
		ManifestCacheOptions{FetchTimeout: 100 * time.Millisecond, fetchDone: done},
	)

	// Trigger ENOENT → kicks singleflight refetch. Caller gets the
	// not-found error immediately; the fetcher races in the background.
	_, err := c.Get("ws_timeout")
	if err == nil {
		t.Fatal("Get must return the original ENOENT-wrapped error; recovery is async")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error chain missing os.ErrNotExist: %v", err)
	}

	// Wait deterministically for the singleflight goroutine to finish
	// (signalled by closing `done` from the kickRefetch defer block).
	// Generous bound — 2s is 20× the configured 100ms timeout but still
	// well under any reasonable CI budget.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fetcher goroutine did not exit within 2s; timeout cancellation did not fire")
	}

	// Invariant 1: fetcher was called exactly once.
	if got := calls.Load(); got != 1 {
		t.Fatalf("fetcher invocations = %d, want 1", got)
	}

	// Invariant 2: fetcher observed context.DeadlineExceeded — proves the
	// cancellation path actually propagated to the FetchFn body.
	got, _ := observedErr.Load().(error)
	if got == nil {
		t.Fatal("fetcher did not observe a ctx.Err()")
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("ctx.Err() = %v, want context.DeadlineExceeded", got)
	}

	// Invariant 3: no on-disk manifest was written. The atomic
	// tmp+rename block must be skipped when the fetcher errors.
	if _, statErr := os.Stat(manifestPath); !os.IsNotExist(statErr) {
		t.Fatalf("manifest file exists on disk after timeout (stat err = %v); expected ENOENT", statErr)
	}
	if _, statErr := os.Stat(manifestPath + ".tmp"); !os.IsNotExist(statErr) {
		t.Fatalf("manifest .tmp file exists on disk after timeout; expected ENOENT")
	}

	// Invariant 4: in-memory cache is empty (no partial manifest).
	c.mu.RLock()
	_, ok := c.cache["ws_timeout"]
	c.mu.RUnlock()
	if ok {
		t.Fatal("cache must not contain a partial manifest after fetcher timeout")
	}
}

func TestManifestCache_FileForPath(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	_ = os.WriteFile(manifestPath, []byte(`{"workspaceId":"ws_1","indexedAt":"2026-04-15T10:00:00Z","files":{"src/foo.ts":{"indexedAt":"2026-04-15T10:00:00Z","language":"typescript","lineCount":1,"symbols":[],"imports":[],"exports":[]}}}`), 0o600)
	c := NewManifestCache(func(string) string { return manifestPath })
	if _, err := c.Get("ws_1"); err != nil {
		t.Fatal(err)
	}
	mf, ok := c.FileForPath("ws_1", "src/foo.ts")
	if !ok {
		t.Fatal("FileForPath should hit")
	}
	if mf.Language != "typescript" {
		t.Fatalf("language = %q", mf.Language)
	}
}
