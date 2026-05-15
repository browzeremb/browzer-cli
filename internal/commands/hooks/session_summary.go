package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"time"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// sessionSummaryTimeout is the wall-clock ceiling for `browzer gain --adoption --json`.
// Matches the 200 ms used by the JS guard so the Stop event is never delayed.
const sessionSummaryTimeout = 200 * time.Millisecond

// sessionSummaryInput models the Stop hook envelope fields the guard reads.
type sessionSummaryInput struct {
	StopHookActive bool   `json:"stop_hook_active"`
	SessionID      string `json:"session_id"`
}

// gainReport mirrors the JSON shape emitted by `browzer gain --adoption --json`.
// Snake-case aliases are tolerated (see parseGainReport).
type gainReport struct {
	// savedTotal is the total saved tokens for the period.
	SavedTotal  *int64      `json:"savedTotal"`
	WastedTotal *int64      `json:"wastedTotal"`
	// Adoption ratio (savedTotal/wastedTotal); null when wastedTotal=0.
	Adoption    *float64    `json:"adoption"`
	TopWasted   *topWasted  `json:"topWasted"`
}

// topWasted captures the top wasted source entry from the gain report.
// Only canonical camelCase JSON names are declared here; snake_case aliases
// (e.g. "saved_tokens") are resolved by rawTopWasted via a supplemental
// raw-map pass so the struct stays unambiguous.
type topWasted struct {
	Source      string `json:"source"`
	Command     string `json:"command"`
	Name        string `json:"name"`
	SavedTokens *int64 `json:"savedTokens"`
	Tokens      *int64 `json:"tokens"`
}

// sessionSummaryOutput is the JSON envelope written to stdout on the Stop event.
// Claude Code Stop hooks read additionalContext from the top-level field
// (not nested inside hookSpecificOutput) per the observed JS behaviour.
type sessionSummaryOutput struct {
	AdditionalContext string `json:"additionalContext"`
}

// gainOutputFn is the test seam for the gain subprocess. In production it
// runs `browzer gain --adoption --json --since 1h` with sessionSummaryTimeout.
// Tests replace it via WithGainOutput to exercise the parsing / formatting path
// without a real binary.
var gainOutputFn = func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionSummaryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "browzer", "gain", "--adoption", "--json", "--since", "1h")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// WithGainOutput swaps the package-level gain-subprocess seam and returns a
// restore closure that reverts it. Test-only — never call from production code.
func WithGainOutput(fn func() ([]byte, error)) (restore func()) {
	prev := gainOutputFn
	gainOutputFn = fn
	return func() { gainOutputFn = prev }
}

// newSessionSummaryCommand returns the cobra command for the session-summary guard.
func newSessionSummaryCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "session-summary",
		Short: "Runs when a session summary is produced",
		RunE:  runSessionSummary,
	}
}

// runSessionSummary is the cobra RunE. Reads stdin, runs the decide logic,
// writes any envelope to stdout, and always exits 0 — never blocks the agent.
func runSessionSummary(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook session-summary: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	out := sessionSummaryDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	// Stop hooks always exit 0 (ExitAllow) so Claude Code continues normally.
	os.Exit(internalhooks.ExitAllow)
	return nil // unreachable
}

// sessionSummaryDecide is the testable core. Returns the output envelope to
// write to stdout, or nil when nothing should be emitted.
func sessionSummaryDecide(raw []byte) *sessionSummaryOutput {
	if !internalhooks.IsInBrowzerWorkspace("") {
		return nil
	}

	var in sessionSummaryInput
	if err := json.Unmarshal(raw, &in); err == nil && in.StopHookActive {
		// Guard against re-triggering the Stop hook.
		return nil
	}

	gainRaw, err := gainOutputFn()
	if err != nil {
		return nil
	}

	trimmed := bytes.TrimSpace(gainRaw)
	if len(trimmed) == 0 {
		return nil
	}

	report, ok := parseGainReport(trimmed)
	if !ok {
		return &sessionSummaryOutput{AdditionalContext: "[browzer] Session summary unavailable."}
	}

	return &sessionSummaryOutput{AdditionalContext: formatGainSummary(report)}
}

