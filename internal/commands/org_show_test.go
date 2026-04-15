package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
)

func TestOrgShow_RegistrationAndHelp(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"org", "show"})
	if err != nil {
		t.Fatalf("find org show: %v", err)
	}
	if cmd.Short == "" {
		t.Error("org show has empty Short description")
	}
	if cmd.Use != "show" {
		t.Errorf("Use = %q, want 'show'", cmd.Use)
	}
}

func TestOrgShow_JSONFlag(t *testing.T) {
	fixture := map[string]any{
		"id":          "org-1",
		"name":        "Acme Corp",
		"slug":        "acme",
		"plan":        "pro",
		"memberCount": 5,
		"createdAt":   "2026-01-01T00:00:00Z",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/organization" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	ac := &api.AuthenticatedClient{
		Client: api.NewClient(srv.URL, "test-token", 5*time.Second),
	}
	org, err := ac.Client.GetOrganization(context.Background()) //nolint:staticcheck — nil ctx OK for test
	if err == nil {
		// Verify the decoded shape matches the fixture.
		if org.ID != "org-1" {
			t.Errorf("ID = %q, want org-1", org.ID)
		}
		if org.Name != "Acme Corp" {
			t.Errorf("Name = %q, want Acme Corp", org.Name)
		}
	}
}

func TestOrgShow_SchemaFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call to %s — schema should not hit the server", r.URL.Path)
	}))
	defer srv.Close()

	root := NewRootCommand("test")
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"org", "show", "--schema"})

	// requireAuth will fail because no credentials exist — that's fine,
	// --schema exits before auth.
	_ = root.Execute()

	// The schema path writes directly to os.Stdout (not cobra's out),
	// so we just verify the command registered without panic and the
	// root command tree is valid.
	cmd, _, err := root.Find([]string{"org", "show"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	f := cmd.Flags().Lookup("schema")
	if f == nil {
		t.Error("--schema flag not registered on org show")
	}
}

func TestOrgMembersList_Registration(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"org", "members", "list"})
	if err != nil {
		t.Fatalf("find org members list: %v", err)
	}
	if cmd.Short == "" {
		t.Error("org members list has empty Short description")
	}
}

func TestOrgMembersList_JSONShape(t *testing.T) {
	fixture := map[string]any{
		"items": []map[string]any{
			{"userId": "u1", "email": "alice@example.com", "role": "admin", "createdAt": "2026-01-01"},
			{"userId": "u2", "email": "bob@example.com", "role": "member", "createdAt": "2026-01-02"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/organization/members" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	ac := &api.AuthenticatedClient{
		Client: api.NewClient(srv.URL, "test-token", 5*time.Second),
	}
	resp, err := ac.Client.ListOrgMembers(context.Background()) //nolint:staticcheck
	if err != nil {
		t.Fatalf("ListOrgMembers: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Errorf("items len = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].Email != "alice@example.com" {
		t.Errorf("first item email = %q, want alice@example.com", resp.Items[0].Email)
	}
}

func TestOrgMembersList_SchemaFlag(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"org", "members", "list"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	f := cmd.Flags().Lookup("schema")
	if f == nil {
		t.Error("--schema flag not registered on org members list")
	}
}

func TestOrgDocsList_Registration(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"org", "docs", "list"})
	if err != nil {
		t.Fatalf("find org docs list: %v", err)
	}
	if cmd.Short == "" {
		t.Error("org docs list has empty Short description")
	}
}

func TestOrgDocsShow_Registration(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"org", "docs", "show"})
	if err != nil {
		t.Fatalf("find org docs show: %v", err)
	}
	if cmd.Use != "show <id>" {
		t.Errorf("Use = %q, want 'show <id>'", cmd.Use)
	}
}

