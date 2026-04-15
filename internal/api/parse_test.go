package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/walker"
)

// TestGetWorkspaceJobs_HappyPath asserts the new PR 3 endpoint decodes
// the `{ pending, processing, oldestEnqueuedAt, workspaceId }` shape
// and threads workspaceID into the URL path.
func TestGetWorkspaceJobs_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/ws-1/jobs" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %q", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspaceId":"ws-1","pending":2,"processing":1,"oldestEnqueuedAt":"2026-04-14T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	resp, err := c.GetWorkspaceJobs(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Pending != 2 || resp.Processing != 1 {
		t.Fatalf("unexpected counts: %+v", resp)
	}
	if resp.OldestEnqueuedAt == "" {
		t.Fatalf("expected non-empty oldestEnqueuedAt")
	}
}

// TestGetWorkspaceJobs_NoneInFlight handles the steady-state response
// where the queue is empty.
func TestGetWorkspaceJobs_NoneInFlight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspaceId":"ws-1","pending":0,"processing":0,"oldestEnqueuedAt":null}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	resp, err := c.GetWorkspaceJobs(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Pending+resp.Processing != 0 {
		t.Fatalf("expected zero counts, got %+v", resp)
	}
}

// TestParseWorkspace_ForceHeader confirms ForceParse=true sets the
// X-Force-Parse: true header on the outbound request.
func TestParseWorkspace_ForceHeader(t *testing.T) {
	var gotForce string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotForce = r.Header.Get("X-Force-Parse")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspaceId":"ws-1","stats":{}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	_, err := c.ParseWorkspace(context.Background(),
		ParseWorkspaceRequest{
			WorkspaceID: "ws-1",
			RootPath:    "/p",
			Folders:     []walker.ParsedFolder{},
			Files:       []walker.ParsedFile{},
		},
		ParseWorkspaceOptions{ForceParse: true},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotForce != "true" {
		t.Fatalf("expected X-Force-Parse: true, got %q", gotForce)
	}
}

// TestParseWorkspace_NoForceHeader confirms the header is omitted when
// ForceParse is false (default).
func TestParseWorkspace_NoForceHeader(t *testing.T) {
	var gotForce string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotForce = r.Header.Get("X-Force-Parse")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	_, err := c.ParseWorkspace(context.Background(),
		ParseWorkspaceRequest{WorkspaceID: "ws-1", RootPath: "/p"},
		ParseWorkspaceOptions{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotForce != "" {
		t.Fatalf("expected no X-Force-Parse header, got %q", gotForce)
	}
}

// TestParseWorkspace_StatusUnchanged decodes the PR 3 fingerprint
// short-circuit response.
func TestParseWorkspace_StatusUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"unchanged","workspaceId":"ws-1","fingerprint":"deadbeef"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	resp, err := c.ParseWorkspace(context.Background(),
		ParseWorkspaceRequest{WorkspaceID: "ws-1", RootPath: "/p"},
		ParseWorkspaceOptions{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "unchanged" {
		t.Fatalf("expected status=unchanged, got %+v", resp)
	}
}

// TestParseWorkspace_409JobsInFlight maps a structured 409 envelope to
// the friendly CliError.
func TestParseWorkspace_409JobsInFlight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"jobs_in_flight","pending":2,"processing":1,"message":"Ingestion still mid-flight"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	_, err := c.ParseWorkspace(context.Background(),
		ParseWorkspaceRequest{WorkspaceID: "ws-1", RootPath: "/p"},
		ParseWorkspaceOptions{},
	)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Ingestion") &&
		!strings.Contains(err.Error(), "jobs") &&
		!strings.Contains(err.Error(), "force") {
		t.Fatalf("expected jobs-in-flight wording, got %q", err.Error())
	}
}

// TestParseWorkspace_429ParseCooldown surfaces a parse_cooldown
// envelope as a rate-limit error with the Retry-After delta.
func TestParseWorkspace_429ParseCooldown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "27")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"parse_cooldown","retryAfter":27}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	_, err := c.ParseWorkspace(context.Background(),
		ParseWorkspaceRequest{WorkspaceID: "ws-1", RootPath: "/p"},
		ParseWorkspaceOptions{},
	)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	cliErr, ok := err.(*cliErrors.CliError)
	if !ok {
		t.Fatalf("expected *cliErrors.CliError, got %T", err)
	}
	if cliErr.ExitCode != cliErrors.ExitRateLimit {
		t.Fatalf("expected exit code %d (rate-limit), got %d", cliErrors.ExitRateLimit, cliErr.ExitCode)
	}
	if !strings.Contains(err.Error(), "27") {
		t.Fatalf("expected message to include retry-after seconds, got %q", err.Error())
	}
	// Reference unused imports
	_ = io.Discard
	_ = json.Marshal
}
