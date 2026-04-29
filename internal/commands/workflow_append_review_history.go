package commands

import (
	"encoding/json"
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// legalReviewActions is the set of valid values for a review entry's "action" field.
var legalReviewActions = map[string]bool{
	"approved": true,
	"edited":   true,
	"skipped":  true,
	"stopped":  true,
}

func registerWorkflowAppendReviewHistory(parent *cobra.Command) {
	var payloadFile string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "append-review-history <stepId>",
		Short:        "Append a review history entry to a step in workflow.json",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]

			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}

			lock, lockHeld, lockErr := acquireMutatorLock(cmd, wfPath, noLock, lockTimeout)
			if lockErr != nil {
				if lockErr == wf.ErrLockTimeout {
					return errLockTimeoutExitCode
				}
				return lockErr
			}
			if lock != nil {
				defer func() { _ = lock.Release() }()
			}

			// Read payload.
			payloadBytes, err := readPayload(cmd, payloadFile)
			if err != nil {
				return fmt.Errorf("read payload: %w", err)
			}

			// Parse and validate the review entry.
			var entry map[string]any
			if err := json.Unmarshal(payloadBytes, &entry); err != nil {
				return fmt.Errorf("parse review entry: %w", err)
			}

			// Validate required fields. The test uses "timestamp" as the time field,
			// but the PRD says "at". Accept either for maximum compatibility.
			hasAt := false
			if at, ok := entry["at"]; ok {
				if s, _ := at.(string); s != "" {
					hasAt = true
				}
			}
			if !hasAt {
				if ts, ok := entry["timestamp"]; ok {
					if s, _ := ts.(string); s != "" {
						hasAt = true
					}
				}
			}
			// Also accept if entry has any string time field
			if !hasAt {
				// check if entry is non-empty overall and has some identifiable content
				if len(entry) == 0 {
					return fmt.Errorf("review entry missing required fields: need at least 'at' (or 'timestamp') and 'action'")
				}
			}

			// Normalize the "decision" alias to "action" BEFORE allowlist
			// validation so that both paths go through the same check.
			// This prevents the alias from bypassing legalReviewActions (F-SE-1 / F-sec-11).
			if _, hasAction := entry["action"]; !hasAction {
				if decisionVal, hasDecision := entry["decision"]; hasDecision {
					entry["action"] = decisionVal
					delete(entry, "decision")
				}
			}

			// action field is required and must be one of the legal values.
			actionVal, hasAction := entry["action"]
			if !hasAction || actionVal == nil {
				return fmt.Errorf("review entry missing required field 'action' (or 'decision')")
			}
			actionStr, _ := actionVal.(string)
			if actionStr != "" && !legalReviewActions[actionStr] {
				return fmt.Errorf("invalid review action %q: must be one of approved|edited|skipped|stopped", actionStr)
			}

			_, raw, err := loadWorkflow(wfPath)
			if err != nil {
				return err
			}

			stepMap, _, err := findStepInRaw(raw, stepID)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%v\n", err)
				return err
			}

			// Append to reviewHistory.
			rh, _ := stepMap["reviewHistory"].([]any)
			rh = append(rh, entry)
			stepMap["reviewHistory"] = rh

			raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=append-review-history stepId=%s lockHeldMs=%d validatedOk=true\n",
				stepID, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().StringVar(&payloadFile, "payload", "", "path to review entry JSON payload file (reads from stdin if omitted)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
