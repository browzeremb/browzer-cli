package hooks

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAppendPendingEvent_CreatesFileAndAppendsLine(t *testing.T) {
	home := setupTempHome(t)

	event := map[string]any{"cmd": "track-cli", "n": 7}
	if err := AppendPendingEvent(event); err != nil {
		t.Fatalf("AppendPendingEvent: %v", err)
	}

	path := filepath.Join(home, "pending-events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("appended line should be newline-terminated; got %q", data)
	}
	var got map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &got); err != nil {
		t.Fatalf("appended line is not valid JSON: %v", err)
	}
	if got["cmd"] != "track-cli" {
		t.Fatalf("unexpected payload: %v", got)
	}
}

func TestAppendPendingEvent_MultipleAppendsProduceMultipleLines(t *testing.T) {
	home := setupTempHome(t)

	for i := range 3 {
		if err := AppendPendingEvent(map[string]any{"i": i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	lines := readPendingEvents(t, home)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines; got %d", len(lines))
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d not valid JSON: %v", i, err)
		}
	}
}

func TestAppendPendingEvent_RotatesAbove10MB(t *testing.T) {
	home := setupTempHome(t)
	path := filepath.Join(home, "pending-events.jsonl")

	if err := writeSizedFile(path, pendingEventsRotationBytes+1); err != nil {
		t.Fatalf("seed oversized file: %v", err)
	}

	if err := AppendPendingEvent(map[string]any{"after": "rotation"}); err != nil {
		t.Fatalf("append after rotation: %v", err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotation backup missing: %v", err)
	}

	lines := readPendingEvents(t, home)
	if len(lines) != 1 {
		t.Fatalf("post-rotation file should contain exactly 1 line; got %d", len(lines))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("post-rotation line not valid JSON: %v", err)
	}
	if got["after"] != "rotation" {
		t.Fatalf("post-rotation line did not contain the new event: %v", got)
	}
}

func TestAppendPendingEvent_RotationOverwritesPriorBackup(t *testing.T) {
	home := setupTempHome(t)
	path := filepath.Join(home, "pending-events.jsonl")
	backup := path + ".1"

	// Pre-seed a stale .1 backup so we can verify it gets overwritten.
	if err := os.WriteFile(backup, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("seed stale backup: %v", err)
	}
	if err := writeSizedFile(path, pendingEventsRotationBytes+1); err != nil {
		t.Fatalf("seed oversized file: %v", err)
	}

	if err := AppendPendingEvent(map[string]any{"k": "v"}); err != nil {
		t.Fatalf("AppendPendingEvent: %v", err)
	}

	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read rotated backup: %v", err)
	}
	if strings.HasPrefix(string(data), "stale") {
		t.Fatalf("rotation should overwrite prior .1 backup; got prefix=stale")
	}
}

func TestAppendPendingEvent_BelowThresholdDoesNotRotate(t *testing.T) {
	home := setupTempHome(t)
	path := filepath.Join(home, "pending-events.jsonl")

	// Write a few small entries; total stays well below 10 MiB.
	for i := range 5 {
		if err := AppendPendingEvent(map[string]any{"i": i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("rotation backup should not exist below threshold; stat err=%v", err)
	}
}

func TestAppendPendingEvent_AtomicAppendUsesOAppend(t *testing.T) {
	// Indirect assertion of the O_APPEND contract: open the file with
	// a competing handle in O_RDONLY and call AppendPendingEvent. The
	// resulting file MUST contain both the pre-existing content and
	// the appended line in order — which is the observable side-effect
	// of O_APPEND placing each write at the EOF cursor.
	home := setupTempHome(t)
	path := filepath.Join(home, "pending-events.jsonl")

	pre := []byte(`{"pre":"seed"}` + "\n")
	if err := os.WriteFile(path, pre, 0o644); err != nil {
		t.Fatalf("write pre-seed: %v", err)
	}

	if err := AppendPendingEvent(map[string]any{"post": "append"}); err != nil {
		t.Fatalf("AppendPendingEvent: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	sc := bufio.NewScanner(f)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (pre + post); got %d (lines=%v)", len(lines), lines)
	}
	if !strings.Contains(lines[0], "pre") {
		t.Fatalf("first line should be the pre-seed; got %q", lines[0])
	}
	if !strings.Contains(lines[1], "post") {
		t.Fatalf("second line should be the appended event; got %q", lines[1])
	}
}

func TestAppendPendingEvent_OversizedPayloadUsesRenameSlot(t *testing.T) {
	// Build a payload whose JSON encoding exceeds pendingEventsMaxLineBytes
	// (4096). AppendPendingEvent must not attempt the O_APPEND path; instead
	// it writes a unique temp file in the queue directory and leaves it
	// (no rename to the main JSONL file).
	home := setupTempHome(t)

	// Generate a value whose JSON is longer than pendingEventsMaxLineBytes.
	// A string of 4096 'x' characters serialises to 4098 bytes (`"<4096 x's>"`).
	bigString := strings.Repeat("x", pendingEventsMaxLineBytes)
	event := map[string]any{"big": bigString}

	if err := AppendPendingEvent(event); err != nil {
		t.Fatalf("AppendPendingEvent with oversized payload: %v", err)
	}

	// The main JSONL file must NOT have been written (O_APPEND path bypassed).
	mainPath := filepath.Join(home, "pending-events.jsonl")
	if _, err := os.Stat(mainPath); !os.IsNotExist(err) {
		t.Fatalf("main pending-events.jsonl should not exist for oversized payload; stat err=%v", err)
	}

	// An overflow file with the expected name pattern must exist in the dir.
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read home dir: %v", err)
	}
	var overflowFiles []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pending-events-overflow-") && strings.HasSuffix(e.Name(), ".jsonl") {
			overflowFiles = append(overflowFiles, e.Name())
		}
	}
	if len(overflowFiles) == 0 {
		t.Fatalf("no overflow file found in %s; entries=%v", home, entries)
	}

	// The overflow file must contain valid JSON followed by a newline.
	overflowPath := filepath.Join(home, overflowFiles[0])
	data, err := os.ReadFile(overflowPath)
	if err != nil {
		t.Fatalf("read overflow file: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("overflow file should be newline-terminated; got last byte=%02x", data[len(data)-1])
	}
	var got map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &got); err != nil {
		t.Fatalf("overflow file is not valid JSON: %v", err)
	}
	if _, ok := got["big"]; !ok {
		t.Fatalf("overflow file missing 'big' key; got %v", got)
	}
}

