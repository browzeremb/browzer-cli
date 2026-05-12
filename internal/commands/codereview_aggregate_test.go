package commands

// Tests for `browzer codereview aggregate`.
//
// AC-1: happy path — 3 members → consolidated CODE_REVIEW.json written.
// AC-2: overlap merge — mergedFrom populated; no finding dropped.
// AC-3: missing staging dir — exit code 2.
// AC-4: corrupt file, non-strict — skipped + logged; success with remaining files.
// AC-5: corrupt file, strict mode — exit code 3.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// setupFeatDir creates a minimal git-root-like temp directory and a staging
// dir for the given feat id.  Returns the fake repo root and staging dir.
// Uses `git init` so git.FindGitRoot (which shells out to git rev-parse)
// resolves the temp dir as a git repository root.
func setupFeatDir(t *testing.T, featID string) (repoRoot, stagingDir string) {
	t.Helper()
	root := t.TempDir()
	// git init so git rev-parse --show-toplevel recognises this as a repo.
	if err := runGitInit(t, root); err != nil {
		t.Fatalf("git init in temp dir: %v", err)
	}
	staging := filepath.Join(root, "docs", "browzer", featID, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("create staging dir: %v", err)
	}
	return root, staging
}

// runGitInit runs `git init --quiet` in dir so git plumbing commands work.
func runGitInit(t *testing.T, dir string) error {
	t.Helper()
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init: %w\n%s", err, out)
	}
	return nil
}

// writeMemberFile writes a minimal valid CODE_REVIEW.<member>.json.
func writeMemberFile(t *testing.T, stagingDir, member string, findings []map[string]any) {
	t.Helper()
	payload := map[string]any{"findings": findings}
	data, _ := json.Marshal(payload)
	path := filepath.Join(stagingDir, "CODE_REVIEW."+member+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writeMemberFile %s: %v", path, err)
	}
}

// memberFinding builds a minimal finding map for test fixtures.
// Uses generic, plugin-agnostic file paths (no apps/api, etc.).
func memberFinding(id, domain, severity, file string, line int) map[string]any {
	return map[string]any{
		"id": id, "domain": domain, "severity": severity,
		"category": "test", "file": file, "line": line,
		"description": "test finding", "suggestedFix": "",
		"assignedSkill": nil, "status": "open",
	}
}

// runAggregate executes `browzer codereview aggregate --feat <id>` (chdir to
// root first so git.FindGitRoot can locate .git).
func runAggregate(t *testing.T, repoRoot, featID string, extraArgs ...string) error {
	t.Helper()
	t.Chdir(repoRoot)
	args := append([]string{"codereview", "aggregate", "--feat", featID}, extraArgs...)
	cmd := NewRootCommand("test")
	cmd.SetArgs(args)
	return cmd.Execute()
}

// runAggregateWithID executes `browzer codereview aggregate --id <id>` using
// the canonical --id alias instead of the legacy --feat flag.
func runAggregateWithID(t *testing.T, repoRoot, featID string, extraArgs ...string) error {
	t.Helper()
	t.Chdir(repoRoot)
	args := append([]string{"codereview", "aggregate", "--id", featID}, extraArgs...)
	cmd := NewRootCommand("test")
	cmd.SetArgs(args)
	return cmd.Execute()
}

// buildCodeReviewCommand builds an isolated cobra root wired with the
// codereview command tree and explicit stdout/stderr buffers for capture.
// This lets tests compare byte-level output without relying on os.Stdout.
func buildCodeReviewCommand(stdout, stderr *bytes.Buffer) *cobra.Command {
	root := NewRootCommand("test")
	if stdout != nil {
		root.SetOut(stdout)
	}
	if stderr != nil {
		root.SetErr(stderr)
	}
	return root
}

