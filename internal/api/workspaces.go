package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ListWorkspaces calls GET /api/workspaces.
func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceDto, error) {
	var body struct {
		Workspaces []WorkspaceDto `json:"workspaces"`
	}
	if err := c.getJSON(ctx, "api/workspaces", nil, &body); err != nil {
		return nil, err
	}
	return body.Workspaces, nil
}

// CreateWorkspace calls POST /api/workspaces.
//
// The server responds with `{ workspaceId, name, rootPath }` (see
// apps/api/src/routes/api-workspaces.ts) — NOT the `{ id, ... }`
// shape used by the list endpoint. Decode into a dedicated struct
// and remap to WorkspaceDto so the rest of the CLI keeps using the
// canonical `ID` field.
func (c *Client) CreateWorkspace(ctx context.Context, req CreateWorkspaceRequest) (*WorkspaceDto, error) {
	var raw struct {
		WorkspaceID string `json:"workspaceId"`
		Name        string `json:"name"`
		RootPath    string `json:"rootPath"`
	}
	if err := c.postJSON(ctx, "api/workspaces", req, &raw); err != nil {
		return nil, err
	}
	return &WorkspaceDto{
		ID:       raw.WorkspaceID,
		Name:     raw.Name,
		RootPath: raw.RootPath,
	}, nil
}

// DeleteWorkspace calls DELETE /api/workspaces/:id.
func (c *Client) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	return c.deleteCall(ctx, "api/workspaces/"+workspaceID, nil)
}

// UpdateWorkspaceRequest is the body of PATCH /api/workspaces/:id.
// Both fields are optional — supply only the ones you want to change.
type UpdateWorkspaceRequest struct {
	Name     string `json:"name,omitempty"`
	RootPath string `json:"rootPath,omitempty"`
}

// UpdateWorkspace calls PATCH /api/workspaces/:id with the supplied name
// and/or rootPath. The server applies last-writer-wins semantics per field.
func (c *Client) UpdateWorkspace(ctx context.Context, workspaceID, name, rootPath string) error {
	req := UpdateWorkspaceRequest{Name: name, RootPath: rootPath}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPatch, "api/workspaces/"+workspaceID, nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return httpStatusError(resp)
}