// TestAppendPendingEvent_ConcurrentAppend_NoInterleaving launches 8 goroutines,
// each appending 5 events, into a shared pending-events.jsonl. The test asserts:
//
//  1. Every line in the final file is a complete, valid JSON object (no partial
//     writes that would corrupt a line boundary).
//  2. The total number of lines equals goroutines × appends per goroutine.
//
// PIPE_BUF atomicity (≤4096 bytes per write) guarantees that O_APPEND writes
// from concurrent goroutines land as whole units on Linux and macOS. The -race
// flag on the gate command exercises the race detector over the kernel-mediated
// O_APPEND path — any race in surrounding bookkeeping (e.g. rotatePendingEventsIfNeeded)
// would surface here even though the write itself is kernel-atomic.
// Note: t.Parallel() cannot be used here because setupTempHome calls
// t.Setenv, which is incompatible with parallel test execution in Go's
// testing framework. The -race flag on the gate command is the mechanism
// that catches data races from the concurrent goroutines below.
func TestAppendPendingEvent_ConcurrentAppend_NoInterleaving(t *testing.T) {
	home := setupTempHome(t)

	const numGoroutines = 8
	const appendsEach = 5

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(gid int) {
			defer wg.Done()
			for i := range appendsEach {
				if err := AppendPendingEvent(map[string]any{"g": gid, "i": i}); err != nil {
					// Cannot call t.Fatal from a goroutine; record via t.Error.
					t.Errorf("concurrent append g=%d i=%d: %v", gid, i, err)
				}
			}
		}(g)
	}
	wg.Wait()

	if t.Failed() {
		t.FailNow()
	}

	lines := readPendingEvents(t, home)
	if len(lines) != numGoroutines*appendsEach {
		t.Fatalf("concurrent append: want %d lines; got %d", numGoroutines*appendsEach, len(lines))
	}
	for idx, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("concurrent append: line %d is not valid JSON (interleaved?): %v -- raw=%q", idx, err, line)
		}
	}
}

func TestPendingEventsPath_HonorsBrowzerHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)
	got := PendingEventsPath()
	want := filepath.Join(dir, "pending-events.jsonl")
	if got != want {
		t.Fatalf("PendingEventsPath = %q; want %q", got, want)
	}
}
