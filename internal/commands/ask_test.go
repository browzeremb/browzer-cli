package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/spf13/cobra"
)

// newTestClient builds an api.AuthenticatedClient pointing at srv.
func newTestClient(t *testing.T, srv *httptest.Server) *api.AuthenticatedClient {
	t.Helper()
	return &api.AuthenticatedClient{
		Client: api.NewClient(srv.URL, "test-token", 5*time.Second),
	}
}

// stubRootCmd returns a minimal cobra.Command to pass into resolveWorkspaceID.
func stubRootCmd() *cobra.Command {
	root := NewRootCommand("test")
	// Traverse to the ask subcommand so flags are accessible.
	cmd, _, _ := root.Find([]string{"ask"})
	return cmd
}

// TestResolveWorkspaceID_FlagWins asserts that the --workspace flag
// short-circuits all other resolution paths.
func TestResolveWorkspaceID_FlagWins(t *testing.T) {
	// Server should never be called when the flag is provided.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call to %s", r.URL.Path)
	}))
	defer srv.Close()

	ac := newTestClient(t, srv)
	cmd := stubRootCmd()

	got, err := resolveWorkspaceID(cmd, ac, "explicit-ws-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "explicit-ws-id" {
		t.Errorf("got %q, want %q", got, "explicit-ws-id")
	}
}

// TestResolveWorkspaceID_APIFallback asserts that when neither the flag nor
// a .browzer/config.json is present, the command calls GET /api/workspaces
// and returns the first workspace ID.
func TestResolveWorkspaceID_APIFallback(t *testing.T) {
	wantID := "ws-from-api"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workspaces": []map[string]any{
				{"id": wantID, "name": "My Workspace"},
			},
		})
	}))
	defer srv.Close()

	// Change to a temp dir that is not inside a git repo so the config-file
	// fallback is skipped without error.
	t.Chdir(t.TempDir())

	ac := newTestClient(t, srv)
	cmd := stubRootCmd()

	got, err := resolveWorkspaceID(cmd, ac, "" /* no flag */)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantID {
		t.Errorf("got %q, want %q", got, wantID)
	}
}

// TestResolveWorkspaceID_AllFallbacksFail asserts that a helpful error is
// returned when the API returns no workspaces and there is no config file.
func TestResolveWorkspaceID_AllFallbacksFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspaces" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"workspaces": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())

	ac := newTestClient(t, srv)
	cmd := stubRootCmd()

	_, err := resolveWorkspaceID(cmd, ac, "")
	if err == nil {
		t.Fatal("expected an error when all fallbacks fail")
	}
	if !strings.Contains(err.Error(), "No workspace found") {
		t.Errorf("error message should mention 'No workspace found', got: %v", err)
	}
}

// TestResolveWorkspaceID_APIError asserts that an API failure during the
// fallback is surfaced as a wrapped error (not silently swallowed).
func TestResolveWorkspaceID_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())

	ac := newTestClient(t, srv)
	cmd := stubRootCmd()

	_, err := resolveWorkspaceID(cmd, ac, "")
	if err == nil {
		t.Fatal("expected an error when the API call fails")
	}
}

// TestFormatAskResponse_WithSources verifies that sources and cache-hit
// indicator are rendered in the human-readable output.
func TestFormatAskResponse_WithSources(t *testing.T) {
	resp := &api.AskResponse{
		Answer:   "The answer is 42.",
		CacheHit: true,
		Sources: []api.AskSource{
			{DocumentName: "docs/answer.md", Score: 0.91},
		},
	}
	got := formatAskResponse(resp)
	for _, want := range []string{"The answer is 42.", "docs/answer.md", "[answered from cache]"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatAskResponse output missing %q:\n%s", want, got)
		}
	}
}

// TestFormatAskResponse_NoSources verifies clean output when sources are empty.
func TestFormatAskResponse_NoSources(t *testing.T) {
	resp := &api.AskResponse{Answer: "Simple answer.", CacheHit: false}
	got := formatAskResponse(resp)
	if got != "Simple answer." {
		t.Errorf("unexpected output: %q", got)
	}
}

// TestRegisterAsk_HelpCompiles verifies the ask command registers without
// panics and its Short description is non-empty.
func TestRegisterAsk_HelpCompiles(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"ask"})
	if err != nil {
		t.Fatalf("find ask: %v", err)
	}
	if cmd.Short == "" {
		t.Error("ask command has empty Short description")
	}
}
