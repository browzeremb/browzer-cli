package api

import "context"

// Ask calls POST /ask with the given question and workspace ID.
// workspaceID must not be empty — the caller is responsible for
// resolving the workspace before calling this method.
func (c *Client) Ask(ctx context.Context, req AskRequest) (*AskResponse, error) {
	var resp AskResponse
	if err := c.postJSON(ctx, "ask", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AskCrossWorkspace calls POST /ask/cross-workspace.
// req must have WorkspaceIDs or AllWorkspaces set; the caller is responsible
// for validating the mutual-exclusion constraint before calling this method.
func (c *Client) AskCrossWorkspace(ctx context.Context, req AskRequest) (*AskResponse, error) {
	var resp AskResponse
	if err := c.postJSON(ctx, "ask/cross-workspace", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SearchCrossWorkspace calls POST /search with the given request body.
// The server fans out across workspaceIds (or all workspaces when allWorkspaces
// is true) and merges results. Single-workspace payloads pass through to
// the legacy single-workspace path.
func (c *Client) SearchCrossWorkspace(ctx context.Context, req CrossWorkspaceSearchRequest) (*CrossWorkspaceSearchResponse, error) {
	var resp CrossWorkspaceSearchResponse
	if err := c.postJSON(ctx, "search", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
