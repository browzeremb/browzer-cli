package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOpen_WritesSchemaVersionFlag pins FR-11 / AC-11: a successful Open
// must drop a `tracker.schema_v` sentinel containing currentSchemaVersion
// in the DB directory so subsequent Opens can skip the PRAGMA probe.
func TestOpen_WritesSchemaVersionFlag(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	tr, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = tr.Close()

	data, err := os.ReadFile(filepath.Join(dir, "tracker.schema_v"))
	if err != nil {
		t.Fatalf("schema flag missing after Open: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != currentSchemaVersion {
		t.Fatalf("schema flag mismatch: got %q, want %q", got, currentSchemaVersion)
	}
}

// TestOpen_RespectsExistingFlag pins the no-op case: when the flag is
// already written at currentSchemaVersion, Open must still succeed (no
// crash from skipping the PRAGMA) and the flag must remain untouched.
func TestOpen_RespectsExistingFlag(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "history.db")
	flagPath := filepath.Join(dir, "tracker.schema_v")

	// Pre-seed the flag so the second open path runs.
	tr, err := Open(dbPath)
	if err != nil {
		t.Fatalf("seed Open: %v", err)
	}
	_ = tr.Close()
	mt0, err := os.Stat(flagPath)
	if err != nil {
		t.Fatalf("flag not present after seed Open: %v", err)
	}

	// Re-open MUST NOT rewrite the flag (we'd touch its mtime).
	tr2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	_ = tr2.Close()
	mt1, err := os.Stat(flagPath)
	if err != nil {
		t.Fatalf("flag missing after re-Open: %v", err)
	}
	if !mt0.ModTime().Equal(mt1.ModTime()) {
		t.Fatalf("schema flag rewritten on re-Open: mtime %v → %v", mt0.ModTime(), mt1.ModTime())
	}
}

func TestTracker_RecordAndQuery(t *testing.T) {
	db := filepath.Join(t.TempDir(), "history.db")
	tr, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	ev := Event{
		TS:           time.Now().UTC().Format(time.RFC3339),
		Source:       "hook-read",
		Command:      "Read",
		InputBytes:   1000,
		OutputBytes:  300,
		SavedTokens:  175,
		SavingsPct:   70.0,
		FilterLevel:  ptr("aggressive"),
		ExecMs:       12,
		Model:        ptr("claude-opus-4-6"),
		SessionID:    ptr("s1"),
		FilterFailed: false,
	}
	if err := tr.Record(ev); err != nil {
		t.Fatalf("Record: %v", err)
	}

	rows, err := tr.QueryAggregated("1d", "source", false)
	if err != nil {
		t.Fatalf("QueryAggregated: %v", err)
	}
	if len(rows) != 1 || rows[0].Group != "hook-read" || rows[0].N != 1 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestTracker_RetentionDeletes90DayOldRows(t *testing.T) {
	db := filepath.Join(t.TempDir(), "history.db")
	tr, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	old := Event{
		TS:          time.Now().AddDate(0, 0, -100).UTC().Format(time.RFC3339),
		Source:      "cli",
		InputBytes:  10, OutputBytes: 5, SavedTokens: 1, SavingsPct: 50,
	}
	if err := tr.Record(old); err != nil {
		t.Fatal(err)
	}
	fresh := old
	fresh.TS = time.Now().UTC().Format(time.RFC3339)
	_ = tr.Record(fresh)
	// Cleanup is no longer called inside Record; invoke it explicitly.
	if err := tr.Cleanup(); err != nil {
		t.Fatal(err)
	}

	rows, _ := tr.QueryAggregated("365d", "source", false)
	if len(rows) != 1 || rows[0].N != 1 {
		t.Fatalf("retention failed: %#v", rows)
	}
}

func TestTracker_UnsentBatchesAndMarkFlushed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "history.db")
	tr, _ := Open(db)
	defer func() { _ = tr.Close() }()

	for range 3 {
		_ = tr.Record(Event{
			TS:          time.Now().UTC().Format(time.RFC3339),
			Source:      "hook-read",
			InputBytes:  100, OutputBytes: 20, SavedTokens: 20, SavingsPct: 80,
			FilterLevel: ptr("aggressive"),
			Model:       ptr("claude-opus-4-6"),
		})
	}

	buckets, ids, err := tr.UnsentBuckets()
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %#v", buckets)
	}
	if buckets[0].N != 3 {
		t.Fatalf("aggregated N = %d, want 3", buckets[0].N)
	}
	if err := tr.MarkFlushed(ids); err != nil {
		t.Fatal(err)
	}
	buckets2, _, _ := tr.UnsentBuckets()
	if len(buckets2) != 0 {
		t.Fatalf("after MarkFlushed, want 0 buckets; got %d", len(buckets2))
	}
}

