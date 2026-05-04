package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowValidate(parent *cobra.Command) {
	var jsonMode bool
	var sinceVersion string

	cmd := &cobra.Command{
		Use:          "validate",
		Short:        "Validate the structural integrity of a workflow.json",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			// Parse --since-version if provided.
			var sinceTS time.Time
			hasSince := sinceVersion != ""
			if hasSince {
				sinceTS, err = time.Parse(time.RFC3339, sinceVersion)
				if err != nil {
					return fmt.Errorf("--since-version: invalid RFC3339 timestamp %q: %w", sinceVersion, err)
				}
			}

			// When neither new flag is set AND BROWZER_NO_SCHEMA_CHECK=1, fall back
			// to the legacy wf.Validate(typed) path for backward-compat with
			// pre-CUE fixtures used by integration tests. When either new flag is
			// set, always use schema.ValidateWorkflow regardless of the env bypass —
			// the caller opted into the richer output and the bypass only skips the
			// write-path validation gate, not read/query validation.
			noSchemaCheck := os.Getenv("BROWZER_NO_SCHEMA_CHECK") == "1"
			if noSchemaCheck && !jsonMode && !hasSince {
				typed, _, loadErr := loadWorkflow(path)
				if loadErr != nil {
					return loadErr
				}
				errs := wf.Validate(typed)
				if len(errs) == 0 {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "valid")
					return nil
				}
				for _, e := range errs {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", e.Path, e.Message)
				}
				return fmt.Errorf("%d validation error(s)", len(errs))
			}

			// Switch to schema.ValidateWorkflow (CUE-based, single source of truth).
			// This gives us AddedIn metadata required for --since-version filtering
			// and consistent JSON output for --json. The legacy wf.Validate(typed)
			// path above is retained only for the BROWZER_NO_SCHEMA_CHECK bypass.
			rawBytes, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("read workflow: %w", readErr)
			}

			result := schema.ValidateWorkflow(rawBytes)

			// Apply --since-version filter: keep only violations with AddedIn >= ts.
			// Operator intent: "ignore violations that predated this workflow's start."
			// Exit code follows the FILTERED result, not the raw result.
			if hasSince {
				var filtered []schema.Violation
				for _, v := range result.Violations {
					vTS, parseErr := time.Parse(time.RFC3339, v.AddedIn)
					if parseErr != nil {
						// Unknown addedIn format — include conservatively.
						filtered = append(filtered, v)
						continue
					}
					if !vTS.Before(sinceTS) {
						filtered = append(filtered, v)
					}
				}
				result = schema.ValidationResult{
					Valid:      len(filtered) == 0,
					Violations: filtered,
				}
			}

			// --json: marshal ValidationResult to stdout.
			if jsonMode {
				out, marshalErr := json.MarshalIndent(result, "", "  ")
				if marshalErr != nil {
					return fmt.Errorf("marshal result: %w", marshalErr)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", out)
				if !result.Valid {
					return fmt.Errorf("%d violation(s)", len(result.Violations))
				}
				return nil
			}

			// Human-readable output (default path, preserved verbatim).
			if result.Valid {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "valid")
				return nil
			}
			for _, v := range result.Violations {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", v.Path, v.Message)
			}
			return fmt.Errorf("%d validation error(s)", len(result.Violations))
		},
	}

	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit JSON output {\"valid\": bool, \"violations\": [...]}")
	cmd.Flags().StringVar(&sinceVersion, "since-version", "", "RFC3339 timestamp; filter violations to those with addedIn >= ts")

	parent.AddCommand(cmd)
}
