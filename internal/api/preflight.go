// Package api — preflight (Phase 7 pricing/billing).
//
// `POST /api/ingestion/preflight` gives the CLI a cheap, authoritative
// "would this fit?" check BEFORE it uploads a single byte. The server
// estimates chunk count from the (path, sizeBytes) tuples and compares
// against the caller's plan limit. The CLI uses this to short-circuit
// init/sync before creating a workspace or enqueueing any batches.
package api

import (
	"context"
)

// PreflightFile is one entry in the preflight request payload.
// `Path` is the repo-relative forward-slash path; `SizeBytes` is the
// raw file size the server will use to project chunk count.
type PreflightFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
}

// PreflightResponse mirrors the server envelope. `Fits` is the only
// field callers need to gate on — the rest are diagnostics surfaced to
// the user when `Fits` is false.
type PreflightResponse struct {
	Fits            bool   `json:"fits"`
	ProjectedChunks int64  `json:"projected_chunks"`
	ProjectedBytes  int64  `json:"projected_bytes"`
	CurrentChunks   int64  `json:"current_chunks"`
	LimitChunks     int64  `json:"limit_chunks"`
	Reason          string `json:"reason,omitempty"`
}

// Preflight calls POST /api/ingestion/preflight. The endpoint is safe
// to retry (it's a read-only estimate), but postJSON does NOT retry
// POSTs — that's an intentional monorepo-wide invariant (see client.go
// `retryableMethods`). Preflight inherits that behavior; the cost of a
// single retry here is low enough that we don't break the rule.
func (c *Client) Preflight(ctx context.Context, files []PreflightFile) (*PreflightResponse, error) {
	req := struct {
		Files []PreflightFile `json:"files"`
	}{Files: files}
	var resp PreflightResponse
	if err := c.postJSON(ctx, "api/ingestion/preflight", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
