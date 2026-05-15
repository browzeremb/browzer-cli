package hooks

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/daemon"
)

// fakeRunner captures Start calls without forking a real process so
// tests can assert on EnsureDaemon's spawn arguments.
type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (f *fakeRunner) Start(name string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	return f.err
}

func (f *fakeRunner) lastCall() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// setupTempHome redirects BROWZER_HOME at a freshly-created temp dir,
// guarantees a hooks/.browzer subdirectory exists, and returns the
// path. The cleanup is registered with t.Cleanup so callers do not
// need to defer.
func setupTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	browzerDir := filepath.Join(dir, "browzer-home")
	if err := os.MkdirAll(browzerDir, 0o755); err != nil {
		t.Fatalf("setup temp home: %v", err)
	}
	t.Setenv("BROWZER_HOME", browzerDir)
	// Force the daemon client to dial a socket path that cannot exist
	// — no listener has been bound there, so the dial fails fast.
	t.Setenv("BROWZER_DAEMON_SOCKET", filepath.Join(dir, "no-such-daemon.sock"))
	return browzerDir
}

func TestDaemonCall_Fallback_AppendsAndRespawns(t *testing.T) {
	home := setupTempHome(t)
	fake := &fakeRunner{}
	restore := WithCommandRunner(fake)
	t.Cleanup(restore)

	payload := map[string]any{"ts": "2026-05-14T12:00:00Z", "command": "test"}
	_, err := DaemonCall("Track", payload)
	if err == nil {
		t.Fatalf("DaemonCall against a missing socket should return an error")
	}

	// Assert: pending-events.jsonl has exactly one line containing our payload.
	lines := readPendingEvents(t, home)
	if len(lines) != 1 {
		t.Fatalf("expected 1 pending event line; got %d (lines=%v)", len(lines), lines)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("pending event line is not valid JSON: %v (line=%q)", err, lines[0])
	}
	if got["method"] != "Track" {
		t.Fatalf("pending event method = %v; want Track", got["method"])
	}

	// Assert: EnsureDaemon was invoked with the expected detached spawn args.
	if fake.callCount() != 1 {
		t.Fatalf("expected exactly 1 EnsureDaemon spawn; got %d", fake.callCount())
	}
	got2 := fake.lastCall()
	want := []string{"browzer", "daemon", "start", "--background"}
	if len(got2) != len(want) {
		t.Fatalf("spawn args length = %d; want %d (got=%v)", len(got2), len(want), got2)
	}
	for i := range want {
		if got2[i] != want[i] {
			t.Fatalf("spawn args[%d] = %q; want %q (full=%v)", i, got2[i], want[i], got2)
		}
	}
}

func TestDaemonCall_Fallback_RotatesAt10MB(t *testing.T) {
	home := setupTempHome(t)
	restore := WithCommandRunner(&fakeRunner{})
	t.Cleanup(restore)

	// Pre-populate the pending-events file with > 10 MiB so the next
	// AppendPendingEvent triggers rotation.
	path := filepath.Join(home, "pending-events.jsonl")
	if err := writeSizedFile(path, pendingEventsRotationBytes+1); err != nil {
		t.Fatalf("seed oversized pending-events file: %v", err)
	}

	payload := map[string]any{"command": "rotation-trigger"}
	_, _ = DaemonCall("Track", payload)

	// Assert: original file was rotated to .1 (single backup).
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup at %s.1; stat err=%v", path, err)
	}

	// Assert: fresh file contains exactly the new event line.
	lines := readPendingEvents(t, home)
	if len(lines) != 1 {
		t.Fatalf("rotated file should contain exactly 1 line; got %d", len(lines))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("rotated file line is not valid JSON: %v", err)
	}
	params, _ := got["params"].(map[string]any)
	if params == nil || params["command"] != "rotation-trigger" {
		t.Fatalf("rotated file does not contain the new event; got %v", got)
	}
}

func TestDaemonCall_EmptyMethodRejected(t *testing.T) {
	setupTempHome(t)
	if _, err := DaemonCall("", nil); err == nil {
		t.Fatalf("DaemonCall with empty method should return an error")
	}
}

func TestDaemonCall_UnsupportedMethodReturnsError(t *testing.T) {
	setupTempHome(t)
	restore := WithCommandRunner(&fakeRunner{})
	t.Cleanup(restore)
	_, err := DaemonCall("NotARealMethod", map[string]any{"a": 1})
	if err == nil {
		t.Fatalf("unsupported method should return an error")
	}
	if !strings.Contains(err.Error(), "not wired") && !strings.Contains(err.Error(), "daemon call") {
		t.Fatalf("unexpected error shape: %v", err)
	}
}

// TestDaemonCall_TypedTrackParams_ZeroCoerceMarshal pins FR-04 / AC-04:
// when DaemonCall receives a typed daemon.TrackParams, the coerce layer
// must take the type-assertion fast path and skip json.Marshal entirely.
// The probe marshal previously at the top of DaemonCall is gone, so the
// counter must NOT increment on this path.
func TestDaemonCall_TypedTrackParams_ZeroCoerceMarshal(t *testing.T) {
	setupTempHome(t)
	restore := WithCommandRunner(&fakeRunner{})
	t.Cleanup(restore)
	ResetEnsureDaemonGuard()

	before := CoerceMarshalCount()
	typed := daemon.TrackParams{
		TS:      "2026-05-15T00:00:00Z",
		Source:  "test",
		Command: "test",
	}
	// Daemon socket is missing (setupTempHome wires a no-such path),
	// so DaemonCall falls back. The coerce layer still runs before the
	// dial attempt.
	_, _ = DaemonCall("Track", typed)
	after := CoerceMarshalCount()
	if delta := after - before; delta != 0 {
		t.Fatalf("typed daemon.TrackParams must skip the coerce marshal; counter delta = %d, want 0", delta)
	}
}

