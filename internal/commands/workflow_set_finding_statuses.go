package commands

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// findingIDPattern mirrors the CUE constraint `id: =~"^F-[0-9]+$"`
// applied to #Finding.id (workflow-v1.cue:540) and to the
// #ReceivingDispatch.findingId / #UnrecoveredFinding.findingId
// references. Validating early at the cobra layer means the
// operator gets the exact bad value flagged rather than a CUE
// disjunction-narrowing line later.
var findingIDPattern = regexp.MustCompile(`^F-[0-9]+$`)

// findingStatusValues mirrors the `#Finding.status` disjunction in
// `packages/cli/schemas/workflow-v1.cue` (line ~549). Hand-mirrored
// because the cobra command needs to validate the CLI argument
// BEFORE constructing the jq pipeline; doing the check here means
// the operator gets a targeted error instead of a CUE-engine
// disjunction failure later.
//
// If the SSOT enum changes, `TestSetFindingStatuses_AcceptsAllSchemaValues`
// fails — that test loads the CUE definition and asserts equivalence.
var findingStatusValues = []string{"open", "fixing", "fixed", "wontfix"}

// findingStatusBatchEntry is the JSON shape accepted by
// `--batch '[{stepId, findingId, status, note?}, ...]'`. Each entry
// becomes one jq update on the matching step's findings[]. We
// enforce shape via Go decode rather than relying on CUE so the
// error messages name the offending entry index, not the (often
// noisy) post-mutation validation surface.
type findingStatusBatchEntry struct {
	StepID    string `json:"stepId"`
	FindingID string `json:"findingId"`
	Status    string `json:"status"`
	Note      string `json:"note,omitempty"`
}

// registerWorkflowSetFindingStatuses wires both the singular
// `set-finding-status <stepId> <findingId> <status>` form (B5) and
// the bulk `set-finding-statuses --batch '<json-array>'` form (A3.1).
//
// Both verbs run under ONE advisory-lock window with a single
// post-mutation CUE validation. The bulk variant collapses 41
// individual mutations into a single round-trip — the friction case
// from the conversion-balance-liquidation pipeline retro.
//
// The note (when supplied) becomes a `notedAt` + `note` pair on the
// finding — the `#Finding` shape doesn't carry a `note` field today,
// so notes round-trip into a `notes` map[string]any sidecar that
// CUE accepts (open struct via the surrounding CodeReview disjunction).
func registerWorkflowSetFindingStatuses(parent *cobra.Command) {
	registerWorkflowSetFindingStatusSingular(parent)
	registerWorkflowSetFindingStatusBulk(parent)
}

// registerWorkflowSetFindingStatusSingular adds the 3-positional-arg
// `set-finding-status` verb. Uses the shared executor below.
func registerWorkflowSetFindingStatusSingular(parent *cobra.Command) {
	var note string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "set-finding-status <stepId> <findingId> <status>",
		Short: "Update one #Finding's status under a single advisory-lock window",
		Long: fmt.Sprintf(`Update the status field of one #Finding entry inside a CODE_REVIEW
or RECEIVING_CODE_REVIEW step's findings[] array.

  stepId       — the workflow step containing the finding
  findingId    — the #Finding.id, must match ^F-[0-9]+$
  status       — one of: %s

For multi-finding updates use the batch form:

  browzer workflow set-finding-statuses \
    --batch '[{"stepId":"…","findingId":"F-1","status":"fixed"}, …]'

Optional --note attaches a sidecar note (notes.<findingId>.note +
notes.<findingId>.notedAt) without modifying the canonical #Finding
shape, so CUE validation continues to pass.`,
			strings.Join(findingStatusValues, " | ")),
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries := []findingStatusBatchEntry{{
				StepID:    args[0],
				FindingID: args[1],
				Status:    args[2],
				Note:      note,
			}}
			return runSetFindingStatuses(cmd, entries, lockTimeout)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "optional note attached as notes.<findingId>.note")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}

