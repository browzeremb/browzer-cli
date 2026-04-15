package commands

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sampleItems returns a deterministic merged picker list covering the
// three interesting shapes: new (local-only), indexed-with-local, and
// ghost (server-only, no local bytes).
func sampleItems() []DocPickerItem {
	return []DocPickerItem{
		{RelativePath: "docs/a.md", LocalAbs: "/r/docs/a.md", LocalHash: "h-a", LocalSize: 10, Indexed: true, ServerDocumentID: "d-a", Selected: true},
		{RelativePath: "docs/b.md", LocalAbs: "/r/docs/b.md", LocalHash: "h-b", LocalSize: 20}, // new, not indexed
		{RelativePath: "docs/c.md", LocalAbs: "/r/docs/c.md", LocalHash: "h-c", LocalSize: 30, Indexed: true, ServerDocumentID: "d-c", Selected: true},
		{RelativePath: "ghost.md", Indexed: true, ServerDocumentID: "d-ghost", Selected: true}, // orphan
	}
}

// TestParseSpec_Sentinels covers every sentinel × scope combination,
// including explicit rejection of cross-scope use.
func TestParseSpec_Sentinels(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		scope   SpecScope
		wantErr bool
		sent    string
	}{
		{"add new", "new", SpecScopeAdd, false, "new"},
		{"replace all", "all", SpecScopeReplace, false, "all"},
		{"replace none", "none", SpecScopeReplace, false, "none"},
		{"remove rejects new", "new", SpecScopeRemove, true, ""},
		{"add rejects all", "all", SpecScopeAdd, true, ""},
		{"add rejects none", "none", SpecScopeAdd, true, ""},
		{"replace rejects new", "new", SpecScopeReplace, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseSpec(tc.raw, tc.scope)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Sentinel != tc.sent {
				t.Errorf("Sentinel = %q, want %q", r.Sentinel, tc.sent)
			}
		})
	}
}

// TestParseSpec_SentinelErrorMessagesByScope verifies the enriched
// sentinel-rejection messages carry the valid sentinels for that scope,
// so agents see an actionable "(accepted: …)" hint instead of a bare
// refusal.
func TestParseSpec_SentinelErrorMessagesByScope(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		scope       SpecScope
		wantContain string
	}{
		{"add rejects all lists new", "all", SpecScopeAdd, "accepted: new"},
		{"add rejects none lists new", "none", SpecScopeAdd, "accepted: new"},
		{"replace rejects new lists all,none", "new", SpecScopeReplace, "accepted: all, none"},
		{"remove rejects new says no sentinels accepted", "new", SpecScopeRemove, "no sentinels accepted"},
		{"remove rejects all says no sentinels accepted", "all", SpecScopeRemove, "no sentinels accepted"},
		{"remove rejects none says no sentinels accepted", "none", SpecScopeRemove, "no sentinels accepted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSpec(tc.raw, tc.scope)
			if err == nil {
				t.Fatalf("expected error for %q in scope %v", tc.raw, tc.scope)
			}
			if !strings.Contains(err.Error(), tc.wantContain) {
				t.Errorf("error %q missing expected hint %q", err.Error(), tc.wantContain)
			}
		})
	}
}

// TestParseSpec_CommaListWhitespace verifies whitespace trimming and
// blank-entry dropping in the comma-list fall-through.
func TestParseSpec_CommaListWhitespace(t *testing.T) {
	r, err := parseSpec("  docs/a.md ,  docs/b.md,,  ", SpecScopeAdd)
	if err != nil {
		t.Fatal(err)
	}
	items := sampleItems()
	matched, unresolved := r.Resolve(items)
	if len(matched) != 2 || !matched["docs/a.md"] || !matched["docs/b.md"] {
		t.Errorf("matched = %v", matched)
	}
	if len(unresolved) != 0 {
		t.Errorf("unresolved = %v", unresolved)
	}
}

