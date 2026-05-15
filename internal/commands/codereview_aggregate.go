package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/codereview"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

// quietByDefaultUnderLLM reports whether the command should suppress
// decorative stderr output. True when BROWZER_LLM is truthy (via the
// shared output.EnvLLMEnabled() parser) or when the inherited --llm
// flag is set on the command tree.
func quietByDefaultUnderLLM(cmd *cobra.Command) bool {
	if output.EnvLLMEnabled() {
		return true
	}
	if cmd != nil {
		if f := cmd.Flags().Lookup("llm"); f != nil && f.Value.String() == "true" {
			return true
		}
		if f := cmd.InheritedFlags().Lookup("llm"); f != nil && f.Value.String() == "true" {
			return true
		}
	}
	return false
}

// codeReviewCmd is the parent cobra command for all `browzer codereview`
// subcommands.  Paired with TASK_06's documented aggregator contract:
// every per-member CODE_REVIEW.<member>.json file in the staging directory
// is merged into a single CODE_REVIEW.json following the preserve-all
// algorithm.
//
// CLI surface consumed by code-review/SKILL.md:
//
//	browzer codereview aggregate --feat <feat-id> [flags]
//
// Flags:
//
//	--feat <id>   feature id (required); resolves staging dir at
//	              docs/browzer/<feat>/staging/
//	--dry-run     print consolidated JSON to stdout without writing to file
//	--json        emit consolidated JSON to stdout (implies --dry-run)
//	--strict      fail (exit 3) if any per-member file fails schema validation;
//	              default: log + skip corrupt files
//
// Exit codes:
//
//	0   success, ≥1 member file aggregated
//	2   no member files found
//	3   schema validation failed in --strict mode
//	4   write failure (permission/disk)
var codeReviewCmd = &cobra.Command{
	Use:   "codereview",
	Short: "Code-review aggregation utilities",
	Long: "Utilities for the browzer code-review pipeline.\n" +
		"\n" +
		"Subcommands:\n" +
		"  aggregate   Merge per-member CODE_REVIEW.<member>.json files into\n" +
		"              a single consolidated CODE_REVIEW.json.\n" +
		"\n" +
		"Run `browzer codereview [command] --help` for subcommand details.",
}

// registerCodeReview adds the codereview command group to parent.
func registerCodeReview(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   codeReviewCmd.Use,
		Short: codeReviewCmd.Short,
		Long:  codeReviewCmd.Long,
	}
	registerCodeReviewAggregate(cmd)
	parent.AddCommand(cmd)
}

// registerCodeReviewAggregate wires the `browzer codereview aggregate` verb.
//
// The verb reads every docs/browzer/<feat>/staging/CODE_REVIEW.<member>.json
// file, validates the findings[] array shape, and produces a single
// consolidated docs/browzer/<feat>/staging/CODE_REVIEW.json following the
// preserve-all aggregation algorithm from the code-review skill contract
// (TASK_06 SKILL.md §Aggregator).
//
// This function is the implementation entry-point referenced in
// packages/skills/skills/code-review/SKILL.md.  Any change to flag names,
// exit codes, or output shape is a breaking change for skills that invoke it.
//
// Flag ergonomics: --id is the canonical alias used by all other workflow
// verbs; --feat is the legacy name kept for backward compatibility.  Both
// bind to the same featID variable.  Exactly one must be supplied; supplying
// both is an error (MarkFlagsMutuallyExclusive).
func registerCodeReviewAggregate(parent *cobra.Command) {
	var (
		featID  string
		dryRun  bool
		jsonOut bool
		strict  bool
	)

	cmd := &cobra.Command{
		Use:   "aggregate (--id|--feat) <feat-id>",
		Short: "Merge per-member CODE_REVIEW.<member>.json into CODE_REVIEW.json",
		Long: `Aggregate all per-member code-review staging files for a feature into a
single consolidated CODE_REVIEW.json following the preserve-all algorithm:

  1. Collect every findings[] array from every CODE_REVIEW.<member>.json.
  2. Assign stable per-member IDs (<PREFIX>-<seq>).
  3. Detect overlap (same file+line across lanes) and record mergedFrom[].
  4. Assign global sequential IDs F-001, F-002, … in the consolidated file.
  5. Roll up severityCounts.

No finding is ever dropped.  Severity is informational — it is NOT used as
a dedup key.  When two members raise the same file+line concern, BOTH are
kept and each records the other's per-member ID in mergedFrom[].

Exit codes:
  0   success, ≥1 member file aggregated
  2   no member files found
  3   schema validation failed in --strict mode
  4   write failure (permission/disk)

Examples:
  # Dry-run (print to stdout, no file write)
  browzer codereview aggregate --id feat-20260511-my-feature --dry-run

  # Emit consolidated JSON to stdout
  browzer codereview aggregate --id feat-20260511-my-feature --json

  # Write to staging/CODE_REVIEW.json (normal operation)
  browzer codereview aggregate --id feat-20260511-my-feature

  # Fail hard on any corrupt member file
  browzer codereview aggregate --id feat-20260511-my-feature --strict

  # Legacy --feat alias (backward compatible)
  browzer codereview aggregate --feat feat-20260511-my-feature`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCodeReviewAggregate(cmd, featID, dryRun || jsonOut, strict)
		},
	}

	// --id is the canonical flag (matches all other workflow verbs).
	// --feat is the legacy alias; both bind to featID.
	// Exactly one must be supplied; supplying both is rejected as mutually exclusive.
	cmd.Flags().StringVar(&featID, "id", "", "feature id; resolves docs/browzer/<id>/staging/ (alias: --feat)")
	cmd.Flags().StringVar(&featID, "feat", "", "feature id (legacy alias for --id)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print consolidated JSON to stdout instead of writing to file")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit consolidated JSON to stdout (implies --dry-run)")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail (exit 3) if any per-member file fails schema validation")

	cmd.MarkFlagsOneRequired("id", "feat")
	cmd.MarkFlagsMutuallyExclusive("id", "feat")
	parent.AddCommand(cmd)
}

