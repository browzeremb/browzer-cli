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