// parseGainReport parses the JSON gain report, tolerating both camelCase and
// snake_case field names via a loose two-pass approach: try the typed struct
// first, then fall back to a raw map for snake_case aliases.
func parseGainReport(data []byte) (*gainReport, bool) {
	var r gainReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, false
	}

	// If the typed parse missed some fields (snake_case aliases), supplement
	// from the raw map.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if r.SavedTotal == nil {
			if v := rawInt64(raw, "saved_total", "saved_tokens"); v != nil {
				r.SavedTotal = v
			}
		}
		if r.WastedTotal == nil {
			if v := rawInt64(raw, "wasted_total"); v != nil {
				r.WastedTotal = v
			}
		}
		if r.Adoption == nil {
			if v := rawFloat64(raw, "adoption_multiplier", "adoptionMultiplier"); v != nil {
				r.Adoption = v
			}
		}
		if r.TopWasted == nil {
			if tw := rawTopWasted(raw, "top_wasted"); tw != nil {
				r.TopWasted = tw
			}
		}
	}

	if r.SavedTotal == nil && r.WastedTotal == nil {
		return nil, false
	}
	return &r, true
}

// formatGainSummary renders the gain report into the compact one-line summary
// string emitted as additionalContext. Matches the JS format exactly.
func formatGainSummary(r *gainReport) string {
	savedK := int64(0)
	if r.SavedTotal != nil {
		savedK = roundToK(*r.SavedTotal)
	}

	adoptionPart := ""
	if r.Adoption != nil && !math.IsInf(*r.Adoption, 0) && !math.IsNaN(*r.Adoption) {
		adoptionPart = fmt.Sprintf(" (adoption %.1fx)", *r.Adoption)
	}

	wastedPart := ""
	if r.TopWasted != nil {
		src := topWastedSource(r.TopWasted)
		tok := topWastedTokens(r.TopWasted)
		if src != "" && tok >= 0 {
			wastedPart = fmt.Sprintf(" Top wasted: %s (%dk).", src, roundToK(tok))
		}
	}

	return fmt.Sprintf("[browzer] Session saved %dk tokens%s.%s", savedK, adoptionPart, wastedPart)
}

// roundToK rounds a token count to the nearest thousand.
func roundToK(n int64) int64 {
	return int64(math.Round(float64(n) / 1000))
}

// topWastedSource returns the source label from a topWasted entry,
// preferring source > command > name.
func topWastedSource(tw *topWasted) string {
	if tw.Source != "" {
		return tw.Source
	}
	if tw.Command != "" {
		return tw.Command
	}
	return tw.Name
}

// topWastedTokens returns the absolute magnitude of the token count from
// a topWasted entry. Returns -1 when no token field is present.
// SavedTokens already absorbs the snake_case "saved_tokens" alias via the
// supplemental raw pass in rawTopWasted.
func topWastedTokens(tw *topWasted) int64 {
	var tok *int64
	switch {
	case tw.SavedTokens != nil:
		tok = tw.SavedTokens
	case tw.Tokens != nil:
		tok = tw.Tokens
	}
	if tok == nil {
		return -1
	}
	v := *tok
	if v < 0 {
		return -v
	}
	return v
}

// rawInt64 extracts the first non-nil int64 value found under any of the
// candidate keys in a raw JSON map.
func rawInt64(m map[string]json.RawMessage, keys ...string) *int64 {
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			var v int64
			if json.Unmarshal(raw, &v) == nil {
				return &v
			}
		}
	}
	return nil
}

// rawFloat64 extracts the first non-nil float64 value found under any of the
// candidate keys in a raw JSON map.
func rawFloat64(m map[string]json.RawMessage, keys ...string) *float64 {
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			var v float64
			if json.Unmarshal(raw, &v) == nil {
				return &v
			}
		}
	}
	return nil
}

// rawTopWasted extracts a topWasted entry from a raw JSON map under any of
// the candidate keys. After the typed parse it does a supplemental raw-map
// pass to coerce snake_case aliases (e.g. "saved_tokens" → SavedTokens) that
// the struct tags do not cover. Returns nil when not found.
func rawTopWasted(m map[string]json.RawMessage, keys ...string) *topWasted {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var tw topWasted
		if json.Unmarshal(raw, &tw) != nil {
			continue
		}
		// Supplemental pass: resolve snake_case aliases not declared on the struct.
		if tw.SavedTokens == nil {
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) == nil {
				if v := rawInt64(nested, "saved_tokens"); v != nil {
					tw.SavedTokens = v
				}
			}
		}
		return &tw
	}
	return nil
}
