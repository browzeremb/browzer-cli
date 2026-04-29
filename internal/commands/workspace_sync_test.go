package commands

import (
	"context"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/walker"
)

// ---------------------------------------------------------------------------
// applySyncSelection tests
// ---------------------------------------------------------------------------

// TestApplySyncSelection_Empty verifies that an empty input produces an
// empty output without panicking (edge case, case 1).
func TestApplySyncSelection_Empty(t *testing.T) {
	out := applySyncSelection(nil)
	if len(out) != 0 {
		t.Fatalf("expected empty result, got %d items", len(out))
	}
}

// TestApplySyncSelection_LocalOnlyGetsSelected verifies that a local-only
// item (Indexed=false, HasLocal=true) is flipped to Selected=true (ADD class,
// case 2).
func TestApplySyncSelection_LocalOnlyGetsSelected(t *testing.T) {
	items := []DocPickerItem{
		{
			RelativePath: "docs/new.md",
			LocalAbs:     "/repo/docs/new.md",
			LocalHash:    "abc",
			Indexed:      false,
			Selected:     false, // default from mergeDocItems for new items
		},
	}
	out := applySyncSelection(items)
	if !out[0].Selected {
		t.Error("expected Selected=true for local-only item (ADD class)")
	}
}

// TestApplySyncSelection_IndexedNoLocalGetsDeselected verifies that a
// server-indexed item without a local file (Indexed=true, HasLocal=false)
// is deselected so it lands in ToDelete (case 3).
func TestApplySyncSelection_IndexedNoLocalGetsDeselected(t *testing.T) {
	items := []DocPickerItem{
		{
			RelativePath:     "docs/deleted.md",
			LocalAbs:         "", // no local file
			ServerDocumentID: "srv-123",
			Indexed:          true,
			Selected:         true, // default from mergeDocItems
		},
	}
	out := applySyncSelection(items)
	if out[0].Selected {
		t.Error("expected Selected=false for indexed item with no local file (DELETE class)")
	}
}

// TestApplySyncSelection_IndexedWithLocalKeepsSelected verifies that an
// already-indexed item that still exists locally stays Selected=true (KEEP/UPDATE
// class, case 4).
func TestApplySyncSelection_IndexedWithLocalKeepsSelected(t *testing.T) {
	items := []DocPickerItem{
		{
			RelativePath:     "docs/existing.md",
			LocalAbs:         "/repo/docs/existing.md",
			LocalHash:        "hashXYZ",
			ServerDocumentID: "srv-456",
			Indexed:          true,
			Selected:         true,
		},
	}
	out := applySyncSelection(items)
	if !out[0].Selected {
		t.Error("expected Selected=true for indexed + local item (KEEP/UPDATE class)")
	}
}

// TestApplySyncSelection_MixedItems verifies correct classification when a
// slice contains items from all three classes simultaneously (case 5).
func TestApplySyncSelection_MixedItems(t *testing.T) {
	items := []DocPickerItem{
		// ADD class
		{RelativePath: "docs/new.md", LocalAbs: "/repo/docs/new.md", Indexed: false, Selected: false},
		// DELETE class
		{RelativePath: "docs/gone.md", LocalAbs: "", ServerDocumentID: "s1", Indexed: true, Selected: true},
		// KEEP class
		{RelativePath: "docs/same.md", LocalAbs: "/repo/docs/same.md", ServerDocumentID: "s2", Indexed: true, Selected: true},
	}
	out := applySyncSelection(items)
	// ADD: Selected must be true.
	if !out[0].Selected {
		t.Error("ADD item: expected Selected=true")
	}
	// DELETE: Selected must be false.
	if out[1].Selected {
		t.Error("DELETE item: expected Selected=false")
	}
	// KEEP: Selected must be true.
	if !out[2].Selected {
		t.Error("KEEP item: expected Selected=true")
	}
}

// ---------------------------------------------------------------------------
// computeDocDelta integration with ADD class
// ---------------------------------------------------------------------------

// TestComputeDocDelta_AddClass verifies that a local-only Selected=true item
// ends up in ToInsert (case 6 — the ADD class round-trip through computeDocDelta).
func TestComputeDocDelta_AddClass(t *testing.T) {
	items := []DocPickerItem{
		{
			RelativePath: "docs/new.md",
			LocalAbs:     "/repo/docs/new.md",
			LocalHash:    "hash1",
			Indexed:      false,
			Selected:     true, // set by applySyncSelection
		},
	}
	emptyCache := cache.DocsCache{Files: make(map[string]cache.CachedDoc)}
	plan := computeDocDelta(items, emptyCache)
	if len(plan.ToInsert) != 1 {
		t.Fatalf("expected 1 ToInsert item, got %d", len(plan.ToInsert))
	}
	if plan.ToInsert[0].RelativePath != "docs/new.md" {
		t.Errorf("unexpected ToInsert path: %s", plan.ToInsert[0].RelativePath)
	}
	if len(plan.ToDelete) != 0 || len(plan.ToReUpload) != 0 {
		t.Errorf("unexpected non-empty ToDelete/ToReUpload for pure ADD plan")
	}
}

