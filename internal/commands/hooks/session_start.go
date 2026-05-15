package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/browzeremb/browzer-cli/internal/daemon"
	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// sessionStartStatusTimeout is the wall-clock ceiling for `browzer status --json`.
const sessionStartStatusTimeout = 5 * time.Second

// sessionStartInput models the SessionStart hook envelope fields the guard reads.
type sessionStartInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// sessionStartEnvelope is the outer JSON written to stdout when the guard
// produces an additionalContext payload.
type sessionStartEnvelope struct {
	HookSpecificOutput *sessionStartSpecific `json:"hookSpecificOutput,omitempty"`
}

type sessionStartSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// newSessionStartCommand returns the cobra command for the session-start guard.
func newSessionStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Runs at the start of a Claude Code session",
		RunE:  runSessionStart,
	}
}

// runSessionStart is the cobra RunE. Reads stdin, runs the decide logic,
// writes any envelope, and exits. Never blocks the agent — all error
// paths exit ExitPassthrough.
func runSessionStart(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook session-start: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, out := sessionStartDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	os.Exit(code)
	return nil // unreachable
}

// sessionStartDecide is the testable core of the session-start guard.
// Returns the exit code and, when workspace context is available, a
// non-nil output envelope containing a summary of the workspace state.
func sessionStartDecide(raw []byte) (int, *sessionStartEnvelope) {
	// SessionStart must always fire in a Browzer workspace — no env gate
	// so the agent boots with workspace context injected from the first turn.
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough, nil
	}

	var in sessionStartInput
	// Tolerate an unparseable payload — the guard degrades gracefully.
	_ = json.Unmarshal(raw, &in)

	summary := fetchWorkspaceStatus()
	if summary == "" {
		return internalhooks.ExitPassthrough, nil
	}

	// Best-effort: pre-warm the daemon and register the session so hooks
	// that depend on a warm daemon (rewrite-read, rewrite-bash run-proxy,
	// postuse-run) avoid cold-start cost on their first call. Both calls
	// are fire-and-forget — errors are logged to stderr but do not affect
	// the exit code or the payload returned to Claude Code.
	sessionStartRegisterDaemon(in.SessionID, in.TranscriptPath)

	// Return ExitAllow (0) so Claude Code parses the stdout JSON and
	// injects the additionalContext into the agent's first turn.
	// ExitPassthrough (1) causes the shell wrapper to discard stdout.
	return internalhooks.ExitAllow, &sessionStartEnvelope{
		HookSpecificOutput: &sessionStartSpecific{
			HookEventName:     "SessionStart",
			AdditionalContext: summary,
		},
	}
}

// sessionStartRegisterDaemon pre-warms the Browzer daemon (spawning it if
// absent) and registers the current session. Both operations are
// best-effort: any error is logged to stderr and silently swallowed so
// the caller's exit code and payload are not affected.
func sessionStartRegisterDaemon(sessionID, transcriptPath string) {
	if err := internalhooks.EnsureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook session-start: daemon pre-warm failed: %v\n", err)
	}

	if sessionID == "" {
		return
	}
	params := daemon.SessionRegisterParams{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
	}
	if _, err := internalhooks.DaemonCall("SessionRegister", params); err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook session-start: SessionRegister failed: %v\n", err)
	}
}

// fetchWorkspaceStatus shells out to `browzer status --json` with a 5s
// context timeout and returns a compact human-readable summary of the
// workspace state. Returns "" on any failure — callers treat "" as
// "no context to inject".
func fetchWorkspaceStatus() string {
	ctx, cancel := context.WithTimeout(context.Background(), sessionStartStatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "browzer", "status", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return ""
	}

	raw := bytes.TrimSpace(stdout.Bytes())
	if len(raw) == 0 {
		return ""
	}

	return formatStatusSummary(raw)
}

// formatStatusSummary parses the JSON payload emitted by `browzer status --json`
// and renders a single-line workspace context string suitable for
// additionalContext injection. Unknown fields are tolerated — only the
// well-known keys are extracted.
func formatStatusSummary(data []byte) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		// Non-JSON status output — return a minimal stub so the agent at
		// least knows the workspace check succeeded.
		return "[browzer] Workspace status available."
	}

	// Extract the workspace name / id for the summary line.
	wsName := jsonStringField(obj, "workspaceName", "workspace_name", "name")
	wsID := jsonStringField(obj, "workspaceId", "workspace_id", "id")
	indexedAt := jsonStringField(obj, "indexedAt", "indexed_at", "lastIndexed")

	switch {
	case wsName != "" && indexedAt != "":
		return fmt.Sprintf("[browzer] Workspace: %s (indexed %s)", wsName, indexedAt)
	case wsName != "":
		return fmt.Sprintf("[browzer] Workspace: %s", wsName)
	case wsID != "":
		return fmt.Sprintf("[browzer] Workspace ID: %s", wsID)
	default:
		return "[browzer] Workspace status available."
	}
}

// jsonStringField extracts the first non-empty string value found under
// any of the candidate keys from a raw JSON object map.
func jsonStringField(obj map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if raw, ok := obj[k]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
	}
	return ""
}
