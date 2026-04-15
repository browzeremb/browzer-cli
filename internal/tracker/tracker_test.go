package tracker

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTracker_RecordAndQuery(t *testing.T) {
	db := filepath.Join(t.TempDir(), "history.db")
	tr, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

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
	defer tr.Close()

	old := Event{
		TS:          time.Now().AddDate(0, 0, -100).UTC().Format(time.RFC3339),
		Source:      "cli",
		InputBytes:  10, OutputBytes: 5, SavedTokens: 1, SavingsPct: 50,
	}
	if err := tr.Record(old); err != nil {
		t.Fatal(err)
	}
	// Record a fresh row to trigger the cleanup.
	fresh := old
	fresh.TS = time.Now().UTC().Format(time.RFC3339)
	_ = tr.Record(fresh)

	rows, _ := tr.QueryAggregated("365d", "source")
	if len(rows) != 1 || rows[0].N != 1 {
		t.Fatalf("retention failed: %#v", rows)
	}
}

func TestTracker_UnsentBatchesAndMarkFlushed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "history.db")
	tr, _ := Open(db)
	defer tr.Close()

	for i := 0; i < 3; i++ {
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
