package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSearchWorkspace_PopulatesPathFromDocumentName pins the cross-surface
// `path` alias contract: the wire format only carries `documentName`, but
// the CLI client mirrors it into `Path` post-decode so JSON consumers can
// use the same field name across explore / deps / search.
//
// Regression for SESSION_REPORT_20260505 §3.6: agents previously got
// `path: null` from `browzer search --json` because the field did not
// exist in the response.
func TestSearchWorkspace_PopulatesPathFromDocumentName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/ws-1/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"text":"hit-a","position":0,"score":0.42,"documentName":"docs/a.md"},
				{"text":"hit-b","position":3,"score":0.31,"documentName":"docs/runbooks/b.md"}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	results, err := c.SearchWorkspace(context.Background(), "ws-1", "q", 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Path == "" {
			t.Errorf("results[%d].Path empty (want %q)", i, r.DocumentName)
		}
		if r.Path != r.DocumentName {
			t.Errorf("results[%d].Path = %q, want %q (mirror of DocumentName)", i, r.Path, r.DocumentName)
		}
	}
}

// TestSearchWorkspace_PreservesServerEmittedPath pins the server-tolerant
// invariant introduced in §I5 (2026-05-05): if the server starts emitting
// `path` directly (distinct from `documentName`), PopulateSearchResultPaths
// must NOT overwrite it. Today server emits only `documentName`; this test
// proves the populator collapses to a no-op when Path is already set.
func TestSearchWorkspace_PreservesServerEmittedPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"text":"hit","position":0,"score":0.42,"documentName":"docs/a.md","path":"workspace-relative/a.md"}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	results, err := c.SearchWorkspace(context.Background(), "ws-1", "q", 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Path != "workspace-relative/a.md" {
		t.Errorf("server-emitted Path was clobbered: got %q, want %q",
			results[0].Path, "workspace-relative/a.md")
	}
}

// TestPopulateSearchResultPaths_Idempotent calls the populator twice and
// verifies the second call is a no-op (Path already set).
func TestPopulateSearchResultPaths_Idempotent(t *testing.T) {
	rs := []SearchResult{
		{DocumentName: "a.md"},
		{DocumentName: "b.md", Path: "explicit/b.md"},
	}
	PopulateSearchResultPaths(rs)
	PopulateSearchResultPaths(rs)
	if rs[0].Path != "a.md" {
		t.Errorf("rs[0].Path = %q, want %q (mirror of DocumentName)", rs[0].Path, "a.md")
	}
	if rs[1].Path != "explicit/b.md" {
		t.Errorf("rs[1].Path = %q, want %q (preserve server-emitted)", rs[1].Path, "explicit/b.md")
	}
}

// TestSearchCrossWorkspace_PopulatesPathFromDocumentName mirrors the same
// invariant for the POST /search fan-out endpoint.
func TestSearchCrossWorkspace_PopulatesPathFromDocumentName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"text":"x","position":0,"score":0.5,"documentName":"docs/x.md","workspaceId":"ws-1"},
				{"text":"y","position":1,"score":0.4,"documentName":"docs/y.md","workspaceId":"ws-2"}
			],
			"fromCache": false
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 0)
	resp, err := c.SearchCrossWorkspace(context.Background(), CrossWorkspaceSearchRequest{
		Query:        "q",
		WorkspaceIDs: []string{"ws-1", "ws-2"},
		TopK:         10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	for i, r := range resp.Results {
		if r.Path == "" {
			t.Errorf("results[%d].Path empty (want %q)", i, r.DocumentName)
		}
		if r.Path != r.DocumentName {
			t.Errorf("results[%d].Path = %q, want %q", i, r.Path, r.DocumentName)
		}
	}
}
