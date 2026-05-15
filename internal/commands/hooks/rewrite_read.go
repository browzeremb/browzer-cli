package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// readHookInput models the PreToolUse(Read) JSON envelope consumed by the
// rewrite-read guard. Only the fields this guard inspects are typed; the
// tool_input map is decoded loosely so unknown keys survive without noise.
type readHookInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput readToolInput  `json:"tool_input"`
	SessionID string         `json:"session_id"`
}

// readToolInput captures the fields of the Read tool's input payload that
// the rewrite-read guard cares about.
type readToolInput struct {
	FilePath string `json:"file_path"`
	Offset   *int   `json:"offset,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

// largeFileThreshold is the minimum file size (40 KB) below which the
// advisory adds more noise than value.
const largeFileThreshold = 40 * 1024 // 40 960 bytes

// injectionThreshold is the file size above which the advisory includes a
// compressed head snippet (4 × largeFileThreshold = 160 KB).
const injectionThreshold = 4 * largeFileThreshold // 163 840 bytes

// headBufBytes is the maximum number of bytes read from the start of a file
// when constructing the head snippet (5 KB).
const headBufBytes = 5120

// headLines is the maximum number of lines included in the head snippet.
const headLines = 60

// binaryThreshold is the null-byte prevalence above which a file is treated
// as binary and the head injection is suppressed.
const binaryThreshold = 0.005

// newRewriteReadCommand returns the real `browzer hook rewrite-read` cobra
// command, replacing the generic stub registered in hooks.go.
func newRewriteReadCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rewrite-read",
		Short: "Rewrite file-read paths before execution",
		RunE:  runRewriteRead,
	}
}

// runRewriteRead is the cobra RunE for the rewrite-read guard.
// Exit-code protocol:
//
//   - exit 0 (ExitAllow)       — advisory emitted; tool call proceeds.
//   - exit 1 (ExitPassthrough) — no decision; Claude Code keeps default.
func runRewriteRead(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook rewrite-read: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, envelope := rewriteReadDecide(raw)
	if envelope != nil {
		writeEnvelope(cmd.OutOrStdout(), *envelope)
	}
	os.Exit(code)
	return nil // unreachable
}

// rewriteReadDecide is the testable core of the guard. It returns the exit
// code and, on the advisory path, a non-nil envelope to write to stdout.
// The caller is responsible for emitting the envelope then os.Exit(code).
func rewriteReadDecide(raw []byte) (int, *rewriteEnvelope) {
	if !internalhooks.GateAllowed("rewrite-read") {
		return internalhooks.ExitPassthrough, nil
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough, nil
	}

	var in readHookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil
	}
	if in.ToolName != "Read" {
		return internalhooks.ExitPassthrough, nil
	}

	filePath := in.ToolInput.FilePath
	if filePath == "" {
		return internalhooks.ExitPassthrough, nil
	}
	// Skip partial reads — caller is already scoping the read; no advisory needed.
	if in.ToolInput.Offset != nil || in.ToolInput.Limit != nil {
		return internalhooks.ExitPassthrough, nil
	}
	if internalhooks.ClassifyPath(filePath) != internalhooks.ClassCode {
		return internalhooks.ExitPassthrough, nil
	}
	if internalhooks.NeverRewriteRE.MatchString(filePath) {
		return internalhooks.ExitPassthrough, nil
	}

	// Resolve an absolute path for stat / read operations.
	absPath := filePath
	if !filepath.IsAbs(filePath) {
		cwd, err := os.Getwd()
		if err != nil {
			return internalhooks.ExitPassthrough, nil
		}
		absPath = filepath.Join(cwd, filePath)
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return internalhooks.ExitPassthrough, nil
	}
	size := info.Size()

	if size <= largeFileThreshold {
		return internalhooks.ExitPassthrough, nil
	}

	includeHead := size > injectionThreshold

	var head string
	var binary bool

	if includeHead {
		head, binary = readHead(absPath)
	}

	// Emit best-effort saved-token telemetry synchronously before os.Exit.
	summaryBytes := estimateSummaryBytes(filePath, includeHead, binary, head)
	savedRaw := max((size-summaryBytes)/4, 0)
	internalhooks.TrackEvent(map[string]any{
		"hook":             "browzer-rewrite-read",
		"savedTokens":      int(savedRaw),
		"estimationMethod": "estimated",
	})

	env := buildAdvisoryEnvelope(filePath, size, includeHead, binary, head)
	return internalhooks.ExitAllow, &env
}

// readHead reads up to headBufBytes from the start of absPath, detects
// whether the content is binary (by null-byte prevalence), and if not
// returns up to headLines lines of UTF-8 text. On any I/O error it returns
// empty strings for both values.
func readHead(absPath string) (head string, binary bool) {
	buf := make([]byte, headBufBytes)
	f, err := os.Open(absPath)
	if err != nil {
		return "", false
	}
	defer f.Close()

	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", false
	}
	chunk := buf[:n]

	// Binary detection: count null bytes; prevalence > 0.5% → binary.
	var nullCount int
	for _, b := range chunk {
		if b == 0 {
			nullCount++
		}
	}
	if n > 0 && float64(nullCount)/float64(n) > binaryThreshold {
		return "", true
	}

	// Split into lines, cap at headLines. strings.Split preserves the same
	// semantics as the former hand-rolled splitLines: a trailing newline
	// produces an empty final element which we trim so "a\nb\n" → ["a","b"].
	raw := string(chunk)
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > headLines {
		lines = lines[:headLines]
	}
	return strings.Join(lines, "\n"), false
}

// buildAdvisoryEnvelope constructs the hookSpecificOutput envelope with the
// advisory additionalContext text and embedded JSON summary block.
func buildAdvisoryEnvelope(filePath string, size int64, includeHead, binary bool, head string) rewriteEnvelope {
	var label string
	switch {
	case !includeHead:
		label = "advisory-only — head skipped to preserve token economy"
	case binary:
		label = "binary file — head omitted"
	default:
		label = fmt.Sprintf("first %d lines (5KB cap)", headLines)
	}

	advisoryText := fmt.Sprintf(
		"This file is large (~%dKB, %d bytes). %s"+
			" — use 'browzer explore \"<symbol>\"' for targeted lookup,"+
			" or 'browzer read --filter=auto' for token-aware reads,"+
			" before requesting the whole file.",
		(size+512)/1024, size, label,
	)

	// Build the summary object and encode it into the context block.
	type summaryObj struct {
		Path      string `json:"path"`
		ByteCount int64  `json:"byteCount"`
		Head      string `json:"head,omitempty"`
		Binary    bool   `json:"binary,omitempty"`
	}
	s := summaryObj{Path: filePath, ByteCount: size}
	if includeHead && !binary && head != "" {
		s.Head = head
	}
	if binary {
		s.Binary = true
	}
	summaryJSON, err := json.Marshal(s)
	if err != nil {
		summaryJSON = []byte("{}")
	}

	additionalContext := advisoryText + "\n\n```json\n" + string(summaryJSON) + "\n```"

	return rewriteEnvelope{
		HookSpecificOutput: &hookSpecificOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			AdditionalContext:  additionalContext,
		},
	}
}

// estimateSummaryBytes approximates the byte count of the JSON summary block
// that will be emitted, used for the saved-token estimate.
func estimateSummaryBytes(filePath string, includeHead, binary bool, head string) int64 {
	n := int64(len(filePath) + 20) // {"path":"...","byteCount":NNNNN}
	if includeHead && !binary && head != "" {
		n += int64(len(head))
	}
	return n
}