// runCodeReviewAggregate is the business logic for `browzer codereview
// aggregate`.  It is factored out of the cobra RunE so it can be unit-tested
// without constructing a full command tree.
func runCodeReviewAggregate(cmd *cobra.Command, featID string, stdout bool, strict bool) error {
	if featID == "" {
		// Defensive: cobra's MarkFlagsOneRequired("id", "feat") fires first,
		// but guard here in case the function is called directly in tests.
		return cliErrors.WithCode("--id (or --feat) is required", 1)
	}

	// Resolve repo root via git walk-up so the command works from any
	// subdirectory of the repo.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	repoRoot := git.FindGitRoot(cwd)
	if repoRoot == "" {
		return cliErrors.WithCode("could not locate git root (run from inside a git repo)", 1)
	}

	stagingDir := codereview.StagingDir(repoRoot, featID)

	// If the staging directory does not exist at all, that is a "no member
	// files" condition — exit code 2 (same as an empty directory).
	if _, statErr := os.Stat(stagingDir); os.IsNotExist(statErr) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"codereview aggregate: staging dir not found: %s\n", stagingDir)
		return cliErrors.WithCode(
			fmt.Sprintf("no member files found (staging dir missing): %s", stagingDir), 2)
	}

	// Collect per-member files.
	var logLines []string
	errLogger := func(msg string) {
		logLines = append(logLines, msg)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warn:", msg)
	}

	members, skipped, err := codereview.LoadMemberFiles(stagingDir, strict, errLogger)
	if err != nil {
		// strict-mode schema violation → exit 3.
		return cliErrors.WithCode(fmt.Sprintf("codereview aggregate: %v", err), 3)
	}

	_ = skipped // already logged by errLogger

	if len(members) == 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"codereview aggregate: no CODE_REVIEW.<member>.json files found in %s\n", stagingDir)
		return cliErrors.WithCode(
			fmt.Sprintf("no member files found in %s", stagingDir), 2)
	}

	// Aggregate.
	result := codereview.Aggregate(members)

	// Marshal.
	data, err := codereview.MarshalResult(result)
	if err != nil {
		return fmt.Errorf("marshal consolidated review: %w", err)
	}

	// Output.
	if stdout {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	}

	// Validate the output JSON round-trips cleanly before writing so the
	// operator never receives a success banner followed by a corrupt file.
	var check map[string]any
	if err := json.Unmarshal(data, &check); err != nil {
		return cliErrors.WithCode(
			fmt.Sprintf("internal: round-trip validation of consolidated JSON failed: %v", err), 4)
	}

	outPath := codereview.OutputPath(stagingDir)
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return cliErrors.WithCode(
			fmt.Sprintf("write %s: %v", outPath, err), 4)
	}

	if !quietByDefaultUnderLLM(cmd) {
		// Emit a summary to stderr so stdout stays machine-parseable.
		counts := result.SeverityCounts
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"codereview aggregate: wrote %s (%d findings: %d high / %d medium / %d low)\n",
			outPath,
			len(result.Findings),
			counts.High, counts.Medium, counts.Low,
		)
	}

	return nil
}
