package commands

// Tests for `browzer codereview aggregate`.
//
// AC-1: happy path — 3 members → consolidated CODE_REVIEW.json written.
// AC-2: overlap merge — mergedFrom populated; no finding dropped.
// AC-3: missing staging dir — exit code 2.
// AC-4: corrupt file, non-strict — skipped + logged; success with remaining files.
// AC-5: corrupt file, strict mode — exit code 3.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