func ptr[T any](v T) *T { return &v }

// TestEstimationMethod_RoundTrip verifies that EstimationMethod (FR-7) is
// persisted and read back via the public API.
//
//   - A non-nil method ('measured', 'estimated', 'counterfactual', 'unknown')
//     round-trips through SQLite as a *string in the resulting Bucket.
//   - A nil method records as NULL and surfaces as nil in the bucket.
func TestEstimationMethod_RoundTrip(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	if err := tr.Record(Event{
		TS: now, Source: "hook-read", Command: "Read",
		InputBytes: 100, OutputBytes: 20, SavedTokens: 80, SavingsPct: 80,
		EstimationMethod: ptr("measured"),
	}); err != nil {
		t.Fatalf("Record (measured): %v", err)
	}
	if err := tr.Record(Event{
		TS: now, Source: "hook-read", Command: "Read",
		InputBytes: 50, OutputBytes: 10, SavedTokens: 40, SavingsPct: 80,
		// EstimationMethod left nil — must persist as NULL.
	}); err != nil {
		t.Fatalf("Record (nil method): %v", err)
	}

	buckets, _, err := tr.UnsentBuckets()
	if err != nil {
		t.Fatalf("UnsentBuckets: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets (measured + nil), got %d: %#v", len(buckets), buckets)
	}
	var sawMeasured, sawNil bool
	for _, b := range buckets {
		switch {
		case b.EstimationMethod != nil && *b.EstimationMethod == "measured":
			sawMeasured = true
		case b.EstimationMethod == nil:
			sawNil = true
		}
	}
	if !sawMeasured || !sawNil {
		t.Fatalf("expected one measured + one nil bucket; got %#v", buckets)
	}
}

// TestEstimationMethod_GroupColumn verifies that groupColumn("method")
// resolves to the new estimation_method column (FR-9) and leaves the
// existing groupings intact.
func TestEstimationMethod_GroupColumn(t *testing.T) {
	col, err := groupColumn("method")
	if err != nil {
		t.Fatalf("groupColumn(method) returned error: %v", err)
	}
	if col != "estimation_method" {
		t.Fatalf("groupColumn(method) = %q, want estimation_method", col)
	}
	// Sanity: existing groupings unchanged.
	for _, g := range []string{"source", "command", "filter", "model", "session"} {
		if _, err := groupColumn(g); err != nil {
			t.Fatalf("groupColumn(%q) regressed: %v", g, err)
		}
	}
}