// TestParseSpec_AtFile covers good, missing, and annotated file refs.
func TestParseSpec_AtFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.txt")
	if err := os.WriteFile(good, []byte("# comment\n\ndocs/a.md\n  docs/b.md  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := parseSpec("@"+good, SpecScopeAdd)
	if err != nil {
		t.Fatalf("good file: %v", err)
	}
	matched, _ := r.Resolve(sampleItems())
	if !matched["docs/a.md"] || !matched["docs/b.md"] {
		t.Errorf("matched = %v", matched)
	}

	if _, err := parseSpec("@"+filepath.Join(dir, "nope.txt"), SpecScopeAdd); err == nil {
		t.Errorf("missing file should error")
	}

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("# only comment\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseSpec("@"+empty, SpecScopeAdd); err == nil {
		t.Errorf("empty file should error")
	}
}

// TestParseSpec_Glob covers stdlib patterns and the explicit ** reject.
func TestParseSpec_Glob(t *testing.T) {
	items := sampleItems()

	r, err := parseSpec("docs/*.md", SpecScopeAdd)
	if err != nil {
		t.Fatal(err)
	}
	matched, _ := r.Resolve(items)
	if len(matched) != 3 { // docs/a.md, docs/b.md, docs/c.md
		t.Errorf("docs/*.md matched = %v", matched)
	}

	r2, err := parseSpec("docs/?.md", SpecScopeAdd)
	if err != nil {
		t.Fatal(err)
	}
	matched2, _ := r2.Resolve(items)
	if len(matched2) != 3 {
		t.Errorf("docs/?.md matched = %v", matched2)
	}

	if _, err := parseSpec("docs/**/*.md", SpecScopeAdd); err == nil {
		t.Errorf("** should be rejected")
	}
}

// TestParseSpec_Unresolved surfaces literal paths that don't exist in
// the item list — the caller's error-vs-warn branching depends on this.
func TestParseSpec_Unresolved(t *testing.T) {
	r, err := parseSpec("docs/a.md,docs/missing.md", SpecScopeAdd)
	if err != nil {
		t.Fatal(err)
	}
	_, unresolved := r.Resolve(sampleItems())
	if len(unresolved) != 1 || unresolved[0] != "docs/missing.md" {
		t.Errorf("unresolved = %v", unresolved)
	}
}

// TestApplySpecsToItems_Add unions new paths onto the indexed base.
func TestApplySpecsToItems_Add(t *testing.T) {
	add, _ := parseSpec("docs/b.md", SpecScopeAdd)
	res, out := applySpecsToItems(sampleItems(), add, nil, nil)
	if len(res.UnresolvedAdd) != 0 {
		t.Fatalf("unresolved = %v", res.UnresolvedAdd)
	}
	sel := selectedPaths(out)
	wantContains(t, sel, "docs/a.md", "docs/b.md", "docs/c.md", "ghost.md")
}

// TestApplySpecsToItems_AddNew picks up ONLY not-yet-indexed local
// files via the `new` sentinel.
func TestApplySpecsToItems_AddNew(t *testing.T) {
	add, _ := parseSpec("new", SpecScopeAdd)
	_, out := applySpecsToItems(sampleItems(), add, nil, nil)
	sel := selectedPaths(out)
	// Should add docs/b.md on top of the indexed base (a/c/ghost).
	wantContains(t, sel, "docs/a.md", "docs/b.md", "docs/c.md", "ghost.md")
}

// TestApplySpecsToItems_Remove deselects indexed paths; unresolved
// literals are reported for a stderr warning but are not fatal.
func TestApplySpecsToItems_Remove(t *testing.T) {
	rm, _ := parseSpec("docs/a.md,docs/missing.md", SpecScopeRemove)
	res, out := applySpecsToItems(sampleItems(), nil, rm, nil)
	if len(res.UnresolvedRemove) != 1 || res.UnresolvedRemove[0] != "docs/missing.md" {
		t.Errorf("UnresolvedRemove = %v", res.UnresolvedRemove)
	}
	sel := selectedPaths(out)
	wantContains(t, sel, "docs/c.md", "ghost.md")
	wantNotContains(t, sel, "docs/a.md")
}

// TestApplySpecsToItems_Replace drops everything and rebuilds the
// selection from the spec.
func TestApplySpecsToItems_Replace(t *testing.T) {
	rp, _ := parseSpec("docs/b.md", SpecScopeReplace)
	_, out := applySpecsToItems(sampleItems(), nil, nil, rp)
	sel := selectedPaths(out)
	if len(sel) != 1 || sel[0] != "docs/b.md" {
		t.Errorf("selected = %v", sel)
	}
}

// TestApplySpecsToItems_ReplaceNone clears every selection.
func TestApplySpecsToItems_ReplaceNone(t *testing.T) {
	rp, _ := parseSpec("none", SpecScopeReplace)
	_, out := applySpecsToItems(sampleItems(), nil, nil, rp)
	if len(selectedPaths(out)) != 0 {
		t.Errorf("expected empty selection, got %v", selectedPaths(out))
	}
}

// TestLargeDeleteSafeguard_Message verifies the refusal message lists
// every path and references the override flag. This is the one piece of
// RunE logic we can exercise without a full cobra harness — we replay
// the same plan + message construction here.
func TestLargeDeleteSafeguard_Message(t *testing.T) {
	var items []DocPickerItem
	for i := 0; i < 5; i++ {
		items = append(items, DocPickerItem{
			RelativePath:     strings.Repeat("x", i+1) + ".md",
			Indexed:          true,
			ServerDocumentID: "d",
			Selected:         false, // deselected → delete
		})
	}
	plan := computeDocDelta(items, defaultDocsCache())
	if len(plan.ToDelete) != 5 {
		t.Fatalf("expected 5 deletes, got %d", len(plan.ToDelete))
	}
	// Replicate the message used in workspace_docs.go RunE.
	msg := largeDeleteMessage(plan)
	if !strings.Contains(msg, "--i-know-what-im-doing") {
		t.Errorf("message missing override flag: %s", msg)
	}
	for _, d := range plan.ToDelete {
		if !strings.Contains(msg, d.RelativePath) {
			t.Errorf("message missing path %q: %s", d.RelativePath, msg)
		}
	}
}

// TestRegisterWorkspaceDocs_NewFlagsHaveHelp is a smoke check ensuring
// every new flag has non-empty help text; CodeRabbit sometimes strips
// flag descriptions during rebases.
func TestRegisterWorkspaceDocs_NewFlagsHaveHelp(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "docs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plan", "add", "remove", "replace", "yes", "dry-run", "i-know-what-im-doing", "json", "save"} {
		f := cmd.Flag(name)
		if f == nil {
			t.Errorf("flag %q not registered", name)
			continue
		}
		if f.Usage == "" {
			t.Errorf("flag %q has empty Usage", name)
		}
	}
}

// --- helpers ---

func selectedPaths(items []DocPickerItem) []string {
	var out []string
	for _, it := range items {
		if it.Selected {
			out = append(out, it.RelativePath)
		}
	}
	sort.Strings(out)
	return out
}

func wantContains(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

func wantNotContains(t *testing.T, got []string, bad ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, b := range bad {
		if set[b] {
			t.Errorf("unexpected %q in %v", b, got)
		}
	}
}
