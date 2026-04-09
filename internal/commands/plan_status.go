package commands

import (
	"context"
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/ui"
)

// printPlanStatus fetches GET /api/billing/usage and prints a single
// line summarizing the caller's plan slot consumption. Best-effort —
// any error (network, 402, missing endpoint on older servers) is
// swallowed silently because plan status is contextual UX polish and
// should never block the command that called it.
//
// The caller provides the context so this respects the same
// cancellation chain as the parent command.
func printPlanStatus(ctx context.Context, client *api.Client) {
	usage, err := client.BillingUsage(ctx)
	if err != nil || usage == nil {
		return
	}
	ui.Arrow(fmt.Sprintf(
		"Plan: %s — workspaces %d/%d",
		usage.Plan,
		usage.Workspaces.Used,
		usage.Workspaces.Limit,
	))
}
