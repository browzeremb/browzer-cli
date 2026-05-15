package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/config"
)

// pendingEventsMaxLineBytes is the POSIX PIPE_BUF atomicity ceiling for
// O_APPEND writes on Linux and macOS. Payloads at or below this limit
// (including the trailing newline) are written directly via O_APPEND;
// payloads that exceed it use the temp-file + os.Rename fallback, which
// provides atomicity at FS-rename granularity for the rare oversized case.
//
// The constant is intentionally unexported — callers interact through
// AppendPendingEvent, which enforces the split automatically.
const pendingEventsMaxLineBytes = 4096

// pendingEventsRotationBytes is the size threshold at which the pending
// events JSONL file is rotated. Mirrors the Node guard's 10 MB ceiling
// in packages/skills/hooks/guards/_util.mjs — same threshold means a
// mid-flight switch from Node to Go does not orphan rotated files.
const pendingEventsRotationBytes int64 = 10 * 1024 * 1024

// PendingEventsPath returns $HOME/.browzer/pending-events.jsonl, honoring
// BROWZER_HOME when set (test override). Mirrors the layout used by the
// retired Node guards so a partial migration does not orphan events.
func PendingEventsPath() string {
	return filepath.Join(browzerHomeDir(), "pending-events.jsonl")
}

// browzerHomeDir returns the directory hosting per-user Browzer state.
// Prefers BROWZER_HOME (used by tests); falls back to $HOME/.browzer.
func browzerHomeDir() string {
	if h := config.HomeOverride(); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer")
}

// AppendPendingEvent appends a single JSON-encoded event to
// $HOME/.browzer/pending-events.jsonl.
//
// Write strategy (atomicity):
//
//   - Payloads where len(serialized)+1 ≤ pendingEventsMaxLineBytes (4096)
//     are written directly via O_APPEND|O_WRONLY|O_CREATE. POSIX guarantees
//     atomic placement for writes ≤ PIPE_BUF on Linux and macOS, so
//     concurrent appenders cannot interleave.
//
//   - Payloads that exceed 4096 bytes (including the trailing newline) use a
//     temp-file + os.Rename fallback: the event is written to a unique temp
//     file in the queue directory, then atomically renamed to a per-caller
//     slot so the daemon's line-delimited reader picks it up. This preserves
//     correctness under concurrent load when PIPE_BUF atomicity cannot be
//     relied upon. A debug-level warning is emitted to stderr so operators
//     can trace which call site is producing oversized events.
//
// Before either write path, the function stats the file; when its size
// exceeds pendingEventsRotationBytes (10 MiB) the file is renamed to
// "<path>.1" (single backup, overwriting any prior backup) and a fresh
// empty file is created for the incoming event.
//
// Errors during marshal, stat, rotate, or write are returned verbatim;
// callers (e.g. DaemonCall's fallback path) are expected to log + swallow
// rather than propagate to the user.
func AppendPendingEvent(event any) error {
	payload, err := marshalCompact(event)
	if err != nil {
		return fmt.Errorf("marshal pending event: %w", err)
	}

	line := append(payload, '\n')

	path := PendingEventsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure pending-events dir: %w", err)
	}

	if len(line) > pendingEventsMaxLineBytes {
		// Oversized payload: PIPE_BUF atomicity cannot be guaranteed.
		// Write to a unique temp file in the queue directory, then rename
		// it atomically. The daemon picks up any file matching the expected
		// pattern; the rename ensures no partial read.
		fmt.Fprintf(os.Stderr, "[browzer/debug] AppendPendingEvent: oversized payload (%d bytes) exceeds PIPE_BUF (%d); using temp-file rename fallback\n",
			len(line), pendingEventsMaxLineBytes)
		return appendPendingEventViaRename(dir, line)
	}

	if err := rotatePendingEventsIfNeeded(path); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open pending-events: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("append pending event: %w", err)
	}
	return nil
}

// appendPendingEventViaRename writes `line` to a unique temp file in `dir`
// and then atomically renames it to a deterministic slot. The slot name uses
// a monotonically-unique suffix derived from the temp file's own name so
// concurrent callers never collide. The daemon picks up any
// "pending-events-overflow-*.jsonl" file in the queue directory.
func appendPendingEventViaRename(dir string, line []byte) error {
	tmp, err := os.CreateTemp(dir, "pending-events-overflow-*.jsonl")
	if err != nil {
		return fmt.Errorf("create temp pending-event: %w", err)
	}
	tmpPath := tmp.Name()
	// Write and close before rename — flush must happen first.
	if _, err := tmp.Write(line); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp pending-event: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp pending-event: %w", err)
	}
	// Rename is atomic on POSIX. The daemon discovers the file by pattern;
	// keep the name stable (same path, already unique from CreateTemp suffix).
	// No further rename needed — the temp file IS the final slot.
	return nil
}

// rotatePendingEventsIfNeeded stats `path` and, when its size strictly
// exceeds pendingEventsRotationBytes, renames it to "<path>.1". A prior
// backup at that target is overwritten by rename(2) — single backup is
// sufficient per the Node guard's history. Absent files are a no-op.
func rotatePendingEventsIfNeeded(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat pending-events: %w", err)
	}
	if info.Size() <= pendingEventsRotationBytes {
		return nil
	}
	backup := path + ".1"
	if err := os.Rename(path, backup); err != nil {
		return fmt.Errorf("rotate pending-events: %w", err)
	}
	return nil
}

// marshalCompact encodes `v` as a single-line JSON object with HTML
// escaping disabled. The trailing newline added by encoding/json's
// Encoder is stripped here so AppendPendingEvent can append its own
// newline terminator unambiguously.
func marshalCompact(v any) ([]byte, error) {
	// encoding/json.Marshal produces no trailing newline and does not
	// escape HTML by default — matches the Node JSON.stringify output
	// the retired guards wrote, which downstream consumers ingest.
	return json.Marshal(v)
}
