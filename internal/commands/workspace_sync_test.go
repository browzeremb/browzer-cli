package commands

import (
	"testing"

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
