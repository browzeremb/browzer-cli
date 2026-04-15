package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

func TestIntegration_TrackerToServer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	// Fake server that counts received buckets.
	var received atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(202)
	}))
	defer srv.Close()

	// Insert 5 events.
	for i := 0; i < 5; i++ {
		_ = tr.Record(tracker.Event{
			TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-read",
			InputBytes: 100, OutputBytes: 20, SavedTokens: 20, SavingsPct: 80,
		})
	}

	sender := NewSender(srv.URL, "tok", "test")
	b := NewBatcher(tr, sender.Send, BatcherOptions{Interval: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if received.Load() == 0 {
		t.Fatal("server never received a batch")
	}
}
