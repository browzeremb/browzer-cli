package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/api"
)

// TestPreflightJobsInFlight_NoneInFlight returns nil when the server
// reports zero pending+processing.
func TestPreflightJobsInFlight_NoneInFlight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspaceId":"ws-1","pending":0,"processing":0}`))
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "tok", 0)
	if err := preflightJobsInFlight(context.Background(), c, "ws-1", true); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestPreflightJobsInFlight_NonTTYErrors surfaces a structured error
// with --force hint when jobs are in flight and there is no TTY (the
// `quiet` arg short-circuits the same way: machine-readable output
// must not block on a confirm prompt).
func TestPreflightJobsInFlight_NonTTYErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspaceId":"ws-1","pending":3,"processing":2,"oldestEnqueuedAt":"2026-04-14T00:00:00Z"}`))
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "tok", 0)
	// quiet=true mimics --json/--save output mode and is treated as
	// non-interactive even when stdin happens to be a TTY.
	err := preflightJobsInFlight(context.Background(), c, "ws-1", true)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Fatalf("expected total count in message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force hint, got %q", err.Error())
	}
}

// TestPreflightJobsInFlight_ServerErrorIsSoftFail returns nil (with a
// warning) when the /jobs endpoint blows up — older servers have no
// such route, so the check must not become a hard prerequisite.
func TestPreflightJobsInFlight_ServerErrorIsSoftFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not found"}`))
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "tok", 0)
	if err := preflightJobsInFlight(context.Background(), c, "ws-1", true); err != nil {
		t.Fatalf("expected nil on server error (soft fail), got %v", err)
	}
}