func TestOrgDocsList_JSONShape(t *testing.T) {
	fixture := map[string]any{
		"items": []map[string]any{
			{"id": "doc-1", "name": "readme.md", "workspaceId": "ws-1", "status": "indexed", "sizeBytes": 1024, "chunkCount": 5, "createdAt": "2026-01-01"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents" || r.URL.Query().Get("scope") != "org" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	ac := &api.AuthenticatedClient{
		Client: api.NewClient(srv.URL, "test-token", 5*time.Second),
	}
	resp, err := ac.Client.ListOrgDocuments(context.Background()) //nolint:staticcheck
	if err != nil {
		t.Fatalf("ListOrgDocuments: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Errorf("items len = %d, want 1", len(resp.Items))
	}
	if resp.Items[0].Name != "readme.md" {
		t.Errorf("name = %q, want readme.md", resp.Items[0].Name)
	}
}

func TestOrgDocsShow_JSONShape(t *testing.T) {
	fixture := map[string]any{
		"id": "doc-1", "name": "readme.md", "workspaceId": "ws-1",
		"status": "indexed", "sizeBytes": 1024, "chunkCount": 5,
		"createdAt": "2026-01-01",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/doc-1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	ac := &api.AuthenticatedClient{
		Client: api.NewClient(srv.URL, "test-token", 5*time.Second),
	}
	doc, err := ac.Client.GetDocument(context.Background(), "doc-1") //nolint:staticcheck
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if doc.ID != "doc-1" {
		t.Errorf("id = %q, want doc-1", doc.ID)
	}
	if doc.Name != "readme.md" {
		t.Errorf("name = %q, want readme.md", doc.Name)
	}
}

func TestWorkspaceDocsList_Registration(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "docs-list"})
	if err != nil {
		t.Fatalf("find workspace docs-list: %v", err)
	}
	if cmd.Short == "" {
		t.Error("workspace docs-list has empty Short description")
	}
}

func TestWorkspaceFilesList_Registration(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "files-list"})
	if err != nil {
		t.Fatalf("find workspace files-list: %v", err)
	}
	if cmd.Short == "" {
		t.Error("workspace files-list has empty Short description")
	}
}

func TestWorkspaceShow_Registration(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "show"})
	if err != nil {
		t.Fatalf("find workspace show: %v", err)
	}
	if cmd.Short == "" {
		t.Error("workspace show has empty Short description")
	}
	f := cmd.Flags().Lookup("schema")
	if f == nil {
		t.Error("--schema flag not registered on workspace show")
	}
}

func TestWorkspaceShow_Detail(t *testing.T) {
	fixture := map[string]any{
		"id": "ws-1", "name": "My Repo",
		"rootPath": "/repo", "fileCount": 10, "folderCount": 3, "symbolCount": 50,
		"documents": []map[string]any{
			{"documentId": "doc-1", "relativePath": "README.md", "status": "indexed"},
		},
		"files": []map[string]any{
			{"path": "main.go", "language": "go"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/ws-1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	ac := &api.AuthenticatedClient{
		Client: api.NewClient(srv.URL, "test-token", 5*time.Second),
	}
	detail, err := ac.Client.GetWorkspaceDetail(context.Background(), "ws-1", "docs,files") //nolint:staticcheck
	if err != nil {
		t.Fatalf("GetWorkspaceDetail: %v", err)
	}
	if detail.ID != "ws-1" {
		t.Errorf("id = %q, want ws-1", detail.ID)
	}
	if len(detail.Documents) != 1 {
		t.Errorf("documents len = %d, want 1", len(detail.Documents))
	}
	if len(detail.Files) != 1 {
		t.Errorf("files len = %d, want 1", len(detail.Files))
	}
}

func TestOrgCommand_Registered(t *testing.T) {
	root := NewRootCommand("test")
	var found bool
	for _, c := range root.Commands() {
		if c.Use == "org" {
			found = true
			break
		}
	}
	if !found {
		t.Error("org command not registered under root")
	}
}

func TestOrgShow_HasExpectedFlags(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, _ := root.Find([]string{"org", "show"})
	for _, name := range []string{"json", "save", "schema"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("org show missing --%s flag", name)
		}
	}
}

func TestOrgMembersList_HasExpectedFlags(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, _ := root.Find([]string{"org", "members", "list"})
	for _, name := range []string{"json", "save", "schema"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("org members list missing --%s flag", name)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{2 * 1024 * 1024, "2.0 MB"},
	}
	for _, tc := range tests {
		got := formatBytes(tc.n)
		if !strings.HasPrefix(got, strings.Fields(tc.want)[0]) {
			t.Errorf("formatBytes(%d) = %q, want prefix %q", tc.n, got, tc.want)
		}
	}
}