// TestDaemonCall_MapTrackParams_SingleCoerceMarshal pins the
// complementary half of FR-04: map / untyped payloads round-trip through
// coerce exactly once. Pre-refactor this path paid TWO marshals (the
// probe at the top of DaemonCall plus the coerce round-trip); post-
// refactor it pays one.
func TestDaemonCall_MapTrackParams_SingleCoerceMarshal(t *testing.T) {
	setupTempHome(t)
	restore := WithCommandRunner(&fakeRunner{})
	t.Cleanup(restore)
	ResetEnsureDaemonGuard()

	before := CoerceMarshalCount()
	payload := map[string]any{
		"ts":      "2026-05-15T00:00:00Z",
		"source":  "test",
		"command": "test",
	}
	_, _ = DaemonCall("Track", payload)
	after := CoerceMarshalCount()
	if delta := after - before; delta != 1 {
		t.Fatalf("map payload must round-trip through coerce exactly once; counter delta = %d, want 1", delta)
	}
}

func TestEnsureDaemon_IdempotentPerProcess(t *testing.T) {
	setupTempHome(t)
	fake := &fakeRunner{}
	restore := WithCommandRunner(fake)
	t.Cleanup(restore)

	// First call spawns.
	if err := EnsureDaemon(); err != nil {
		t.Fatalf("first EnsureDaemon: %v", err)
	}
	// Second call is a no-op.
	if err := EnsureDaemon(); err != nil {
		t.Fatalf("second EnsureDaemon: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("EnsureDaemon should be idempotent; got %d spawns", fake.callCount())
	}

	// After explicit reset (test seam), a fresh spawn is allowed.
	ResetEnsureDaemonGuard()
	if err := EnsureDaemon(); err != nil {
		t.Fatalf("third EnsureDaemon after reset: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("after reset, EnsureDaemon should spawn again; got %d total", fake.callCount())
	}
}

func TestEnsureDaemon_SpawnArgsAreDetachable(t *testing.T) {
	setupTempHome(t)
	fake := &fakeRunner{}
	restore := WithCommandRunner(fake)
	t.Cleanup(restore)

	if err := EnsureDaemon(); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
	call := fake.lastCall()
	if call == nil {
		t.Fatalf("EnsureDaemon did not invoke runner.Start")
	}
	want := []string{"browzer", "daemon", "start", "--background"}
	if len(call) != len(want) {
		t.Fatalf("Start args = %v; want %v", call, want)
	}
	for i := range want {
		if call[i] != want[i] {
			t.Fatalf("Start args[%d] = %q; want %q", i, call[i], want[i])
		}
	}
}

func TestEnsureDaemon_ConcurrentCallsSpawnAtMostOnce(t *testing.T) {
	// Spawn 10 goroutines each calling EnsureDaemon concurrently.
	// Only the first goroutine to win the intra-process atomic.Bool
	// CAS (and the cross-process AcquireLock) should issue a Start;
	// the remaining goroutines must return nil without forking.
	setupTempHome(t)
	fake := &fakeRunner{}
	restore := WithCommandRunner(fake)
	t.Cleanup(restore)

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = EnsureDaemon()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: EnsureDaemon returned error: %v", i, err)
		}
	}
	if count := fake.callCount(); count > 1 {
		t.Fatalf("concurrent EnsureDaemon calls produced %d spawns; want ≤1", count)
	}
}

func TestRealCommandRunner_SetsDetachSysProcAttr(t *testing.T) {
	// We don't actually want to fork `browzer` in unit tests, so this
	// test just exercises the helper that mutates *exec.Cmd. The Unix
	// build of ApplyDetachSysProcAttr sets Setpgid=true; the Windows
	// build is a no-op. We only assert the Unix expectation when the
	// helper actually attaches a SysProcAttr, otherwise we skip.
	cmd := newDummyExecCmd()
	ApplyDetachSysProcAttr(cmd)
	assertDetachSysProcAttr(t, cmd)
}

// readPendingEvents reads every line of $home/pending-events.jsonl,
// trimming trailing empties so callers can assert on len(lines).
func readPendingEvents(t *testing.T, home string) []string {
	t.Helper()
	path := filepath.Join(home, "pending-events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open pending-events: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	var lines []string
	sc := bufio.NewScanner(f)
	// Tolerate large lines from the rotation-test seed payload, though
	// real events are tiny.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan pending-events: %v", err)
	}
	return lines
}

// writeSizedFile creates a file at `path` with `size` bytes of ASCII
// content laid out as plausible JSONL (each line is a tiny JSON object
// terminated by '\n'). Uses a buffered writer so the 10 MiB cost stays
// under a second on modern hardware.
func writeSizedFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()

	// 32-byte lines: 31 bytes of payload + '\n'.
	const filler = `{"old":"event-padding-payload"}` + "\n"
	if len(filler) != 32 {
		return errInternalPadding
	}
	written := int64(0)
	for written < size {
		n, err := w.WriteString(filler)
		if err != nil {
			return err
		}
		written += int64(n)
	}
	return nil
}

// errInternalPadding fires when the test's filler line size assumption
// drifts — guards against silent test-only refactors.
var errInternalPadding = newPaddingErr()

func newPaddingErr() error {
	return &paddingErr{}
}

type paddingErr struct{}

func (*paddingErr) Error() string {
	return "test padding string length drifted from 32 bytes"
}
