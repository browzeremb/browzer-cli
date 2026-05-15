package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/browzeremb/browzer-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// wastedPattern maps a regex match against the shell skeleton to a source
// label.  Mirrors the PATTERNS array in browzer-track-wasted.mjs exactly so
// the Go and JS guards classify the same commands identically.
type wastedPattern struct {
	source string
	re     *regexp.Regexp
}

// wastepPatterns is the ordered list of wasted-command classifiers.
// First match wins, just like the JS guard.
var wastePatterns = []wastedPattern{
	{source: "wasted-grep", re: regexp.MustCompile(`(?i)^\s*grep\s+.*-r\b`)},
	{source: "wasted-grep", re: regexp.MustCompile(`(?i)^\s*grep\s+.*-R\b`)},
	{source: "wasted-rg", re: regexp.MustCompile(`(?i)^\s*rg\b`)},
	{source: "wasted-find", re: regexp.MustCompile(`(?i)^\s*find\s+.*\s+-name\b`)},
	{source: "wasted-find", re: regexp.MustCompile(`(?i)^\s*find\s+.*\s+-iname\b`)},
	{source: "wasted-find", re: regexp.MustCompile(`(?i)^\s*ls\s+.*-R\b`)},
}

// browzerPrefixRE matches a skeleton that begins with the browzer CLI binary
// so we can skip browzer-native commands (they're tracked by track-cli).
var browzerPrefixRE = regexp.MustCompile(`(?i)^\s*browzer\s`)

// trackWastedInput is the PostToolUse(Bash) hook envelope for the
// track-wasted guard.
type trackWastedInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	ToolResponse struct {
		Stdout string `json:"stdout"`
	} `json:"tool_response"`
	SessionID string `json:"session_id"`
}

// newTrackWastedCommand returns the cobra command for the track-wasted guard.
func newTrackWastedCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "track-wasted",
		Short: "Track wasted / redundant tool calls",
		RunE:  runTrackWasted,
	}
}

// runTrackWasted is the cobra RunE for the track-wasted guard.
// Always exits ExitPassthrough — purely observational.
func runTrackWasted(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook track-wasted: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	trackWastedDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// trackWastedDecide is the testable core. It parses the hook input, classifies
// the command as "wasted" (grep/rg/find/ls that should have been browzer
// explore), and fires a fire-and-forget telemetry event with a negative
// savedTokens value — representing tokens the agent spent on an unindexed
// search path.
//
// Returns the matched source label (non-empty when telemetry was submitted).
func trackWastedDecide(raw []byte) string {
	if !internalhooks.GateAllowed("track-wasted") {
		return ""
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return ""
	}

	var in trackWastedInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	if in.ToolName != "Bash" {
		return ""
	}

	cmd := in.ToolInput.Command
	if cmd == "" {
		return ""
	}

	skeleton := internalhooks.StripQuoted(cmd)

	// Browzer-native commands are tracked by track-cli, not here.
	if browzerPrefixRE.MatchString(skeleton) {
		return ""
	}

	matched := classifyWasted(skeleton)
	if matched == "" {
		return ""
	}

	outputBytes := len([]byte(in.ToolResponse.Stdout))
	// Negative savedTokens = wasted spend (counterfactual: what browzer
	// explore would have saved had it been used instead).
	wastedTokens := -(outputBytes / 4)
	if wastedTokens == 0 {
		wastedTokens = -1 // record the event even for zero-byte outputs
	}

	tracker.EmitHookDelta(tracker.HookDeltaArgs{
		Hook:             matched,
		SavedTokens:      wastedTokens,
		SessionID:        in.SessionID,
		EstimationMethod: "counterfactual",
	})

	return matched
}

// classifyWasted returns the source label for the first matching wasted
// pattern, or "" when no pattern matches.
func classifyWasted(skeleton string) string {
	for _, p := range wastePatterns {
		if p.re.MatchString(skeleton) {
			return p.source
		}
	}
	return ""
}