// TestComputeDocDelta_DeleteClass verifies that a deselected server item ends
// up in ToDelete (case 7 — DELETE class round-trip).
func TestComputeDocDelta_DeleteClass(t *testing.T) {
	items := []DocPickerItem{
		{
			RelativePath:     "docs/old.md",
			LocalAbs:         "",
			ServerDocumentID: "srv-old",
			Indexed:          true,
			Selected:         false, // set by applySyncSelection (no local file)
		},
	}
	emptyCache := cache.DocsCache{Files: make(map[string]cache.CachedDoc)}
	plan := computeDocDelta(items, emptyCache)
	if len(plan.ToDelete) != 1 {
		t.Fatalf("expected 1 ToDelete item, got %d", len(plan.ToDelete))
	}
	if len(plan.ToInsert) != 0 || len(plan.ToReUpload) != 0 {
		t.Errorf("unexpected non-empty ToInsert/ToReUpload for pure DELETE plan")
	}
}

// ---------------------------------------------------------------------------
// Threshold helper tests
// ---------------------------------------------------------------------------

// exceedsThreshold encapsulates the gate logic so it can be tested without
// running the full RunE. It mirrors the inline check in workspace_sync.go.
func exceedsThreshold(plan DocDeltaPlan, confirmAdds, confirmDeletes int, yes bool) (string, bool) {
	if yes {
		return "", false
	}
	if len(plan.ToInsert) > confirmAdds {
		return "adds", true
	}
	if len(plan.ToDelete) > confirmDeletes {
		return "deletes", true
	}
	return "", false
}

// TestThreshold_AddExceeded verifies that the gate trips when ToInsert > confirmAdds
// and --yes is false (case 8).
func TestThreshold_AddExceeded(t *testing.T) {
	plan := DocDeltaPlan{}
	for range 51 {
		plan.ToInsert = append(plan.ToInsert, DocPickerItem{
			RelativePath: "docs/new.md",
			LocalAbs:     "/repo/docs/new.md",
		})
	}
	reason, tripped := exceedsThreshold(plan, 50, 50, false)
	if !tripped {
		t.Error("expected threshold to trip for 51 adds with limit 50")
	}
	if reason != "adds" {
		t.Errorf("expected reason=adds, got %q", reason)
	}
}

// TestThreshold_DeleteExceeded verifies that the gate trips when ToDelete > confirmDeletes
// and --yes is false (case 9).
func TestThreshold_DeleteExceeded(t *testing.T) {
	plan := DocDeltaPlan{}
	for range 51 {
		plan.ToDelete = append(plan.ToDelete, DocPickerItem{
			RelativePath:     "docs/old.md",
			ServerDocumentID: "srv",
			Indexed:          true,
		})
	}
	reason, tripped := exceedsThreshold(plan, 50, 50, false)
	if !tripped {
		t.Error("expected threshold to trip for 51 deletes with limit 50")
	}
	if reason != "deletes" {
		t.Errorf("expected reason=deletes, got %q", reason)
	}
}

// TestThreshold_YesBypasses verifies that --yes bypasses both thresholds even
// when both counts exceed their limits (case 10).
func TestThreshold_YesBypasses(t *testing.T) {
	plan := DocDeltaPlan{}
	for range 100 {
		plan.ToInsert = append(plan.ToInsert, DocPickerItem{LocalAbs: "/x"})
		plan.ToDelete = append(plan.ToDelete, DocPickerItem{Indexed: true})
	}
	_, tripped := exceedsThreshold(plan, 50, 50, true)
	if tripped {
		t.Error("expected threshold NOT to trip when yes=true")
	}
}

// TestThreshold_WithinLimits verifies that the gate does not trip when both
// counts are exactly at (not exceeding) their thresholds (case 11).
func TestThreshold_WithinLimits(t *testing.T) {
	plan := DocDeltaPlan{}
	for range 50 {
		plan.ToInsert = append(plan.ToInsert, DocPickerItem{LocalAbs: "/x"})
		plan.ToDelete = append(plan.ToDelete, DocPickerItem{Indexed: true})
	}
	_, tripped := exceedsThreshold(plan, 50, 50, false)
	if tripped {
		t.Error("expected threshold NOT to trip when counts equal the limit (not exceeding)")
	}
}

