package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// postuseGlobFallbackBytes is the flat estimate used when the workspace
// manifest is unavailable and the Glob was blocked (40 KB).
const postuseGlobFallbackBytes = 40 * 1024

// postuseGlobInput is the PostToolUse(Glob) hook envelope.
type postuseGlobInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Type    string `json:"type"`
		Include string `json:"include"`
	} `json:"tool_input"`
	ToolResponse struct {
		Content string `json:"content"`
		Stdout  string `json:"stdout"`
	} `json:"tool_response"`
	SessionID string `json:"session_id"`
}

// postuseGlobManifest models the per-workspace manifest file written by the
// browzer daemon. Only the `files` map is consumed here.
type postuseGlobManifest struct {
	Files map[string]struct {
		LineCount int `json:"lineCount"`
	} `json:"files"`
}

// newPostuseGlobCommand returns the cobra command for the postuse-glob guard.
func newPostuseGlobCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "postuse-glob",
		Short: "Post-tool-use guard for Glob calls",
		RunE:  runPostuseGlob,
	}
}

// runPostuseGlob is the cobra RunE for the postuse-glob guard.
// It fires telemetry for Glob calls, using counterfactual estimation when
// the Glob was in block-mode. Always exits passthrough.
func runPostuseGlob(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook postuse-glob: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	postuseGlobDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// postuseGlobDecide is the testable core. It fires a fire-and-forget
// telemetry event for Glob calls. Returns true when telemetry was submitted.
func postuseGlobDecide(raw []byte) bool {
	if !internalhooks.GateAllowed("postuse-glob") {
		return false
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return false
	}

	var in postuseGlobInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	if in.ToolName != "Glob" {
		return false
	}

	// Build the config-surface target string and skip telemetry when matched.
	target := internalhooks.ConfigSurfaceTarget(
		in.ToolInput.Path, in.ToolInput.Pattern, in.ToolInput.Glob,
		in.ToolInput.Type, in.ToolInput.Include,
	)
	if internalhooks.ConfigSurfaceRE.MatchString(target) {
		return false
	}

	wsRoot := internalhooks.WorkspaceRootFor("")
	mode := internalhooks.WorkspaceGlobMode(wsRoot)
	pattern := in.ToolInput.Pattern
	if pattern == "" {
		pattern = in.ToolInput.Glob
	}

	content := in.ToolResponse.Content
	if content == "" {
		content = in.ToolResponse.Stdout
	}
	outputBytes := len([]byte(content))

	var savedTokens int
	var estimationMethod string
	var source string

	if mode == "block" {
		cf := postuseGlobCounterfactual(wsRoot, pattern)
		if cf >= 0 {
			savedTokens = cf
			estimationMethod = "counterfactual"
		} else {
			savedTokens = postuseGlobFallbackBytes / 4
			estimationMethod = "estimated"
		}
		source = "hook-glob-blocked"
	} else {
		savedTokens = outputBytes / 4
		estimationMethod = "measured"
		source = "hook-glob-suggested"
	}

	internalhooks.TrackEvent(map[string]any{
		"ts":               "",
		"source":           source,
		"command":          "Glob",
		"inputBytes":       0,
		"outputBytes":      outputBytes,
		"savedTokens":      savedTokens,
		"savingsPct":       0,
		"filterLevel":      mode,
		"filterFailed":     false,
		"execMs":           0,
		"sessionId":        in.SessionID,
		"estimationMethod": estimationMethod,
	})

	return true
}

// postuseGlobCounterfactual estimates saved tokens by scanning the workspace
// manifest for files matching the glob pattern. Returns -1 when the estimate
// cannot be produced (manifest absent or no matching files).
func postuseGlobCounterfactual(wsRoot, pattern string) int {
	if wsRoot == "" || pattern == "" {
		return -1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return -1
	}

	// Workspace ID is derived from the workspace root basename (same
	// derivation as the daemon's manifest_cache.go).
	wsID := filepath.Base(wsRoot)

	manifestPath := filepath.Join(home, ".browzer", "workspaces", wsID, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return -1
	}
	var manifest postuseGlobManifest
	if json.Unmarshal(data, &manifest) != nil {
		return -1
	}
	if manifest.Files == nil {
		return -1
	}

	re := globPatternToRE(pattern)
	// Linear scan over manifest.Files: O(N) MatchString calls where N is the
	// number of indexed files in the workspace. The regex is compiled once per
	// postuseGlobCounterfactual invocation (one compile per hook firing), so
	// the compile cost is amortised across the loop. Expected envelope is
	// ≤10 k files for a typical workspace; this is an acceptable scan depth
	// for a telemetry-only, non-latency-critical code path.
	var totalBytes int
	for relPath, entry := range manifest.Files {
		if !re.MatchString(relPath) {
			continue
		}
		// ~80 bytes per source line (order-of-magnitude estimate).
		totalBytes -= entry.LineCount * 80
	}
	if totalBytes == 0 {
		return -1
	}
	return totalBytes / 4
}

// globPatternToRE converts a simple glob pattern to a regexp.Regexp.
// Supports `*`, `**`, and `?`; escapes all other RE metacharacters.
//
// Performance note: this function compiles a new *regexp.Regexp on every call.
// The caller (postuseGlobCounterfactual) invokes it once per hook firing and
// immediately amortises the compile cost across the O(N) manifest walk that
// follows. Do not cache across invocations — the hook is stateless by design.
func globPatternToRE(pattern string) *regexp.Regexp {
	// Normalise Windows path separators to POSIX before building the regex.
	// Claude Code may emit `\`-separated Glob inputs on Windows agents; the
	// workspace manifest always stores POSIX-canonical (`/`) paths, so a
	// backslash in the pattern would never match.
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			// reMetaChar reports whether c is a regexp metacharacter that must
			// be escaped. The set is ASCII-only; using a switch makes that
			// assumption explicit and avoids an unnecessary rune conversion.
			switch c {
			case '.', '+', '^', '$', '|', '(', ')', '\\', '[', ']', '{', '}':
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
		i++
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		// Fallback: match nothing on an invalid pattern.
		return regexp.MustCompile(`^\x00$`)
	}
	return re
}
