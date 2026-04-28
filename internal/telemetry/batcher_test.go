package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

func TestBatcher_FlushesUnsentEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	for range 3 {
		_ = tr.Record(tracker.Event{
			TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-read",
			InputBytes: 100, OutputBytes: 20, SavedTokens: 20, SavingsPct: 80,
		})
	}

	var calls atomic.Int32
	send := func(ctx context.Context, b []tracker.Bucket) error {
		calls.Add(1)
		if len(b) != 1 {
			t.Errorf("buckets = %d", len(b))
		}
		return nil
	}

	b := NewBatcher(tr, send, BatcherOptions{Interval: 50 * time.Millisecond, Threshold: 100})
	ctx := t.Context()
	go b.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("batcher never invoked send")
	}

	// After flush, unsent should be empty.
	_, ids, _ := tr.UnsentBuckets()
	if len(ids) != 0 {
		t.Fatalf("after flush, %d unsent ids remain", len(ids))
	}
}

// TestBatcherAddAndDrain verifies that a Batcher with a fast interval drains
// all unsent events from the tracker via the send function. This is a
// higher-level "add → drain" contract test that complements TestBatcher_FlushesUnsentEvents.
func TestBatcherAddAndDrain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "add-drain.db")
	tr, err := tracker.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	// Add events from two distinct sources so we get two buckets.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range 5 {
		src := "hook-read"
		if i%2 == 0 {
			src = "cli"
		}
		_ = tr.Record(tracker.Event{
			TS: now, Source: src,
			InputBytes: 200, OutputBytes: 40, SavedTokens: 30, SavingsPct: 75.0,
		})
	}

	var sentBuckets atomic.Int32
	send := func(ctx context.Context, b []tracker.Bucket) error {
		sentBuckets.Add(int32(len(b)))
		return nil
	}

	batcher := NewBatcher(tr, send, BatcherOptions{
		Interval:  20 * time.Millisecond,
		Threshold: 100,
	})
	ctx := t.Context()
	go batcher.Run(ctx)

	// Wait until all events are drained.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, ids, _ := tr.UnsentBuckets()
		if len(ids) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, remaining, _ := tr.UnsentBuckets()
	if len(remaining) != 0 {
		t.Fatalf("expected 0 unsent after drain, got %d ids", len(remaining))
	}
	// We had 2 distinct sources so expect 2 buckets in one or more send calls.
	if sentBuckets.Load() < 2 {
		t.Fatalf("expected ≥2 bucket(s) sent, got %d", sentBuckets.Load())
	}
}

// TestSenderSendBatch verifies that Sender.Send POSTs a valid JSON payload
// to the server and returns no error on HTTP 202. Uses net/http/httptest.
func TestSenderSendBatch(t *testing.T) {
	var receivedBody []byte
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sender := NewSender(srv.URL+"/api/telemetry/usage", "bearer-xyz", "v1.3.1")
	buckets := []tracker.Bucket{
		{Day: "2026-04-28", Source: "hook-read", N: 3, InputBytes: 900, OutputBytes: 150, SavedTokens: 200},
		{Day: "2026-04-28", Source: "cli", N: 1, InputBytes: 200, OutputBytes: 50, SavedTokens: 40},
	}
	if err := sender.Send(context.Background(), buckets); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Auth header must be present.
	if receivedAuth != "Bearer bearer-xyz" {
		t.Fatalf("Authorization = %q, want 'Bearer bearer-xyz'", receivedAuth)
	}

	// Payload must deserialize correctly.
	var payload struct {
		SchemaVersion int `json:"schemaVersion"`
		Buckets       []struct {
			Source string `json:"source"`
			N      int    `json:"n"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.SchemaVersion != 1 {
		t.Fatalf("schemaVersion = %d, want 1", payload.SchemaVersion)
	}
	if len(payload.Buckets) != 2 {
		t.Fatalf("len(buckets) = %d, want 2", len(payload.Buckets))
	}
}

// TestBatcher_SendErrorKeepsEventsUnsent verifies F-18: when the send function
// returns an error, the batcher must NOT mark the rows as flushed — they must
// remain in UnsentBuckets for the next flush attempt.
func TestBatcher_SendErrorKeepsEventsUnsent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h-err.db")
	tr, err := tracker.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	// Record one event.
	_ = tr.Record(tracker.Event{
		TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-read",
		InputBytes: 100, OutputBytes: 20, SavedTokens: 20, SavingsPct: 80,
	})

	var calls atomic.Int32
	sendErr := errors.New("simulated send failure")
	send := func(ctx context.Context, b []tracker.Bucket) error {
		calls.Add(1)
		return sendErr
	}

	b := NewBatcher(tr, send, BatcherOptions{Interval: 50 * time.Millisecond, Threshold: 100})
	ctx := t.Context()
	go b.Run(ctx)

	// Wait until at least one send attempt has been made.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("batcher never invoked send")
	}

	// After a failed send, the events must remain unsent (not marked flushed).
	_, ids, _ := tr.UnsentBuckets()
	if len(ids) == 0 {
		t.Fatal("expected unsent ids to remain after failed send, got 0 — batcher incorrectly marked events as flushed on error")
	}
}

// TestSenderSendBatch_ErrorOnNon202 verifies F-19: Sender.Send must return a
// non-nil error for non-202 responses (429, 500) and for context cancellation.
func TestSenderSendBatch_ErrorOnNon202(t *testing.T) {
	buckets := []tracker.Bucket{
		{Day: "2026-04-28", Source: "hook-read", N: 1, InputBytes: 100, OutputBytes: 20, SavedTokens: 10},
	}

	tests := []struct {
		name       string
		statusCode int
	}{
		{"HTTP 429 Too Many Requests", http.StatusTooManyRequests},
		{"HTTP 500 Internal Server Error", http.StatusInternalServerError},
		{"HTTP 400 Bad Request", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			sender := NewSender(srv.URL+"/api/telemetry/usage", "bearer-xyz", "v1.3.1")
			err := sender.Send(context.Background(), buckets)
			if err == nil {
				t.Fatalf("expected non-nil error for HTTP %d, got nil", tc.statusCode)
			}
		})
	}

	// Context cancellation must return a non-nil error.
	t.Run("context cancellation", func(t *testing.T) {
		// Use a server that delays responding so the context can be cancelled first.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Block until the request context is cancelled.
			<-r.Context().Done()
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		sender := NewSender(srv.URL+"/api/telemetry/usage", "bearer-xyz", "v1.3.1")
		ctx, cancel := context.WithCancel(context.Background())
		// Cancel immediately so the HTTP request fails.
		cancel()
		err := sender.Send(ctx, buckets)
		if err == nil {
			t.Fatal("expected non-nil error for cancelled context, got nil")
		}
	})
}