// ---------------------------------------------------------------------------
// mergeDocItems + applySyncSelection round-trip
// ---------------------------------------------------------------------------

// TestMergeAndApply_NewLocalFileAdded verifies the full merge→apply→delta
// pipeline for a new local file: mergeDocItems defaults Selected=false,
// applySyncSelection flips it to true, computeDocDelta puts it in ToInsert
// (case 12).
func TestMergeAndApply_NewLocalFileAdded(t *testing.T) {
	local := []walker.DocFile{
		{RelativePath: "docs/new.md", AbsolutePath: "/repo/docs/new.md", SHA256: "h1", Size: 100},
	}
	// No server docs — file was never indexed.
	items := mergeDocItems(local, nil)
	items = applySyncSelection(items)
	plan := computeDocDelta(items, cache.DocsCache{Files: make(map[string]cache.CachedDoc)})
	if len(plan.ToInsert) != 1 {
		t.Fatalf("expected ToInsert=1, got %d", len(plan.ToInsert))
	}
}

// ---------------------------------------------------------------------------
// Bidirectional workspace manifest reconciliation tests (AC-5-cli)
// ---------------------------------------------------------------------------

// mockSyncManifest is an in-memory WorkspaceSyncManifest for testing.
type mockSyncManifest struct {
	entries map[string]cache.ManifestEntry
}

func newMockManifest(initial ...cache.ManifestEntry) *mockSyncManifest {
	m := &mockSyncManifest{entries: make(map[string]cache.ManifestEntry)}
	for _, e := range initial {
		m.entries[e.ID] = e
	}
	return m
}