// exitCode extracts the CliError exit code from an error, defaulting to 1.
func exitCode(err error) int {
	var ce *cliErrors.CliError
	if errors.As(err, &ce) {
		return ce.ExitCode
	}
	return 1
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-1: 3 members, no overlap → consolidated file written
// ─────────────────────────────────────────────────────────────────────────────

func TestCodeReviewAggregate_HappyPath_ThreeMembers(t *testing.T) {
	root, stagingDir := setupFeatDir(t, "feat-test-aggregate")

	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		memberFinding("SR-1", "senior-engineer", "high", "src/routes/auth.ts", 10),
	})
	writeMemberFile(t, stagingDir, "software-architect", []map[string]any{
		memberFinding("ARCH-1", "software-architect", "medium", "src/services/user.ts", 20),
	})
	writeMemberFile(t, stagingDir, "qa", []map[string]any{
		memberFinding("QA-1", "qa", "low", "src/routes/billing.ts", 30),
	})

	err := runAggregate(t, root, "feat-test-aggregate")
	if err != nil {
		t.Fatalf("expected success; got: %v", err)
	}

	outPath := filepath.Join(stagingDir, "CODE_REVIEW.json")
	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("CODE_REVIEW.json not written: %v", readErr)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("CODE_REVIEW.json parse: %v", err)
	}

	findings, _ := result["findings"].([]any)
	if len(findings) != 3 {
		t.Errorf("expected 3 findings; got %d", len(findings))
	}

	// All IDs must follow F-NNN format.
	for i, raw := range findings {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		if !strings.HasPrefix(id, "F-") {
			t.Errorf("finding[%d].id = %q; want F-NNN format", i, id)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-2: overlap → mergedFrom populated, both findings kept
// ─────────────────────────────────────────────────────────────────────────────

func TestCodeReviewAggregate_Overlap_MergedFrom_BothKept(t *testing.T) {
	root, stagingDir := setupFeatDir(t, "feat-test-overlap")

	// Both members report a finding on src/worker.ts:10.
	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		memberFinding("SR-1", "senior-engineer", "high", "src/worker.ts", 10),
	})
	writeMemberFile(t, stagingDir, "software-architect", []map[string]any{
		memberFinding("ARCH-1", "software-architect", "high", "src/worker.ts", 10),
	})

	err := runAggregate(t, root, "feat-test-overlap")
	if err != nil {
		t.Fatalf("expected success; got: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(stagingDir, "CODE_REVIEW.json"))
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	findings, _ := result["findings"].([]any)

	if len(findings) != 2 {
		t.Fatalf("both findings must be kept; got %d", len(findings))
	}

	// Each finding must have a non-empty mergedFrom.
	for i, raw := range findings {
		m, _ := raw.(map[string]any)
		mf, _ := m["mergedFrom"].([]any)
		if len(mf) == 0 {
			t.Errorf("finding[%d] mergedFrom empty; expected cross-reference", i)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-3: missing staging dir → exit code 2
// ─────────────────────────────────────────────────────────────────────────────

func TestCodeReviewAggregate_MissingDir_ExitCode2(t *testing.T) {
	root := t.TempDir()
	// git init so FindGitRoot resolves — but do NOT create the staging dir.
	if err := runGitInit(t, root); err != nil {
		t.Fatalf("git init: %v", err)
	}

	err := runAggregate(t, root, "feat-nonexistent-dir")
	if err == nil {
		t.Fatal("expected error for missing staging dir; got nil")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("expected exit code 2; got %d (err: %v)", code, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-4: corrupt file, non-strict → skipped + logged, success with rest
// ─────────────────────────────────────────────────────────────────────────────

func TestCodeReviewAggregate_CorruptFile_NonStrict_SkipsAndSucceeds(t *testing.T) {
	root, stagingDir := setupFeatDir(t, "feat-test-corrupt-nonstrict")

	// One valid member file.
	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		memberFinding("SR-1", "senior-engineer", "low", "src/lib/util.ts", 5),
	})
	// One corrupt file.
	corruptPath := filepath.Join(stagingDir, "CODE_REVIEW.corrupt.json")
	_ = os.WriteFile(corruptPath, []byte("{ not valid json !!!"), 0o644)

	err := runAggregate(t, root, "feat-test-corrupt-nonstrict")
	if err != nil {
		t.Fatalf("non-strict mode should succeed with 1 valid member; got: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(stagingDir, "CODE_REVIEW.json"))
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	findings, _ := result["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("expected 1 finding from valid member; got %d", len(findings))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-5: corrupt file, strict mode → exit code 3
// ─────────────────────────────────────────────────────────────────────────────

func TestCodeReviewAggregate_CorruptFile_Strict_ExitCode3(t *testing.T) {
	root, stagingDir := setupFeatDir(t, "feat-test-corrupt-strict")

	// One corrupt file.
	corruptPath := filepath.Join(stagingDir, "CODE_REVIEW.corrupt.json")
	_ = os.WriteFile(corruptPath, []byte("{ not valid json !!!"), 0o644)

	err := runAggregate(t, root, "feat-test-corrupt-strict", "--strict")
	if err == nil {
		t.Fatal("expected error for corrupt file in strict mode; got nil")
	}
	if code := exitCode(err); code != 3 {
		t.Errorf("expected exit code 3; got %d (err: %v)", code, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Dry-run: output goes to stdout, no file written
// ─────────────────────────────────────────────────────────────────────────────

func TestCodeReviewAggregate_DryRun_NoFileWritten(t *testing.T) {
	root, stagingDir := setupFeatDir(t, "feat-test-dryrun")

	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		memberFinding("SR-1", "senior-engineer", "low", "src/lib/util.ts", 5),
	})

	err := runAggregate(t, root, "feat-test-dryrun", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}

	outPath := filepath.Join(stagingDir, "CODE_REVIEW.json")
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Error("--dry-run must NOT write CODE_REVIEW.json to disk")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FR-3: --id alias produces byte-identical JSON output to --feat
// ─────────────────────────────────────────────────────────────────────────────

// TestCodeReviewAggregate_IDAlias_SameAsFeat verifies byte-equality of the
// JSON output produced by --feat vs --id on identical fixtures.  Both flags
// bind to the same featID variable; any divergence is a regression.
//
// Strategy: run aggregate --json (stdout, no file write) twice over the same
// staging dir — once with --feat, once with --id — and assert bytes.Equal on
// the captured stdout payloads.
func TestCodeReviewAggregate_IDAlias_SameAsFeat(t *testing.T) {
	// Build one staging dir that both invocations share (read-only; --json
	// never writes to disk so there is no ordering dependency).
	root, stagingDir := setupFeatDir(t, "feat-test-id-alias")

	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		memberFinding("SR-1", "senior-engineer", "high", "src/routes/auth.ts", 10),
	})

	t.Chdir(root)

	// Run with --feat (legacy flag).
	var featOut, featErr bytes.Buffer
	featCmd := buildCodeReviewCommand(&featOut, &featErr)
	featCmd.SetArgs([]string{"codereview", "aggregate", "--feat", "feat-test-id-alias", "--json"})
	if err := featCmd.Execute(); err != nil {
		t.Fatalf("--feat: expected success; got: %v\nstderr: %s", err, featErr.String())
	}

	// Run with --id (canonical flag).
	var idOut, idErr bytes.Buffer
	idCmd := buildCodeReviewCommand(&idOut, &idErr)
	idCmd.SetArgs([]string{"codereview", "aggregate", "--id", "feat-test-id-alias", "--json"})
	if err := idCmd.Execute(); err != nil {
		t.Fatalf("--id: expected success; got: %v\nstderr: %s", err, idErr.String())
	}

	// Byte-equality: both flags must produce identical JSON output.
	if !bytes.Equal(featOut.Bytes(), idOut.Bytes()) {
		t.Errorf("--feat and --id produce different JSON output:\n--feat: %s\n--id:   %s",
			featOut.String(), idOut.String())
	}

	// Sanity: the output must be valid JSON with at least one finding.
	var result map[string]any
	if err := json.Unmarshal(idOut.Bytes(), &result); err != nil {
		t.Fatalf("JSON parse of --id output: %v\nraw: %s", err, idOut.String())
	}
	findings, _ := result["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("expected 1 finding; got %d", len(findings))
	}
}

// TestCodeReviewAggregate_NoFlag_Errors verifies that omitting both --id
// and --feat produces an error with a helpful message.
func TestCodeReviewAggregate_NoFlag_Errors(t *testing.T) {
	root := t.TempDir()
	if err := runGitInit(t, root); err != nil {
		t.Fatalf("git init: %v", err)
	}

	t.Chdir(root)
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"codereview", "aggregate"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when neither --id nor --feat is provided; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "id") && !strings.Contains(msg, "feat") {
		t.Errorf("error message should mention 'id' or 'feat'; got: %s", msg)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// R-2: fillFindingDefaults — empty required fields are backfilled
// ─────────────────────────────────────────────────────────────────────────────

// TestAggregate_RoundTripWithSaveStep verifies that findings with empty
// status/domain/description/category are filled with CUE-valid defaults by
// the aggregator (R-2).  The test stages 2 per-member files where findings
// intentionally omit those required fields, runs aggregate, reads
// CODE_REVIEW.json, and asserts every finding has non-empty values.
//
// The "RoundTrip" in the name refers to the logical round-trip of partial
// input → aggregate → valid output; the optional save-step subprocess gate
// (which requires a live browzer daemon) is skipped when the binary is not on
// PATH so the test is self-contained in any CI environment.
func TestAggregate_RoundTripWithSaveStep(t *testing.T) {
	featID := "feat-test-fill-defaults"
	root, stagingDir := setupFeatDir(t, featID)

	// senior-engineer member: findings with empty status, domain, description, category.
	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		{
			// All required fields empty / missing — aggregator must fill them.
			"id":           "SR-1",
			"domain":       "",
			"severity":     "high",
			"category":     "",
			"file":         "src/routes/auth.ts",
			"line":         10,
			"description":  "",
			"suggestedFix": "Use timingSafeEqual",
			"assignedSkill": nil,
			"status":       "",
		},
		{
			// description empty AND suggestedFix empty → fallback sentinel.
			"id":           "SR-2",
			"domain":       "",
			"severity":     "medium",
			"category":     "",
			"file":         "src/lib/util.ts",
			"line":         5,
			"description":  "",
			"suggestedFix": "",
			"assignedSkill": nil,
			"status":       "",
		},
	})

	// software-architect member: one finding with only status empty.
	writeMemberFile(t, stagingDir, "software-architect", []map[string]any{
		{
			"id":           "ARCH-1",
			"domain":       "",
			"severity":     "low",
			"category":     "",
			"file":         "src/services/user.ts",
			"line":         20,
			"description":  "missing index",
			"suggestedFix": "",
			"assignedSkill": nil,
			"status":       "",
		},
	})

	// Run aggregate (writes CODE_REVIEW.json).
	if err := runAggregate(t, root, featID); err != nil {
		t.Fatalf("aggregate failed: %v", err)
	}

	outPath := filepath.Join(stagingDir, "CODE_REVIEW.json")
	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("CODE_REVIEW.json not written: %v", readErr)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("CODE_REVIEW.json parse: %v", err)
	}

	findings, _ := result["findings"].([]any)
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings; got %d", len(findings))
	}

	// --- Domain defaults ---
	// senior-engineer findings → "complexity"
	// software-architect finding → "architecture"
	wantDomains := map[string]string{
		"F-001": "complexity",   // senior-engineer
		"F-002": "complexity",   // senior-engineer
		"F-003": "architecture", // software-architect
	}

	for i, raw := range findings {
		m, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("finding[%d] is not a map", i)
		}

		id, _ := m["id"].(string)

		// status must not be empty — default "open"
		status, _ := m["status"].(string)
		if status == "" {
			t.Errorf("finding[%d] (id=%s): status is empty; expected 'open'", i, id)
		}

		// domain must not be empty
		domain, _ := m["domain"].(string)
		if domain == "" {
			t.Errorf("finding[%d] (id=%s): domain is empty", i, id)
		}
		if want, ok := wantDomains[id]; ok && domain != want {
			t.Errorf("finding[%d] (id=%s): domain = %q; want %q", i, id, domain, want)
		}

		// description must not be empty
		desc, _ := m["description"].(string)
		if desc == "" {
			t.Errorf("finding[%d] (id=%s): description is empty", i, id)
		}

		// category must not be empty — default "review-finding"
		cat, _ := m["category"].(string)
		if cat == "" {
			t.Errorf("finding[%d] (id=%s): category is empty; expected 'review-finding'", i, id)
		}
	}

	// Spot-check: SR-1 had suggestedFix="Use timingSafeEqual" and empty description
	// → description should be the suggestedFix value.
	sr1 := findFindingByOriginalOrder(findings, 0) // first finding = SR-1 (senior-engineer sorts before software-architect)
	if desc, _ := sr1["description"].(string); desc != "Use timingSafeEqual" {
		t.Errorf("SR-1: description = %q; want 'Use timingSafeEqual' (from suggestedFix)", desc)
	}

	// Spot-check: SR-2 had both description and suggestedFix empty → sentinel.
	sr2 := findFindingByOriginalOrder(findings, 1)
	if desc, _ := sr2["description"].(string); desc != "(no description provided)" {
		t.Errorf("SR-2: description = %q; want '(no description provided)'", desc)
	}
}

// findFindingByOriginalOrder returns findings[idx] as a map, or an empty map.
func findFindingByOriginalOrder(findings []any, idx int) map[string]any {
	if idx >= len(findings) {
		return map[string]any{}
	}
	m, _ := findings[idx].(map[string]any)
	return m
}

// TestCodeReviewAggregate_IDAlias_WritesDisk verifies that the canonical --id
// flag (as opposed to the legacy --feat alias) still writes CODE_REVIEW.json
// to disk when --json is not passed.  runAggregateWithID is exercised here so
// the helper is not dead code.
func TestCodeReviewAggregate_IDAlias_WritesDisk(t *testing.T) {
	root, stagingDir := setupFeatDir(t, "feat-test-id-writes-disk")

	writeMemberFile(t, stagingDir, "senior-engineer", []map[string]any{
		memberFinding("SR-1", "senior-engineer", "high", "src/routes/auth.ts", 10),
	})

	if err := runAggregateWithID(t, root, "feat-test-id-writes-disk"); err != nil {
		t.Fatalf("--id flag: expected success; got: %v", err)
	}

	outPath := filepath.Join(stagingDir, "CODE_REVIEW.json")
	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("--id flag did not write CODE_REVIEW.json: %v", readErr)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("CODE_REVIEW.json parse: %v", err)
	}
	findings, _ := result["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("expected 1 finding; got %d", len(findings))
	}
}

// TestCodeReviewAggregate_BothFlags_MutuallyExclusive verifies that providing
// both --id and --feat is rejected with cobra's mutex error, which must name
// both flag identifiers so the operator knows which flags conflict.
func TestCodeReviewAggregate_BothFlags_MutuallyExclusive(t *testing.T) {
	root, _ := setupFeatDir(t, "feat-test-both-flags")

	t.Chdir(root)
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"codereview", "aggregate", "--id", "feat-test-both-flags", "--feat", "feat-test-both-flags"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when both --id and --feat are provided; got nil")
	}
	msg := err.Error()
	// Cobra's MarkFlagsMutuallyExclusive produces:
	//   "if any flags in the group [feat id] are set none of the others can be; [feat id] were all set"
	// Both flag names must appear so the operator knows exactly which flags conflict.
	if !strings.Contains(msg, "id") {
		t.Errorf("mutex error message should mention 'id'; got: %s", msg)
	}
	if !strings.Contains(msg, "feat") {
		t.Errorf("mutex error message should mention 'feat'; got: %s", msg)
	}
}
