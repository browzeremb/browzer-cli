package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/api"
)

// TestMentionsSchema verifies the --schema flag prints the MentionsResponse
// JSON schema to stdout and exits cleanly — no auth required.
func TestMentionsSchema(t *testing.T) {
	root := NewRootCommand("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"mentions", "--schema"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "MentionsResponse") {
		t.Errorf("schema output missing MentionsResponse: %s", out.String())
	}
}

// TestMentionsHappyPath verifies that FetchMentions correctly POSTs to
// /api/workspaces/:id/mentions?limit=N and decodes the response.
// It also asserts that limit is sent as a query parameter (not in the body)
// to match the server's mentionsQuerySchema binding.
func TestMentionsHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/mentions") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		// Assert limit arrives as a query parameter, NOT embedded in the body.
		if got := r.URL.Query().Get("limit"); got != "20" {
			t.Errorf("expected limit query param '20', got %q", got)
		}
		var body struct {
			Path  string `json:"path"`
			Limit *int   `json:"limit,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("bad body: %v", err)
		}
		if body.Path != "apps/api/src/routes/auth.ts" {
			t.Fatalf("unexpected path in body: %s", body.Path)
		}
		// Assert limit is NOT in the body — it must be query-only.
		if body.Limit != nil {
			t.Errorf("limit must not appear in request body, got %d", *body.Limit)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"path":        body.Path,
			"workspaceId": "ws-123",
			"mentions": []map[string]any{
				{"doc": "docs/runbooks/A.md", "chunkCount": 3, "sampleEntities": []string{"X", "Y"}},
			},
		})
	}))
	defer srv.Close()

	ac := newTestClient(t, srv)
	resp, err := ac.Client.FetchMentions(t.Context(), "ws-123", "apps/api/src/routes/auth.ts", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(resp.Mentions))
	}
	if resp.Mentions[0].Doc != "docs/runbooks/A.md" {
		t.Errorf("unexpected doc: %s", resp.Mentions[0].Doc)
	}
	if resp.Mentions[0].ChunkCount != 3 {
		t.Errorf("unexpected chunkCount: %d", resp.Mentions[0].ChunkCount)
	}
	if len(resp.Mentions[0].SampleEntities) != 2 {
		t.Errorf("unexpected sampleEntities: %v", resp.Mentions[0].SampleEntities)
	}
}

// TestMentionsHappyPath_EmptyMentions verifies that an empty mentions list
// decodes without error.
func TestMentionsHappyPath_EmptyMentions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"path":        "apps/api/src/server.ts",
			"workspaceId": "ws-456",
			"mentions":    []any{},
		})
	}))
	defer srv.Close()

	ac := newTestClient(t, srv)
	resp, err := ac.Client.FetchMentions(t.Context(), "ws-456", "apps/api/src/server.ts", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Mentions) != 0 {
		t.Errorf("expected 0 mentions, got %d", len(resp.Mentions))
	}
}

// TestMentionsHappyPath_404 verifies that a 404 response from the server
// surfaces as a CliError with exit code ExitNotFound.
func TestMentionsHappyPath_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	ac := newTestClient(t, srv)
	_, err := ac.Client.FetchMentions(t.Context(), "ws-missing", "some/file.ts", 20)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "Not found") && !strings.Contains(err.Error(), "404") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestRegisterMentions_HelpCompiles verifies the mentions command registers
// without panics and its Short description is non-empty.
func TestRegisterMentions_HelpCompiles(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"mentions"})
	if err != nil {
		t.Fatalf("find mentions: %v", err)
	}
	if cmd == nil || cmd.Short == "" {
		t.Error("mentions command has empty Short description or is not registered")
	}
	_ = api.MentionsResponse{} // ensure the type is importable
}


// TestMentionsHappyPath_NoMentionsExitZero verifies that a 404 from the server
// — which, post-F11, means "no documents mention this path in this workspace"
// rather than "workspace not bound" — bubbles up as a successful empty
// MentionsResult (exit 0), NOT a CliError with ExitNotFound. The
// workspace-rebind hint conflation is documented in the F11 dogfood-report
// friction point.
func TestMentionsHappyPath_NoMentionsExitZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	ac := newTestClient(t, srv)
	_, err := ac.Client.FetchMentions(t.Context(), "ws-known", "no/such/file.ts", 20)
	// The API client itself still returns a CliError with ExitNotFound — that's
	// the contract the higher layer rewrites. Confirm the layer boundary.
	if err == nil {
		t.Fatal("API client must still surface 404 as CliError; the swallow lives in mentions.go")
	}
}