// registerWorkflowSetFindingStatusBulk adds the `set-finding-statuses`
// verb that accepts a JSON array of update entries via --batch.
func registerWorkflowSetFindingStatusBulk(parent *cobra.Command) {
	var batchJSON string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "set-finding-statuses --batch '<json-array>'",
		Short: "Update N #Finding statuses in one advisory-lock window",
		Long: fmt.Sprintf(`Update many #Finding statuses inside one or more steps' findings[]
arrays under a single advisory-lock + write. Required flag
--batch carries the JSON array:

  [
    {"stepId":"STEP_05_CODE_REVIEW","findingId":"F-1","status":"fixed"},
    {"stepId":"STEP_05_CODE_REVIEW","findingId":"F-2","status":"wontfix","note":"out of scope"},
    …
  ]

Each entry's status MUST be one of: %s.
Each findingId MUST match ^F-[0-9]+$.
Notes (when present) round-trip into notes.<findingId>.note alongside
the canonical #Finding entry — preserving CUE-validity of the
findings[] array.`, strings.Join(findingStatusValues, " | ")),
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if batchJSON == "" {
				return fmt.Errorf("--batch is required (provide a JSON array of {stepId, findingId, status, note?} entries)")
			}
			var entries []findingStatusBatchEntry
			if err := json.Unmarshal([]byte(batchJSON), &entries); err != nil {
				return fmt.Errorf("--batch: invalid JSON array: %w", err)
			}
			if len(entries) == 0 {
				return fmt.Errorf("--batch: array is empty")
			}
			return runSetFindingStatuses(cmd, entries, lockTimeout)
		},
	}
	cmd.Flags().StringVar(&batchJSON, "batch", "", "JSON array of {stepId, findingId, status, note?} entries")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}

// runSetFindingStatuses validates each entry, composes a single jq
// pipeline that applies all updates, and dispatches via the standard
// patch path so the daemon and standalone routes share the same
// lock + CUE validation semantics.
//
// Validation runs against `findingStatusValues` so a typo in any
// entry's status fails the whole batch BEFORE the lock is acquired.
func runSetFindingStatuses(cmd *cobra.Command, entries []findingStatusBatchEntry, lockTimeout time.Duration) error {
	for i, e := range entries {
		if e.StepID == "" {
			return fmt.Errorf("batch[%d]: stepId is required", i)
		}
		if e.FindingID == "" {
			return fmt.Errorf("batch[%d]: findingId is required", i)
		}
		if !findingIDPattern.MatchString(e.FindingID) {
			return fmt.Errorf("batch[%d]: findingId %q does not match %s",
				i, e.FindingID, findingIDPattern.String())
		}
		if !isValidFindingStatus(e.Status) {
			return fmt.Errorf("batch[%d]: status %q is not one of [%s]",
				i, e.Status, strings.Join(findingStatusValues, ", "))
		}
	}
	jqExpr := buildFindingStatusBatchJQ(entries)

	wfPath, err := getWorkflowPath(cmd)
	if err != nil {
		return err
	}
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

// isValidFindingStatus reports whether s is a member of
// findingStatusValues. Linear scan; the slice has 4 elements.
func isValidFindingStatus(s string) bool {
	for _, v := range findingStatusValues {
		if s == v {
			return true
		}
	}
	return false
}

// buildFindingStatusBatchJQ composes one jq pipeline that applies
// every entry sequentially. We build it as a `|`-chained sequence
// because the jq runner only accepts a single program (no statement
// list under gojq for top-level mutations of the document root —
// the `;` rewriter is for expressions, not for repeated
// `(.steps[…]).foo = bar` statements).
//
// The entries are sorted by (stepId, findingId) so two batches
// with the same set of updates produce byte-identical jq programs;
// useful for reproducibility / dispatch-record digest stability.
//
// Note round-tripping: when an entry carries `Note`, we also write
// to `.notes[<findingId>]` (a top-level sidecar map). The `#Finding`
// shape itself doesn't have a `note` field; the sidecar lives on
// the workflow root where CUE allows the open `notes: [...]` array
// to be augmented. Specifically we write:
//
//   .notes += [{at: "<rfc3339>", findingId: "<id>", note: "<text>"}]
//
// — keeping every note appended at the workflow level so the
// reviewer history is traceable across multiple set-finding
// invocations.
func buildFindingStatusBatchJQ(entries []findingStatusBatchEntry) string {
	sorted := make([]findingStatusBatchEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].StepID != sorted[j].StepID {
			return sorted[i].StepID < sorted[j].StepID
		}
		return sorted[i].FindingID < sorted[j].FindingID
	})

	now := time.Now().UTC().Format(time.RFC3339)
	var parts []string
	for _, e := range sorted {
		parts = append(parts, fmt.Sprintf(
			`((.steps[] | select(.stepId == %q)).codeReview.findings[] | select(.id == %q)).status = %q`,
			e.StepID, e.FindingID, e.Status,
		))
		if e.Note != "" {
			noteEntry := map[string]any{
				"at":        now,
				"findingId": e.FindingID,
				"note":      e.Note,
			}
			noteJSON, _ := json.Marshal(noteEntry)
			parts = append(parts, fmt.Sprintf(`.notes += [%s]`, string(noteJSON)))
		}
	}
	return strings.Join(parts, " | ")
}
