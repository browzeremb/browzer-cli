package tracker

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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

	rows, err := tr.QueryAggregated("1d", "source")
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

	rows, _ := tr.QueryAggregated("365d", "source")
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
	rows, err := tr.QueryAggregated("1d", "source")
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
	rows, err := tr.QueryAggregated("1d", "source")
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
	rows, err := tr.QueryAggregated("365d", "source")
	if err != nil {
		t.Fatalf("QueryAggregated after Cleanup: %v", err)
	}
	if len(rows) != 1 || rows[0].N != 1 {
		t.Fatalf("expected 1 fresh row after Cleanup, got %#v", rows)
	}
}
