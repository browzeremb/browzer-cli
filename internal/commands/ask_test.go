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
// indicator are rendered in the human-readable output (legacy shape, no positions).
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

// TestFormatAskResponse_WithPositions verifies the new B14/B15 format:
// "<doc>#chunk<N> (score <S>)" for each position entry.
func TestFormatAskResponse_WithPositions(t *testing.T) {
	resp := &api.AskResponse{
		Answer: "Cache pipeline explained.",
		Sources: []api.AskSource{
			{DocumentName: "packages/core/src/search/chain.ts", Score: 0.95, Positions: []int{2, 5}},
			{DocumentName: "apps/api/src/routes/ask.ts", Score: 0.87, Positions: []int{1}},
		},
	}
	got := formatAskResponse(resp)
	for _, want := range []string{
		"packages/core/src/search/chain.ts#chunk2 (score 0.950)",
		"packages/core/src/search/chain.ts#chunk5 (score 0.950)",
		"apps/api/src/routes/ask.ts#chunk1 (score 0.870)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatAskResponse output missing %q:\n%s", want, got)
		}
	}
}

// TestFormatAskResponse_PositionsFallback verifies that a source without
// Positions (older server) still renders gracefully without a "#chunk" suffix.
func TestFormatAskResponse_PositionsFallback(t *testing.T) {
	resp := &api.AskResponse{
		Answer: "Fallback answer.",
		Sources: []api.AskSource{
			{DocumentName: "README.md", Score: 0.80},
		},
	}
	got := formatAskResponse(resp)
	if !strings.Contains(got, "README.md (score 0.800)") {
		t.Errorf("expected fallback format, got:\n%s", got)
	}
	if strings.Contains(got, "#chunk") {
		t.Errorf("expected no #chunk in fallback format, got:\n%s", got)
	}
}

// TestFormatAskResponse_WithTimingAndSourceRefs verifies that AskResponse
// can carry timing and sourceRefs fields (they are printed only in JSON mode;
// the human formatter ignores them — this test asserts the struct is populated
// correctly when decoded from a mock server payload).
func TestFormatAskResponse_WithTimingAndSourceRefs(t *testing.T) {
	searchMs := 120
	resp := &api.AskResponse{
		Answer:     "Timing answer.",
		SourceRefs: []string{"1", "3"},
		Timing:     &api.AskTiming{Search: &searchMs},
		Sources: []api.AskSource{
			{DocumentName: "doc.md", Score: 0.9, Positions: []int{1}},
		},
	}
	if len(resp.SourceRefs) != 2 || resp.SourceRefs[0] != "1" || resp.SourceRefs[1] != "3" {
		t.Errorf("unexpected SourceRefs: %v", resp.SourceRefs)
	}
	if resp.Timing == nil || resp.Timing.Search == nil || *resp.Timing.Search != 120 {
		t.Errorf("unexpected Timing: %+v", resp.Timing)
	}
	got := formatAskResponse(resp)
	if !strings.Contains(got, "doc.md#chunk1") {
		t.Errorf("expected chunk format in output, got:\n%s", got)
	}
}

// TestAsk_ServerPayloadDecoding verifies that the CLI correctly decodes
// a B14/B15 server payload: deduped sources with positions, sourceRefs, timing.
func TestAsk_ServerPayloadDecoding(t *testing.T) {
	payload := map[string]any{
		"answer": "The cache uses pgvector.",
		"found":  true,
		"cached": false,
		"sourceRefs": []string{"1", "2"},
		"timing": map[string]any{
			"search": 95,
		},
		"sources": []map[string]any{
			{
				"documentName": "packages/core/src/search/answer-cache.ts",
				"score":        0.93,
				"positions":    []int{1, 3},
			},
			{
				"documentName": "apps/api/src/routes/ask.ts",
				"score":        0.81,
				"positions":    []int{2},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspaces":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workspaces": []map[string]any{{"id": "ws-test", "name": "Test"}},
			})
		case "/ask":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ac := newTestClient(t, srv)
	resp, err := ac.Client.Ask(t.Context(), api.AskRequest{Question: "How does the cache work?", WorkspaceID: "ws-test"})
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	// Assert no duplicate documentNames in sources.
	seen := map[string]bool{}
	for _, s := range resp.Sources {
		if seen[s.DocumentName] {
			t.Errorf("duplicate documentName in sources: %q", s.DocumentName)
		}
		seen[s.DocumentName] = true
	}

	// Assert positions populated.
	if len(resp.Sources) < 1 || len(resp.Sources[0].Positions) == 0 {
		t.Errorf("expected positions on first source, got: %+v", resp.Sources)
	}

	// Assert sourceRefs propagated.
	if len(resp.SourceRefs) != 2 {
		t.Errorf("expected 2 sourceRefs, got %d: %v", len(resp.SourceRefs), resp.SourceRefs)
	}

	// Assert timing.search populated.
	if resp.Timing == nil || resp.Timing.Search == nil {
		t.Errorf("expected timing.search, got: %+v", resp.Timing)
	} else if *resp.Timing.Search != 95 {
		t.Errorf("expected timing.search=95, got %d", *resp.Timing.Search)
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
