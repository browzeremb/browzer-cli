// Package api — billing usage (Phase 7 pricing/billing).
//
// `GET /api/billing/usage` returns the caller's current plan, trial
// window, and per-resource usage counters. The CLI surfaces this via
// `browzer status` so developers can tell at a glance how close they
// are to any cap.
package api

import (
	"context"
	"time"
)

// BillingCounter is a generic `used / limit` pair. All numeric fields
// come back as JSON numbers; we keep them as int64 for ease of
// formatting with humanBytes / percentage helpers.
type BillingCounter struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// BillingUsageResponse mirrors the envelope apps/api returns.
// `TrialEndsAt` and `CurrentPeriodEnd` are pointers because the server
// omits them for non-trial / grandfathered plans.
type BillingUsageResponse struct {
	Plan             string          `json:"plan"`
	Status           string          `json:"status"`
	TrialEndsAt      *time.Time      `json:"trial_ends_at,omitempty"`
	CurrentPeriodEnd *time.Time      `json:"current_period_end,omitempty"`
	Queries          BillingCounter  `json:"queries"`
	Chunks           BillingCounter  `json:"chunks"`
	Storage          BillingCounter  `json:"storage"`
	Workspaces       BillingCounter  `json:"workspaces"`
	Users            BillingCounter  `json:"users"`
	APIKeys          BillingCounter  `json:"api_keys"`
}

// BillingUsage calls GET /api/billing/usage. Returns a CliError via
// getJSON on 401/402/etc. — callers in `browzer status` treat any
// error as non-fatal and simply skip the billing block.
func (c *Client) BillingUsage(ctx context.Context) (*BillingUsageResponse, error) {
	var resp BillingUsageResponse
	if err := c.getJSON(ctx, "api/billing/usage", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
