package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

func TestBatcher_FlushesUnsentEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer tr.Close()
	for i := 0; i < 3; i++ {
		_ = tr.Record(tracker.Event{
			TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-read",
			InputBytes: 100, OutputBytes: 20, SavedTokens: 20, SavingsPct: 80,
		})
	}

	calls := 0
	send := func(ctx context.Context, b []tracker.Bucket) error {
		calls++
		if len(b) != 1 {
			t.Fatalf("buckets = %d", len(b))
		}
		return nil
	}

	b := NewBatcher(tr, send, BatcherOptions{Interval: 50 * time.Millisecond, Threshold: 100})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls == 0 {
		t.Fatal("batcher never invoked send")
	}

	// After flush, unsent should be empty.
	_, ids, _ := tr.UnsentBuckets()
	if len(ids) != 0 {
		t.Fatalf("after flush, %d unsent ids remain", len(ids))
	}
}
