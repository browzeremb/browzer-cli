// Package api — billing usage (Phase 7 pricing/billing).
//
// `GET /api/billing/usage` returns the caller's current plan, trial
// window, and per-resource usage counters. The CLI surfaces this via
// `browzer status` so developers can tell at a glance how close they
// are to any cap.
package api

import (
	"context"
	"strings"
	"time"
)

// BillingCounter is a generic `used / limit` pair. All numeric fields
// come back as JSON numbers; we keep them as int64 for ease of
// formatting with humanBytes / percentage helpers.
type BillingCounter struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// IngestionDailyCounter extends BillingCounter with an optional reset
// timestamp. Returned by `GET /api/billing/usage` when the api has a
// Redis client wired (production always does; unit tests and stubs may
// omit). ResetAt is the ISO-8601 instant at which Redis's PEXPIRE on
// the day-bucket key fires — i.e. when the daily counter rolls over
// to zero. nil means the key currently has no TTL (counter stays at
// zero until the first debit of the day).
type IngestionDailyCounter struct {
	Used    int64      `json:"used"`
	Limit   int64      `json:"limit"`
	ResetAt *time.Time `json:"reset_at,omitempty"`
}

// BillingUsageResponse mirrors the envelope apps/api returns.
// `TrialEndsAt` and `CurrentPeriodEnd` are pointers because the server
// omits them for non-trial / grandfathered plans.
//
// IngestionDaily is a pointer because it is only populated when the
// api has a Redis client wired — pre-2026-04-22 deployments omit it,
// and the Go decoder leaves the pointer nil there. Callers MUST
// nil-check before dereferencing.
type BillingUsageResponse struct {
	Plan             string                 `json:"plan"`
	Status           string                 `json:"status"`
	TrialEndsAt      *time.Time             `json:"trial_ends_at,omitempty"`
	CurrentPeriodEnd *time.Time             `json:"current_period_end,omitempty"`
	Queries          BillingCounter         `json:"queries"`
	Chunks           BillingCounter         `json:"chunks"`
	Storage          BillingCounter         `json:"storage"`
	Workspaces       BillingCounter         `json:"workspaces"`
	Users            BillingCounter         `json:"users"`
	APIKeys          BillingCounter         `json:"api_keys"`
	IngestionDaily   *IngestionDailyCounter `json:"ingestion_daily,omitempty"`
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

// IsBillingForbidden reports whether err indicates the caller's API key
// lacks read scope on /api/billing/usage. Default member-role API keys
// (post-G15 RBAC) hit this on every successful sync; the apps/api 403
// envelope is mapped by the http client to a CliError whose Message
// begins with "Forbidden — your token does not have access ...".
//
// Callers (e.g. workspace_sync) use this to suppress the noisy
// "could not fetch billing usage: Forbidden" warning that surfaces on
// every successful sync. F-13 (FR-4); see
// docs/browzer/dogfood-2026-04-29.md.
func IsBillingForbidden(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Forbidden")
}
