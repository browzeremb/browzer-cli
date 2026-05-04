package schema

import "testing"

// TestMutationKill_PlaceholderSurface documents the mutation-killer test
// surface for the WF-CLI-VALIDATE-1 + WF-CLI-DISPATCH-1 scopes. Real killer
// tests are added in follow-up commits AFTER the first nightly go-mutesting
// run on main surfaces specific surviving mutants.
//
// Why a placeholder: this PR ships the mutation-testing infrastructure
// (Makefile target + nightly workflow). The first run produces a baseline
// mutant-kill rate; surviving mutants then drive killer tests. Pre-emptively
// writing killer tests here without a baseline would be cargo-cult.
//
// QA-001 (2026-05-04): changed from a no-op `t.Log` (which always passes
// silently and creates a false signal that mutation coverage exists) to
// an explicit `t.Skip` so CI output makes the pending state visible.
//
// Tracked in docs/CHANGELOG.md as TEST-MUT-1 absorbed by WF-SYNC-1, and
// referenced by WF-MUTATION-TEST-1 in docs/TECHNICAL_DEBTS.md.
func TestMutationKill_PlaceholderSurface(t *testing.T) {
	t.Skip("WF-MUTATION-TEST-1: baseline pending first nightly run; see docs/CHANGELOG.md")
}
