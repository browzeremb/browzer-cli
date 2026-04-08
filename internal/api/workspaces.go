package api

import (
	"context"
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
func (c *Client) CreateWorkspace(ctx context.Context, req CreateWorkspaceRequest) (*WorkspaceDto, error) {
	var ws WorkspaceDto
	if err := c.postJSON(ctx, "api/workspaces", req, &ws); err != nil {
		return nil, err
	}
	return &ws, nil
}

// DeleteWorkspace calls DELETE /api/workspaces/:id.
func (c *Client) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	return c.deleteCall(ctx, "api/workspaces/"+workspaceID, nil)
}

// ParseWorkspace calls POST /api/workspaces/parse with the body shape
// expected by apps/api: { workspaceId, rootPath, folders, files }.
func (c *Client) ParseWorkspace(ctx context.Context, req ParseWorkspaceRequest) error {
	return c.postJSON(ctx, "api/workspaces/parse", req, nil)
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
