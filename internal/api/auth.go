package api

import "context"

// GetMe calls GET /api/auth/me — returns the user identity bound to
// the current bearer token. Used by `browzer login` post-auth.
func (c *Client) GetMe(ctx context.Context) (*MeResponse, error) {
	var me MeResponse
	if err := c.getJSON(ctx, "api/auth/me", nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}
