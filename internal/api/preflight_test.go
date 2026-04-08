package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPreflight_Fits asserts the happy-path response decodes cleanly
// and the request body carries the expected PreflightFile slice.
func TestPreflight_Fits(t *testing.T) {
	var gotBody struct {
		Files []PreflightFile `json:"files"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ingestion/preflight" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %q", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fits":true,"projected_chunks":120,"projected_bytes":4096,"current_chunks":10,"limit_chunks":1000}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	resp, err := c.Preflight(context.Background(), []PreflightFile{
		{Path: "docs/a.md", SizeBytes: 2048},
		{Path: "docs/b.md", SizeBytes: 2048},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Fits || resp.ProjectedChunks != 120 || resp.LimitChunks != 1000 {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	if len(gotBody.Files) != 2 || gotBody.Files[0].Path != "docs/a.md" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
}

// TestPreflight_DoesNotFit confirms the server's `fits:false` response
// decodes including the human-readable reason.
func TestPreflight_DoesNotFit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fits":false,"projected_chunks":2000,"projected_bytes":99999,"current_chunks":950,"limit_chunks":1000,"reason":"free plan cap"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	resp, err := c.Preflight(context.Background(), []PreflightFile{{Path: "x.md", SizeBytes: 1}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Fits {
		t.Fatalf("expected fits=false")
	}
	if !strings.Contains(resp.Reason, "free plan") {
		t.Fatalf("expected reason, got %q", resp.Reason)
	}
}
