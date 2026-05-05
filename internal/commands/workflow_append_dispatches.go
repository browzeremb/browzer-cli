package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// appendDispatchBatchEntry is the JSON shape accepted by
// `--batch '[{stepId, payload: {...}}, ...]'`.
//
// Payload fields mirror the singular `append-dispatch` flag set
// (`--prompt-file`, `--agent-id`, `--render-template`) but encoded
// inline so a batch covering N subagent dispatches needs ONE
// round-trip instead of N.
//
// `payload.promptFile` is the canonical name; `payload.promptText`
// is accepted as an alternative when the caller already has the
// prompt bytes in memory and prefers not to write a tmp file.
// At least one of the two MUST be set.
type appendDispatchBatchEntry struct {
	StepID  string                          `json:"stepId"`
	Payload appendDispatchBatchEntryPayload `json:"payload"`
}

type appendDispatchBatchEntryPayload struct {
	PromptFile     string `json:"promptFile,omitempty"`
	PromptText     string `json:"promptText,omitempty"`
	AgentID        string `json:"agentId,omitempty"`
	RenderTemplate string `json:"renderTemplate,omitempty"`
}

// registerWorkflowAppendDispatches wires the bulk
// `append-dispatches --batch '<json-array>'` verb (A3.2).
//
// Each entry produces ONE #DispatchRecord (sha256 + spool path +
// #DispatchRecord shape) following the same rules as the singular
// `append-dispatch` command. All N records land in one
// advisory-lock window with a single CUE validation, replacing the
// historic loop that took N round-trips and N separate fsyncs.
func registerWorkflowAppendDispatches(parent *cobra.Command) {
	var batchJSON string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "append-dispatches --batch '<json-array>'",
		Short:        "Append N dispatch records under one advisory-lock window",
		Long: `Append many #DispatchRecord entries to one or more steps' dispatches[]
arrays under a single advisory-lock + write. Required flag
--batch carries the JSON array:

  [
    {"stepId":"STEP_05_CODE_REVIEW","payload":{
       "promptFile":"/tmp/dispatch-1.txt",
       "agentId":"…",
       "renderTemplate":"code-review"}},
    {"stepId":"STEP_05_CODE_REVIEW","payload":{"promptText":"…"}},
    …
  ]

Each entry's payload MUST set either promptFile (path) OR promptText
(literal bytes). agentId defaults to a fresh uuid v4 per entry.
The dispatch spool file is written under
.browzer/dispatch-spool/<feat-slug>/<stepId>/<agentId>.txt — same
layout as the singular append-dispatch verb.

Mutually exclusive with --jq / --bulk on patch; this command is
specifically for #DispatchRecord shapes and bypasses the generic
patch pipeline so the spool-file side-effects are guaranteed.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if batchJSON == "" {
				return fmt.Errorf("--batch is required (provide a JSON array of {stepId, payload} entries)")
			}
			var entries []appendDispatchBatchEntry
			if err := json.Unmarshal([]byte(batchJSON), &entries); err != nil {
				return fmt.Errorf("--batch: invalid JSON array: %w", err)
			}
			if len(entries) == 0 {
				return fmt.Errorf("--batch: array is empty")
			}
			return runAppendDispatchesBatch(cmd, entries, lockTimeout)
		},
	}
	cmd.Flags().StringVar(&batchJSON, "batch", "", "JSON array of {stepId, payload} entries")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}

// runAppendDispatchesBatch validates every entry, materialises each
// dispatch spool file, then composes ONE jq pipeline that appends
// every #DispatchRecord onto the right step. The patch dispatch
// path runs the rest (CUE validation + atomic write).
//
// Side effects (spool files) execute BEFORE the jq pipeline runs.
// On batch validation failure, partially-written spool files are
// not cleaned up — they are deterministic-path overwrites, so a
// retry of the same batch overwrites them in place. This matches
// the singular append-dispatch verb's behaviour.
func runAppendDispatchesBatch(cmd *cobra.Command, entries []appendDispatchBatchEntry, lockTimeout time.Duration) error {
	wfPath, err := getWorkflowPath(cmd)
	if err != nil {
		return err
	}
	repoRoot := schema.FindRepoRoot(filepath.Dir(wfPath))
	featSlug := filepath.Base(filepath.Dir(wfPath))

	// Validate + materialise spool files + build a slice of
	// (stepId, recordJSON) pairs.
	prepares := make([]preparedDispatchRecord, 0, len(entries))
	for i, e := range entries {
		if e.StepID == "" {
			return fmt.Errorf("batch[%d]: stepId is required", i)
		}
		if e.Payload.PromptFile == "" && e.Payload.PromptText == "" {
			return fmt.Errorf("batch[%d]: payload.promptFile or payload.promptText is required", i)
		}
		var promptBytes []byte
		if e.Payload.PromptFile != "" {
			b, readErr := os.ReadFile(e.Payload.PromptFile)
			if readErr != nil {
				return fmt.Errorf("batch[%d]: read promptFile %q: %w",
					i, e.Payload.PromptFile, readErr)
			}
			promptBytes = b
		} else {
			promptBytes = []byte(e.Payload.PromptText)
		}
		sum := sha256.Sum256(promptBytes)
		digest := "sha256:" + hex.EncodeToString(sum[:])

		agentID := e.Payload.AgentID
		if agentID == "" {
			agentID = uuid.NewString()
		}
		spoolDir := filepath.Join(repoRoot, ".browzer", "dispatch-spool", featSlug, e.StepID)
		if mkErr := os.MkdirAll(spoolDir, 0o755); mkErr != nil {
			return fmt.Errorf("batch[%d]: mkdir spool dir: %w", i, mkErr)
		}
		spoolPath := filepath.Join(spoolDir, agentID+".txt")
		if writeErr := writeSpoolFile(spoolPath, promptBytes); writeErr != nil {
			return fmt.Errorf("batch[%d]: write spool file: %w", i, writeErr)
		}
		relPath, relErr := filepath.Rel(repoRoot, spoolPath)
		if relErr != nil {
			relPath = spoolPath
		}

		now := time.Now().UTC().Format(time.RFC3339)
		record := map[string]any{
			"agentId":              agentID,
			"dispatchPromptDigest": digest,
			"promptByteCount":      int64(len(promptBytes)),
			"promptPath":           relPath,
			"renderTemplateUsed":   e.Payload.RenderTemplate,
			"dispatchedAt":         now,
			"model":                nil,
			"status":               "in_progress",
			"findingsAddressed":    []any{},
			"filesModified":        []any{},
			"filesCreated":         []any{},
			"filesDeleted":         []any{},
		}
		if e.Payload.RenderTemplate == "" {
			record["renderTemplateUsed"] = nil
		}
		recordJSON, marshErr := json.Marshal(record)
		if marshErr != nil {
			return fmt.Errorf("batch[%d]: marshal record: %w", i, marshErr)
		}
		prepares = append(prepares, preparedDispatchRecord{stepID: e.StepID, recordJSON: recordJSON})
	}

	jqExpr := buildAppendDispatchesBatchJQ(prepares)
	noLock, _ := cmd.Flags().GetBool("no-lock")
	if !noLock {
		noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
	}
	mode, err := resolveWriteMode(cmd)
	if err != nil {
		return err
	}
	return dispatchToDaemonOrFallback(cmd, wfPath, "patch", wf.MutatorArgs{
		JQExpr: jqExpr,
	}, mode, noLock, lockTimeout)
}

// preparedDispatchRecord pairs a target stepId with the JSON-encoded
// #DispatchRecord that's about to be appended to its dispatches[].
type preparedDispatchRecord struct {
	stepID     string
	recordJSON []byte
}

// buildAppendDispatchesBatchJQ composes a jq pipeline that appends
// each prepared record to the right step's dispatches[] array. The
// approach is the same as buildFindingStatusBatchJQ: chain
// per-entry update expressions with `|` so all N updates apply in
// one pass against the in-memory document.
func buildAppendDispatchesBatchJQ(prepares []preparedDispatchRecord) string {
	var parts []string
	for _, p := range prepares {
		parts = append(parts, fmt.Sprintf(
			`(.steps[] | select(.stepId == %q)).dispatches += [%s]`,
			p.stepID, string(p.recordJSON),
		))
	}
	return strings.Join(parts, " | ")
}