// TestEstimationMethod_BucketAggregation verifies that UnsentBuckets
// aggregates events along the estimation_method dimension: two 'measured'
// rows + one 'counterfactual' row collapse to two buckets per
// (day,source,filter,model).
func TestEstimationMethod_BucketAggregation(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	for range 2 {
		if err := tr.Record(Event{
			TS: now, Source: "hook-read",
			InputBytes: 100, OutputBytes: 20, SavedTokens: 80, SavingsPct: 80,
			FilterLevel: ptr("aggressive"), Model: ptr("claude-opus-4-7"),
			EstimationMethod: ptr("measured"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Record(Event{
		TS: now, Source: "hook-read",
		InputBytes: 50, OutputBytes: 10, SavedTokens: 40, SavingsPct: 80,
		FilterLevel: ptr("aggressive"), Model: ptr("claude-opus-4-7"),
		EstimationMethod: ptr("counterfactual"),
	}); err != nil {
		t.Fatal(err)
	}

	buckets, _, err := tr.UnsentBuckets()
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets after method-dimension split, got %d: %#v", len(buckets), buckets)
	}
	for _, b := range buckets {
		if b.EstimationMethod == nil {
			t.Fatalf("bucket has nil EstimationMethod despite all rows non-NULL: %#v", b)
		}
	}
}

// TestEstimationMethod_OpenIdempotent verifies that the column-add
// migration is idempotent: closing and re-Opening the same DB path does
// not error on the second pass (FR-7 acceptance).
func TestEstimationMethod_OpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	tr1, err := Open(path)
	if err != nil {
		t.Fatalf("Open#1: %v", err)
	}
	if err := tr1.Close(); err != nil {
		t.Fatalf("Close#1: %v", err)
	}
	tr2, err := Open(path)
	if err != nil {
		t.Fatalf("Open#2 (must be idempotent): %v", err)
	}
	defer func() { _ = tr2.Close() }()
}

// TestTrackerConcurrentRecord verifies F-26: tracker.Record is safe to call
// from multiple goroutines simultaneously — no data race, no "database is
// locked" error, and QueryAggregated returns exactly N recorded events.
func TestTrackerConcurrentRecord(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := Event{
				TS:          time.Now().UTC().Format(time.RFC3339),
				Source:      "hook-read",
				Command:     "Read",
				InputBytes:  100 + i,
				OutputBytes: 20 + i,
				SavedTokens: 10 + i,
				SavingsPct:  float64(50 + i),
			}
			if err := tr.Record(ev); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	// Assert no errors from concurrent writes.
	for err := range errs {
		t.Errorf("concurrent Record error: %v", err)
	}

	// Assert all N events were recorded.
	rows, err := tr.QueryAggregated("1d", "source", false)
	if err != nil {
		t.Fatalf("QueryAggregated: %v", err)
	}
	var total int
	for _, r := range rows {
		total += r.N
	}
	if total != goroutines {
		t.Fatalf("expected %d total events after concurrent writes, got %d", goroutines, total)
	}
}

// ── T-3 tests: one per public function ───────────────────────────────────────

// TestTrackerRecord verifies that Record inserts an event and no error is returned.
func TestTrackerRecord(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	ev := Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      "hook-read",
		Command:     "Read",
		InputBytes:  500,
		OutputBytes: 100,
		SavedTokens: 80,
		SavingsPct:  80.0,
		FilterLevel: ptr("aggressive"),
		ExecMs:      5,
		Model:       ptr("claude-opus-4-6"),
		SessionID:   ptr("sess-t3"),
	}
	if err := tr.Record(ev); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	// Verify the row is queryable.
	rows, err := tr.QueryAggregated("1d", "source", false)
	if err != nil {
		t.Fatalf("QueryAggregated: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 aggregated row, got %d", len(rows))
	}
	if rows[0].Group != "hook-read" {
		t.Fatalf("Group = %q, want hook-read", rows[0].Group)
	}
	if rows[0].N != 1 {
		t.Fatalf("N = %d, want 1", rows[0].N)
	}
}

// TestTrackerUnsentBuckets verifies that UnsentBuckets returns the events that
// have not yet been flushed, and returns no error.
func TestTrackerUnsentBuckets(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	for i := range 2 {
		if err := tr.Record(Event{
			TS:          now,
			Source:      "cli",
			InputBytes:  50,
			OutputBytes: 10,
			SavedTokens: 5,
			SavingsPct:  10.0,
		}); err != nil {
			t.Fatalf("Record[%d]: %v", i, err)
		}
	}

	buckets, ids, err := tr.UnsentBuckets()
	if err != nil {
		t.Fatalf("UnsentBuckets returned error: %v", err)
	}
	if len(buckets) == 0 {
		t.Fatal("expected at least one bucket, got 0")
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	// The two events collapse into a single bucket (same day+source+nil filter+nil model).
	if buckets[0].N != 2 {
		t.Fatalf("bucket N = %d, want 2", buckets[0].N)
	}
}

// TestTrackerMarkFlushed verifies that MarkFlushed clears the unsent rows and
// subsequent UnsentBuckets returns empty, with no error.
func TestTrackerMarkFlushed(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	_ = tr.Record(Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      "hook-read",
		InputBytes:  100,
		OutputBytes: 20,
		SavedTokens: 15,
		SavingsPct:  75.0,
	})

	_, ids, err := tr.UnsentBuckets()
	if err != nil {
		t.Fatalf("UnsentBuckets: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("expected unsent ids before flush")
	}

	if err := tr.MarkFlushed(ids); err != nil {
		t.Fatalf("MarkFlushed returned error: %v", err)
	}

	buckets2, ids2, err := tr.UnsentBuckets()
	if err != nil {
		t.Fatalf("UnsentBuckets after MarkFlushed: %v", err)
	}
	if len(buckets2) != 0 || len(ids2) != 0 {
		t.Fatalf("expected 0 unsent after MarkFlushed, got %d buckets / %d ids", len(buckets2), len(ids2))
	}
}

// TestParseSince_EmptyDefaultsTo7d verifies that an empty since string defaults
// to a 7-day window (parseSince is called from QueryAggregated).
func TestParseSince_EmptyDefaultsTo7d(t *testing.T) {
	got, err := parseSince("")
	if err != nil {
		t.Fatalf("parseSince(\"\") returned error: %v", err)
	}
	// Result must be roughly now minus 7 days — within 1 minute of tolerance.
	diff := time.Since(got)
	sevenDays := 7 * 24 * time.Hour
	if diff < sevenDays-time.Minute || diff > sevenDays+time.Minute {
		t.Errorf("parseSince(\"\") diff = %v, want ~7d", diff)
	}
}

// TestParseSince_HourUnit verifies that a "1h" since string produces a time ~1h ago.
func TestParseSince_HourUnit(t *testing.T) {
	got, err := parseSince("1h")
	if err != nil {
		t.Fatalf("parseSince(\"1h\") returned error: %v", err)
	}
	diff := time.Since(got)
	if diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("parseSince(\"1h\") diff = %v, want ~1h", diff)
	}
}

// TestParseSince_DayUnit verifies that a "2d" since string produces a time ~2d ago.
func TestParseSince_DayUnit(t *testing.T) {
	got, err := parseSince("2d")
	if err != nil {
		t.Fatalf("parseSince(\"2d\") returned error: %v", err)
	}
	diff := time.Since(got)
	twoDays := 2 * 24 * time.Hour
	if diff < twoDays-time.Minute || diff > twoDays+time.Minute {
		t.Errorf("parseSince(\"2d\") diff = %v, want ~2d", diff)
	}
}

// TestParseSince_BadInput verifies that a malformed since string returns an error.
func TestParseSince_BadInput(t *testing.T) {
	_, err := parseSince("x")
	if err == nil {
		t.Error("parseSince(\"x\"): expected error for len < 2, got nil")
	}
}

// TestParseSince_InvalidDayNum verifies that a non-numeric day component errors.
func TestParseSince_InvalidDayNum(t *testing.T) {
	_, err := parseSince("xd")
	if err == nil {
		t.Error("parseSince(\"xd\"): expected error for non-numeric day count, got nil")
	}
}

// TestParseSince_InvalidHourNum verifies that a non-numeric hour component errors.
func TestParseSince_InvalidHourNum(t *testing.T) {
	_, err := parseSince("xh")
	if err == nil {
		t.Error("parseSince(\"xh\"): expected error for non-numeric hour count, got nil")
	}
}

// TestBoolToInt_TrueIsOne asserts boolToInt(true) == 1.
func TestBoolToInt_TrueIsOne(t *testing.T) {
	if got := boolToInt(true); got != 1 {
		t.Errorf("boolToInt(true) = %d, want 1", got)
	}
}

// TestBoolToInt_FalseIsZero asserts boolToInt(false) == 0.
func TestBoolToInt_FalseIsZero(t *testing.T) {
	if got := boolToInt(false); got != 0 {
		t.Errorf("boolToInt(false) = %d, want 0", got)
	}
}

// TestBoolToInt_DistinctValues asserts true and false return distinct, different values.
func TestBoolToInt_DistinctValues(t *testing.T) {
	if boolToInt(true) == boolToInt(false) {
		t.Error("boolToInt: true and false must map to different integers")
	}
}

// TestHookSavedTokens_RoundTrip verifies that recording an event with
// source = HookSavedTokens and querying via QueryAggregated returns the row,
// and that QueryHookDeltas also surfaces it keyed by command.
func TestHookSavedTokens_RoundTrip(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	if err := tr.Record(Event{
		TS:          now,
		Source:      HookSavedTokens,
		Command:     "browzer-rewrite-read",
		InputBytes:  500,
		OutputBytes: 100,
		SavedTokens: 120,
		SavingsPct:  80.0,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// AggregatedRow round-trip via QueryAggregated grouped by source.
	rows, err := tr.QueryAggregated("1d", "source", false)
	if err != nil {
		t.Fatalf("QueryAggregated: %v", err)
	}
	if len(rows) != 1 || rows[0].Group != HookSavedTokens {
		t.Fatalf("QueryAggregated rows = %#v, want one row with Group=%q", rows, HookSavedTokens)
	}
	if rows[0].SavedTokens != 120 {
		t.Errorf("SavedTokens = %d, want 120", rows[0].SavedTokens)
	}

	// QueryHookDeltas returns the same delta keyed by command.
	hookRows, err := tr.QueryHookDeltas("1d")
	if err != nil {
		t.Fatalf("QueryHookDeltas: %v", err)
	}
	if len(hookRows) != 1 || hookRows[0].Hook != "browzer-rewrite-read" {
		t.Fatalf("QueryHookDeltas rows = %#v", hookRows)
	}
	if hookRows[0].SavedTokens != 120 {
		t.Errorf("hook SavedTokens = %d, want 120", hookRows[0].SavedTokens)
	}
}

// TestHookSavedTokens_MultipleHooks verifies that QueryHookDeltas aggregates
// across multiple hook commands independently.
func TestHookSavedTokens_MultipleHooks(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, ev := range []Event{
		{TS: now, Source: HookSavedTokens, Command: "hook-a", InputBytes: 100, SavedTokens: 50, SavingsPct: 50},
		{TS: now, Source: HookSavedTokens, Command: "hook-a", InputBytes: 100, SavedTokens: 60, SavingsPct: 60},
		{TS: now, Source: HookSavedTokens, Command: "hook-b", InputBytes: 200, SavedTokens: 80, SavingsPct: 40},
		// Different source — must NOT appear in QueryHookDeltas.
		{TS: now, Source: "hook-read", Command: "hook-a", InputBytes: 50, SavedTokens: 10, SavingsPct: 20},
	} {
		if err := tr.Record(ev); err != nil {
			t.Fatal(err)
		}
	}

	hookRows, err := tr.QueryHookDeltas("1d")
	if err != nil {
		t.Fatalf("QueryHookDeltas: %v", err)
	}
	if len(hookRows) != 2 {
		t.Fatalf("expected 2 hook rows, got %d: %#v", len(hookRows), hookRows)
	}
	byHook := map[string]int64{}
	for _, r := range hookRows {
		byHook[r.Hook] = r.SavedTokens
	}
	if byHook["hook-a"] != 110 {
		t.Errorf("hook-a SavedTokens = %d, want 110", byHook["hook-a"])
	}
	if byHook["hook-b"] != 80 {
		t.Errorf("hook-b SavedTokens = %d, want 80", byHook["hook-b"])
	}
}

// TestHookSavedTokens_Constant verifies the HookSavedTokens constant value
// is the exact string required by the hook layer.
func TestHookSavedTokens_Constant(t *testing.T) {
	if HookSavedTokens != "hook-saved-tokens" {
		t.Errorf("HookSavedTokens = %q, want %q", HookSavedTokens, "hook-saved-tokens")
	}
}

// TestHookSavedTokensConstant_StaysCanonical is the paired regression pin for
// F-021: the Go tracker bucket constant and the JS HOOK_SAVED_TOKENS_BUCKET
// export in hooks/_emit-hook-delta.mjs must equal the exact string
// "hook-saved-tokens". Any rename on either side must fail this test first,
// preventing a silent cross-language contract drift.
func TestHookSavedTokensConstant_StaysCanonical(t *testing.T) {
	const want = "hook-saved-tokens"
	if HookSavedTokens != want {
		t.Fatalf("HookSavedTokens constant drifted: got %q, want %q — update hooks/_emit-hook-delta.mjs HOOK_SAVED_TOKENS_BUCKET to match", HookSavedTokens, want)
	}
}

// TestHookSavedTokens_EmptyDB verifies that QueryHookDeltas on an empty DB
// returns an empty slice without error.
func TestHookSavedTokens_EmptyDB(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	rows, err := tr.QueryHookDeltas("7d")
	if err != nil {
		t.Fatalf("QueryHookDeltas empty DB: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows on empty DB, got %d", len(rows))
	}
}

// TestTrackerCleanup verifies that Cleanup runs without error and is idempotent
// (a second call on an already-clean DB also returns no error).
func TestTrackerCleanup(t *testing.T) {
	tr, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	// Add a mix of old and new rows.
	old := Event{
		TS:          time.Now().AddDate(0, 0, -100).UTC().Format(time.RFC3339),
		Source:      "cli",
		InputBytes:  10,
		OutputBytes: 5,
		SavedTokens: 1,
		SavingsPct:  50.0,
	}
	if err := tr.Record(old); err != nil {
		t.Fatalf("Record old: %v", err)
	}
	fresh := old
	fresh.TS = time.Now().UTC().Format(time.RFC3339)
	if err := tr.Record(fresh); err != nil {
		t.Fatalf("Record fresh: %v", err)
	}

	// First Cleanup — must not error.
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("Cleanup (first): %v", err)
	}

	// Idempotent — second call on already-clean DB must not error.
	if err := tr.Cleanup(); err != nil {
		t.Fatalf("Cleanup (second, idempotent): %v", err)
	}

	// Only the fresh row should remain.
	rows, err := tr.QueryAggregated("365d", "source", false)
	if err != nil {
		t.Fatalf("QueryAggregated after Cleanup: %v", err)
	}
	if len(rows) != 1 || rows[0].N != 1 {
		t.Fatalf("expected 1 fresh row after Cleanup, got %#v", rows)
	}
}
