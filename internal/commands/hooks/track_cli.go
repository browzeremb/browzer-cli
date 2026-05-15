package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/browzeremb/browzer-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// browzerSubverbRE matches a browzer explore|search|deps|ask invocation
// inside a (possibly compound) shell skeleton. The check is applied after
// StripQuoted so it is not fooled by tokens embedded inside string literals.
var browzerSubverbRE = regexp.MustCompile(`(?i)\bbrowzer\s+(explore|search|deps|ask)\b`)

// saveFlagRE matches the --save flag with its argument in a shell command
// skeleton, capturing the file path. Compiled once at package init to avoid
// per-call allocation on the hot PostToolUse path.
var saveFlagRE = regexp.MustCompile(`--save[\s=]+(\S+)`)

// compoundSplitRE splits a shell skeleton on compound operators (&&, ||, |)
// and statement terminators (; and newline). Compiled once at package init.
var compoundSplitRE = regexp.MustCompile(`&&|\|\|?|;|\n`)

// trackCLIInput is the PostToolUse(Bash) hook envelope for the track-cli guard.
type trackCLIInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	ToolResponse struct {
		Stdout string `json:"stdout"`
	} `json:"tool_response"`
	SessionID string `json:"session_id"`
}

// newTrackCLICommand returns the cobra command for the track-cli guard.
func newTrackCLICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "track-cli",
		Short: "Track CLI invocations for telemetry",
		RunE:  runTrackCLI,
	}
}

// runTrackCLI is the cobra RunE for the track-cli guard.
// Always exits ExitPassthrough — purely observational.
func runTrackCLI(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook track-cli: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	trackCLIDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// trackCLIDecide is the testable core. It parses the hook input, detects a
// browzer CLI invocation, and fires a fire-and-forget telemetry event.
// Returns true when telemetry was submitted.
func trackCLIDecide(raw []byte) bool {
	if !internalhooks.GateAllowed("track-cli") {
		return false
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return false
	}

	var in trackCLIInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	if in.ToolName != "Bash" {
		return false
	}

	cmd := in.ToolInput.Command
	if cmd == "" {
		return false
	}

	skeleton := internalhooks.StripQuoted(cmd)

	// Split on compound operators to check each segment for a browzer subverb.
	segments := splitCompound(skeleton)
	var subverb string
	for _, seg := range segments {
		if m := browzerSubverbRE.FindStringSubmatch(strings.TrimSpace(seg)); m != nil {
			subverb = strings.ToLower(m[1])
			break
		}
	}
	if subverb == "" {
		return false
	}

	// Measure saved bytes: prefer --save file when referenced, else stdout.
	outputBytes := 0
	saveMatch := saveFlagRE.FindStringSubmatch(skeleton)
	if saveMatch != nil {
		info, err := os.Stat(saveMatch[1])
		if err == nil {
			outputBytes = int(info.Size())
		}
	}
	if outputBytes == 0 {
		outputBytes = len([]byte(in.ToolResponse.Stdout))
	}

	savedTokens := outputBytes / 4 // conservative tokensOf estimate

	tracker.EmitHookDelta(tracker.HookDeltaArgs{
		Hook:             fmt.Sprintf("hook-cli-%s", subverb),
		SavedTokens:      savedTokens,
		SessionID:        in.SessionID,
		ExecMs:           0,
		EstimationMethod: "measured",
	})

	return true
}

// splitCompound splits a shell skeleton on &&, ||, |, ;, and newlines.
func splitCompound(s string) []string {
	return compoundSplitRE.Split(s, -1)
}
