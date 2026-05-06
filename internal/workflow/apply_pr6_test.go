package workflow

import (
	"reflect"
	"testing"
)

// RETRO PR 6 §5.6 — `kind: "rename-domain"` rewrites domains across both
// task.explorer.domains[] and task.explorer.skillsFound[].domain. Returns
// true when something actually changed (so the mutator can record real
// edits vs idempotent no-ops).
func TestRenameTaskExplorerDomain_RewritesBothFields(t *testing.T) {
	taskMap := map[string]any{
		"explorer": map[string]any{
			"domains": []any{"fastapi-backend", "fastify-backend"},
			"skillsFound": []any{
				map[string]any{"domain": "fastapi-backend", "skill": "fastify-best-practices", "relevance": "high"},
				map[string]any{"domain": "go-cli", "skill": "go-best-practices", "relevance": "med"},
			},
		},
	}
	if !renameTaskExplorerDomain(taskMap, "fastapi-backend", "fastify-backend") {
		t.Fatal("expected renameTaskExplorerDomain to return true (renames happened)")
	}

	exp := taskMap["explorer"].(map[string]any)
	gotDomains := exp["domains"].([]any)
	// Dedup-on-rewrite: from→to where to was already present collapses to
	// a single entry; preserves the operator's intent of fixing the typo.
	if want := []any{"fastify-backend"}; !reflect.DeepEqual(gotDomains, want) {
		t.Errorf("domains mismatch: want %v, got %v", want, gotDomains)
	}

	gotSkills := exp["skillsFound"].([]any)
	if got, _ := gotSkills[0].(map[string]any)["domain"].(string); got != "fastify-backend" {
		t.Errorf("skillsFound[0].domain not rewritten: got %q", got)
	}
	if got, _ := gotSkills[1].(map[string]any)["domain"].(string); got != "go-cli" {
		t.Errorf("skillsFound[1].domain wrongly rewritten: got %q", got)
	}
}

// Idempotent no-op: rename when neither field contains `from` returns
// false WITHOUT altering the map.
func TestRenameTaskExplorerDomain_NoOpWhenAbsent(t *testing.T) {
	taskMap := map[string]any{
		"explorer": map[string]any{
			"domains":     []any{"fastify-backend"},
			"skillsFound": []any{map[string]any{"domain": "go-cli", "skill": "x", "relevance": "med"}},
		},
	}
	if renameTaskExplorerDomain(taskMap, "fastapi-backend", "fastify-backend") {
		t.Fatal("expected renameTaskExplorerDomain to return false (no edits)")
	}
}

// RETRO PR 6 §2.2 — projectExplorerRichToLean translates the rich shape
// the haiku Explorer subagent emits (path/anchor/imports per file +
// depsGraph.{forward,reverse}) to the lean shape the CUE schema persists.
func TestProjectExplorerRichToLean_FlattensFilesAndRenamesDeps(t *testing.T) {
	// Make sure the legacy-reject env isn't bleeding from another test
	// or the parent shell.
	t.Setenv("BROWZER_EXPLORER_LEGACY_REJECT", "")

	taskMap := map[string]any{
		"explorer": map[string]any{
			"filesModified": []any{
				map[string]any{"path": "apps/api/src/routes/telemetry.ts", "anchor": "POST /api/telemetry/usage", "new": false, "imports": []any{}, "importedBy": []any{}},
				"apps/api/src/server.ts", // bare string already in lean form
			},
			"filesToRead": []any{
				map[string]any{"path": "apps/api/src/routes/telemetry.ts", "reason": "Add GET /api/telemetry/adoption-ratio"},
			},
			"depsGraph": map[string]any{
				"apps/api/src/routes/telemetry.ts": map[string]any{
					"forward": []any{"telemetry-schema.ts"},
					"reverse": []any{"API router"},
				},
			},
		},
	}

	if !projectExplorerRichToLean(taskMap) {
		t.Fatal("expected projection to return true (real edits)")
	}

	exp := taskMap["explorer"].(map[string]any)

	// filesModified: rich object → flat string.
	gotMod := exp["filesModified"].([]any)
	wantMod := []any{"apps/api/src/routes/telemetry.ts", "apps/api/src/server.ts"}
	if !reflect.DeepEqual(gotMod, wantMod) {
		t.Errorf("filesModified flatten mismatch: want %v, got %v", wantMod, gotMod)
	}

	// filesToRead: rich object → flat string.
	gotRead := exp["filesToRead"].([]any)
	wantRead := []any{"apps/api/src/routes/telemetry.ts"}
	if !reflect.DeepEqual(gotRead, wantRead) {
		t.Errorf("filesToRead flatten mismatch: want %v, got %v", wantRead, gotRead)
	}

	// depsGraph: forward/reverse → imports/importedBy.
	deps := exp["depsGraph"].(map[string]any)
	node := deps["apps/api/src/routes/telemetry.ts"].(map[string]any)
	if _, has := node["forward"]; has {
		t.Errorf("expected `forward` to be removed; got %v", node)
	}
	if _, has := node["reverse"]; has {
		t.Errorf("expected `reverse` to be removed; got %v", node)
	}
	if got, _ := node["imports"].([]any); !reflect.DeepEqual(got, []any{"telemetry-schema.ts"}) {
		t.Errorf("imports mismatch: got %v", got)
	}
	if got, _ := node["importedBy"].([]any); !reflect.DeepEqual(got, []any{"API router"}) {
		t.Errorf("importedBy mismatch: got %v", got)
	}
}

// BROWZER_EXPLORER_LEGACY_REJECT=1 disables the projection so the rich
// payload reaches the CUE validator unchanged (which will reject it).
// Operators / CI use the flag to detect drift in subagent prompts that
// emit the rich shape (the §2.4 audit relies on no-projection-needed).
func TestProjectExplorerRichToLean_DisabledByEnv(t *testing.T) {
	// t.Setenv handles cleanup automatically (Go 1.17+) and correctly
	// restores any pre-existing value — an explicit defer os.Unsetenv
	// would clobber that restoration.
	t.Setenv("BROWZER_EXPLORER_LEGACY_REJECT", "1")

	taskMap := map[string]any{
		"explorer": map[string]any{
			"filesModified": []any{
				map[string]any{"path": "apps/api/src/routes/telemetry.ts"},
			},
		},
	}
	if projectExplorerRichToLean(taskMap) {
		t.Fatal("expected projection to be disabled under BROWZER_EXPLORER_LEGACY_REJECT=1")
	}
	// The rich shape must still be there — projection skipped, not partial.
	exp := taskMap["explorer"].(map[string]any)
	mod := exp["filesModified"].([]any)
	if _, isObj := mod[0].(map[string]any); !isObj {
		t.Fatalf("expected filesModified[0] to remain a map (rich shape); got %T", mod[0])
	}
}

// No-op when the explorer payload is already lean — projection returns
// false and the audit line keeps explorerProjected suppressed.
func TestProjectExplorerRichToLean_NoOpOnLeanInput(t *testing.T) {
	t.Setenv("BROWZER_EXPLORER_LEGACY_REJECT", "")
	taskMap := map[string]any{
		"explorer": map[string]any{
			"filesModified": []any{"apps/api/src/server.ts"},
			"filesToRead":   []any{"apps/api/src/server.ts"},
			"depsGraph": map[string]any{
				"apps/api/src/server.ts": map[string]any{
					"imports":    []any{"a.ts"},
					"importedBy": []any{"b.ts"},
				},
			},
		},
	}
	if projectExplorerRichToLean(taskMap) {
		t.Fatal("expected projection to be a no-op on already-lean payload")
	}
}