func (m *mockSyncManifest) Get(id string) (cache.ManifestEntry, bool) {
	e, ok := m.entries[id]
	return e, ok
}
func (m *mockSyncManifest) Upsert(entry cache.ManifestEntry) {
	m.entries[entry.ID] = entry
}
func (m *mockSyncManifest) Remove(id string) {
	delete(m.entries, id)
}
func (m *mockSyncManifest) All() []cache.ManifestEntry {
	out := make([]cache.ManifestEntry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	return out
}

// mockSyncClient is an in-memory WorkspaceSyncClient for testing. It records
// which PATCH and DELETE calls were made so tests can assert on them.
type mockSyncClient struct {
	patchCalls  []struct{ id, name, rootPath string }
	deleteCalls []string
}

func (c *mockSyncClient) UpdateWorkspace(_ context.Context, id, name, rootPath string) error {
	c.patchCalls = append(c.patchCalls, struct{ id, name, rootPath string }{id, name, rootPath})
	return nil
}
func (c *mockSyncClient) DeleteWorkspace(_ context.Context, id string) error {
	c.deleteCalls = append(c.deleteCalls, id)
	return nil
}

// TestWorkspaceSync_ServerRename_UpdatesLocalManifest (T-1 / AC-5-cli):
// When the server renames a workspace (Name differs), ReconcileWorkspaceManifest
// must update the local manifest entry with the server's new name (last-writer-wins pull).
func TestWorkspaceSync_ServerRename_UpdatesLocalManifest(t *testing.T) {
	manifest := newMockManifest(cache.ManifestEntry{
		ID:        "ws-1",
		Name:      "old-name",
		RootPath:  "/repo",
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	})
	client := &mockSyncClient{}

	serverList := []api.WorkspaceDto{
		{ID: "ws-1", Name: "new-name", RootPath: "/repo"},
	}

	if err := ReconcileWorkspaceManifest(context.Background(), client, manifest, serverList); err != nil {
		t.Fatalf("ReconcileWorkspaceManifest returned error: %v", err)
	}

	entry, ok := manifest.Get("ws-1")
	if !ok {
		t.Fatal("expected manifest entry ws-1 to exist after reconciliation")
	}
	if entry.Name != "new-name" {
		t.Errorf("expected Name=%q, got %q (server rename should win)", "new-name", entry.Name)
	}
}

// TestWorkspaceSync_ServerDelete_RemovesFromManifest (T-2 / AC-5-cli):
// When the server no longer lists a workspace that exists in the local manifest,
// ReconcileWorkspaceManifest must remove it from the manifest.
func TestWorkspaceSync_ServerDelete_RemovesFromManifest(t *testing.T) {
	manifest := newMockManifest(cache.ManifestEntry{
		ID:        "ws-gone",
		Name:      "was-here",
		RootPath:  "/repo",
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	})
	client := &mockSyncClient{}

	// Server returns an empty list — ws-gone was deleted server-side.
	serverList := []api.WorkspaceDto{}

	if err := ReconcileWorkspaceManifest(context.Background(), client, manifest, serverList); err != nil {
		t.Fatalf("ReconcileWorkspaceManifest returned error: %v", err)
	}

	if _, ok := manifest.Get("ws-gone"); ok {
		t.Error("expected manifest entry ws-gone to be removed after server-side delete")
	}
}

// TestWorkspaceSync_LocalRename_PatchesServer (T-3 / AC-5-cli):
// When a manifest entry has LocallyModified=true (but PendingDelete=false),
// ReconcileWorkspaceManifest must call UpdateWorkspace on the client, clear
// the LocallyModified flag in the manifest, and preserve the new name.
func TestWorkspaceSync_LocalRename_PatchesServer(t *testing.T) {
	client := &mockSyncClient{}

	manifest := newMockManifest(cache.ManifestEntry{
		ID:              "ws-2",
		Name:            "renamed-locally",
		RootPath:        "/repo",
		UpdatedAt:       time.Now().Add(-1 * time.Hour),
		LocallyModified: true,
		PendingDelete:   false,
	})

	// Server reflects the new name (what the server returns after a successful
	// PATCH in production). The push path fires first via LocallyModified, then
	// the pull pass sees name already matches and makes no further change.
	serverList := []api.WorkspaceDto{
		{ID: "ws-2", Name: "renamed-locally", RootPath: "/repo"},
	}

	ctx := context.Background()
	if err := ReconcileWorkspaceManifest(ctx, client, manifest, serverList); err != nil {
		t.Fatalf("ReconcileWorkspaceManifest returned error: %v", err)
	}

	// Verify UpdateWorkspace was called with the locally-renamed name.
	if len(client.patchCalls) != 1 {
		t.Fatalf("expected 1 UpdateWorkspace call, got %d", len(client.patchCalls))
	}
	call := client.patchCalls[0]
	if call.id != "ws-2" {
		t.Errorf("expected UpdateWorkspace id=ws-2, got %q", call.id)
	}
	if call.name != "renamed-locally" {
		t.Errorf("expected UpdateWorkspace name=renamed-locally, got %q", call.name)
	}

	// Verify LocallyModified flag is cleared in the manifest.
	entry, ok := manifest.Get("ws-2")
	if !ok {
		t.Fatal("expected manifest entry ws-2 to exist after reconciliation")
	}
	if entry.LocallyModified {
		t.Error("expected LocallyModified=false after successful push")
	}
	if entry.Name != "renamed-locally" {
		t.Errorf("expected Name=renamed-locally in manifest, got %q", entry.Name)
	}

	// Verify no spurious DeleteWorkspace calls.
	if len(client.deleteCalls) != 0 {
		t.Errorf("expected 0 DeleteWorkspace calls, got %d", len(client.deleteCalls))
	}
}

// TestWorkspaceSync_LocalDelete_CallsDeleteAndRemovesManifest (T-4 / AC-5-cli):
// When a manifest entry has LocallyModified=true and PendingDelete=true,
// ReconcileWorkspaceManifest must call DeleteWorkspace on the client and
// remove the entry from the manifest.
func TestWorkspaceSync_LocalDelete_CallsDeleteAndRemovesManifest(t *testing.T) {
	client := &mockSyncClient{}

	manifest := newMockManifest(cache.ManifestEntry{
		ID:              "ws-3",
		Name:            "to-delete",
		RootPath:        "/repo",
		UpdatedAt:       time.Now().Add(-1 * time.Hour),
		LocallyModified: true,
		PendingDelete:   true,
	})

	// Server does not list the workspace — simulates the state after a
	// successful DELETE. The push path calls DeleteWorkspace on the client
	// (which removes it from the server), then the pull pass sees an empty list
	// and makes no change (entry already removed from manifest in push pass).
	serverList := []api.WorkspaceDto{}

	ctx := context.Background()
	if err := ReconcileWorkspaceManifest(ctx, client, manifest, serverList); err != nil {
		t.Fatalf("ReconcileWorkspaceManifest returned error: %v", err)
	}

	// Verify DeleteWorkspace was called.
	if len(client.deleteCalls) != 1 {
		t.Fatalf("expected 1 DeleteWorkspace call, got %d", len(client.deleteCalls))
	}
	if client.deleteCalls[0] != "ws-3" {
		t.Errorf("expected DeleteWorkspace id=ws-3, got %q", client.deleteCalls[0])
	}

	// Verify the manifest entry was removed.
	if _, ok := manifest.Get("ws-3"); ok {
		t.Error("expected manifest entry ws-3 to be removed after local delete")
	}

	// Verify no spurious UpdateWorkspace calls.
	if len(client.patchCalls) != 0 {
		t.Errorf("expected 0 UpdateWorkspace calls, got %d", len(client.patchCalls))
	}
}