// ParseWorkspaceResponse captures the fields of POST /api/workspaces/parse
// that the CLI cares about. Only `Status` is populated when the server
// short-circuits with `{ status: "unchanged" }` (PR 3 fingerprint hit) —
// the other fields stay zero/empty so callers can branch on `Status`.
type ParseWorkspaceResponse struct {
	Status      string `json:"status,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ParseWorkspaceOptions controls per-call behavior on the
// POST /api/workspaces/parse client.
type ParseWorkspaceOptions struct {
	// ForceParse, when true, sets the `X-Force-Parse: true` header so the
	// server bypasses the jobs-in-flight gate (PR 3). The CLI sets this
	// when the user passes `--force` to `index`/`sync`.
	ForceParse bool
}

// ParseWorkspace calls POST /api/workspaces/parse with the body shape
// expected by apps/api: { workspaceId, rootPath, folders, files }.
//
// Returns the decoded response so callers can detect `status: "unchanged"`
// (fingerprint short-circuit, PR 3) and surface a friendly message
// instead of pretending a re-parse happened.
func (c *Client) ParseWorkspace(ctx context.Context, req ParseWorkspaceRequest, opts ParseWorkspaceOptions) (*ParseWorkspaceResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{}
	if opts.ForceParse {
		// Server reads this header case-insensitively.
		headers["X-Force-Parse"] = "true"
	}

	resp, err := c.doWithHeaders(ctx, http.MethodPost, "api/workspaces/parse", nil, bytes.NewReader(body), "application/json", headers)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out ParseWorkspaceResponse
		// 204 / empty body is tolerated — out stays zero-valued.
		_ = json.NewDecoder(io.LimitReader(resp.Body, MaxResponseBytes)).Decode(&out)
		return &out, nil
	}

	return nil, httpStatusError(resp)
}

// SearchWorkspace calls GET /api/workspaces/:id/search.
func (c *Client) SearchWorkspace(ctx context.Context, workspaceID, query string, topK int, minScore float64) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("query", query)
	if topK > 0 {
		q.Set("topK", strconv.Itoa(topK))
	}
	if minScore > 0 {
		q.Set("minScore", strconv.FormatFloat(minScore, 'f', -1, 64))
	}
	var body SearchResponse
	if err := c.getJSON(ctx, "api/workspaces/"+workspaceID+"/search", q, &body); err != nil {
		return nil, err
	}
	return body.Results, nil
}

// ExploreWorkspace calls GET /api/workspaces/:id/explore.
func (c *Client) ExploreWorkspace(ctx context.Context, workspaceID, query string, limit, depth int) ([]ExploreEntry, error) {
	q := url.Values{}
	q.Set("query", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if depth > 0 {
		q.Set("depth", strconv.Itoa(depth))
	}
	var body ExploreResponse
	if err := c.getJSON(ctx, "api/workspaces/"+workspaceID+"/explore", q, &body); err != nil {
		return nil, err
	}
	return body.Entries, nil
}

// FetchDeps returns the dependency graph for a single file in the workspace.
func (c *Client) FetchDeps(ctx context.Context, workspaceID, path string, reverse bool, limit int) (*DepsResponse, error) {
	q := url.Values{}
	q.Set("path", path)
	if reverse {
		q.Set("reverse", "true")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var body DepsResponse
	if err := c.getJSON(ctx, "api/workspaces/"+workspaceID+"/deps", q, &body); err != nil {
		return nil, err
	}
	return &body, nil
}

// FetchMentions returns the documents that mention a given source file via
// POST /api/workspaces/:id/mentions?limit=N.
// limit is sent as a query parameter (the server binds it via mentionsQuerySchema
// on request.query), NOT in the JSON body.
func (c *Client) FetchMentions(ctx context.Context, workspaceID, path string, limit int) (*MentionsResponse, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	reqBody := map[string]any{"path": path}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	httpResp, err := c.do(ctx, "POST", "api/workspaces/"+workspaceID+"/mentions", q, bytes.NewReader(buf), "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = httpResp.Body.Close() }()
	var resp MentionsResponse
	if err := decodeJSONResponse(httpResp, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetWorkspaceManifest fetches the per-file manifest consumed by the
// daemon's filter engine when the user asks for `filterLevel:
// "aggressive"`. The CLI caches the response at
// `~/.browzer/workspaces/<id>/manifest.json`; the daemon reloads it on
// demand via `ManifestCache.FileForPath`.
func (c *Client) GetWorkspaceManifest(ctx context.Context, workspaceID string) (*WorkspaceManifest, error) {
	var body WorkspaceManifest
	if err := c.getJSON(ctx, "api/workspaces/"+workspaceID+"/manifest", nil, &body); err != nil {
		return nil, err
	}
	return &body, nil
}

// GetWorkspace fetches a single workspace via the list endpoint
// (mirrors the legacy `workspace get` behavior, since the read-by-id
// route doesn't exist server-side).
func (c *Client) GetWorkspace(ctx context.Context, workspaceID string) (*WorkspaceDto, error) {
	all, err := c.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == workspaceID {
			return &all[i], nil
		}
	}
	return nil, nil
}

// WorkspaceDetailDto extends WorkspaceDto with optional nested lists
// populated when ?include=docs or ?include=files is passed.
type WorkspaceDetailDto struct {
	WorkspaceDto
	Documents []IndexedDocument  `json:"documents,omitempty"`
	Files     []WorkspaceFileDto `json:"files,omitempty"`
}

// WorkspaceFileDto is one indexed file entry.
type WorkspaceFileDto struct {
	Path        string `json:"path"`
	Language    string `json:"language,omitempty"`
	SymbolCount int    `json:"symbolCount,omitempty"`
	Lines       int    `json:"lines,omitempty"`
}

// GetWorkspaceDetail calls GET /api/workspaces/:id?include=<include>.
// The include parameter is comma-separated ("docs", "files", or "docs,files").
func (c *Client) GetWorkspaceDetail(ctx context.Context, workspaceID, include string) (*WorkspaceDetailDto, error) {
	q := url.Values{}
	if include != "" {
		q.Set("include", include)
	}
	var out WorkspaceDetailDto
	if err := c.getJSON(ctx, "api/workspaces/"+workspaceID, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
