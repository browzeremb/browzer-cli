package commands

import (
	"fmt"
	"strconv"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/auth"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

func registerStatus(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Browzer login and workspace status",
		Long: `Show Browzer login and workspace status.

Examples:
  browzer status
  browzer status --json
  browzer status --json --save status.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			// Surface "Not logged in" before "No Browzer project here"
			// so the user gets the most actionable hint first.
			creds := auth.LoadCredentials()
			if creds == nil {
				return cliErrors.NotAuthenticated()
			}

			gitRoot, err := requireGitRoot()
			if err != nil {
				return err
			}
			project, err := config.LoadProjectConfig(gitRoot)
			if err != nil {
				return err
			}
			if project == nil {
				return cliErrors.NoProject()
			}

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			ws, err := ac.Client.GetWorkspace(rootContext(cmd), project.WorkspaceID)
			if err != nil {
				return err
			}
			if ws == nil {
				return cliErrors.WithCode("Workspace was deleted on server. Run `browzer init --force` to re-create.", 4)
			}

			// Billing usage is best-effort — old servers won't have
			// GET /api/billing/usage, and we must not fail `status`
			// just because the billing endpoint is unreachable.
			usage, usageErr := ac.Client.BillingUsage(rootContext(cmd))
			if usageErr != nil {
				usage = nil
			}

			workspacePayload := map[string]any{
				"id":          project.WorkspaceID,
				"name":        project.WorkspaceName,
				"root":        gitRoot,
				"fileCount":   ws.FileCount,
				"folderCount": ws.FolderCount,
				"symbolCount": ws.SymbolCount,
			}
			if project.LastSyncCommit != "" {
				workspacePayload["lastSyncCommit"] = project.LastSyncCommit
			}

			payload := map[string]any{
				"user":              map[string]string{"id": creds.UserID},
				"organization":      map[string]string{"id": creds.OrganizationID},
				"server":            project.Server,
				"tokenExpiresAt":    creds.ExpiresAt,
				"tokenExpiresHuman": formatExpiry(creds.ExpiresAt),
				"workspace":         workspacePayload,
			}
			if usage != nil {
				payload["billing"] = usage
			}

			// Two compact tables: session on top, workspace below.
			// Renders as bordered brand tables on a TTY and as
			// tab-separated plain text when stdout is piped.
			sessionTable := ui.Table(
				[]string{"Field", "Value"},
				[][]string{
					{"User", creds.UserID},
					{"Organization", creds.OrganizationID},
					{"Server", project.Server},
					{"Token expires", formatExpiry(creds.ExpiresAt)},
				},
			)
			workspaceTable := ui.Table(
				[]string{"Workspace", "Value"},
				[][]string{
					{"Name", fmt.Sprintf("%s (%s)", project.WorkspaceName, project.WorkspaceID)},
					{"Root", gitRoot},
					{"Files", strconv.Itoa(ws.FileCount)},
					{"Folders", strconv.Itoa(ws.FolderCount)},
					{"Symbols", strconv.Itoa(ws.SymbolCount)},
				},
			)
			human := sessionTable + "\n" + workspaceTable
			if usage != nil {
				human += "\n" + renderBillingTable(usage)
			}

			return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON on stdout")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}

// renderBillingTable formats a BillingUsageResponse as a compact
// bordered table. Rendered below the workspace table in `status`.
// The numeric columns are kept narrow so the table width matches the
// other two on a typical 80-col terminal.
func renderBillingTable(u *api.BillingUsageResponse) string {
	planCell := u.Plan
	if u.Status != "" {
		planCell = fmt.Sprintf("%s (%s)", u.Plan, u.Status)
	}
	rows := [][]string{
		{"Plan", planCell},
	}
	if u.TrialEndsAt != nil {
		days := int(time.Until(*u.TrialEndsAt).Hours() / 24)
		switch {
		case days < 0:
			rows = append(rows, []string{"Trial", "expired"})
		case days == 0:
			rows = append(rows, []string{"Trial", "ends today"})
		default:
			rows = append(rows, []string{"Trial", fmt.Sprintf("%d days left", days)})
		}
	}
	rows = append(rows,
		[]string{"Queries", formatCounter(u.Queries, false)},
		[]string{"Chunks", formatCounter(u.Chunks, false)},
		[]string{"Storage", formatCounter(u.Storage, true)},
		[]string{"Workspaces", formatCounter(u.Workspaces, false)},
		[]string{"Users", formatCounter(u.Users, false)},
		[]string{"API keys", formatCounter(u.APIKeys, false)},
	)
	// Daily ingestion counter — only present on servers >= 2026-04-22.
	// Surfaced so users notice a near-cap state BEFORE `browzer sync`
	// starts rejecting batches with the misleading "batch not found"
	// symptom. Shown with the usual used/limit/pct format and, when
	// available, the ISO reset-at time.
	if u.IngestionDaily != nil {
		daily := api.BillingCounter{
			Used:  u.IngestionDaily.Used,
			Limit: u.IngestionDaily.Limit,
		}
		cell := formatCounter(daily, false)
		if u.IngestionDaily.ResetAt != nil {
			hoursLeft := int(time.Until(*u.IngestionDaily.ResetAt).Hours())
			if hoursLeft > 0 {
				cell = fmt.Sprintf("%s (resets in %dh)", cell, hoursLeft)
			}
		}
		rows = append(rows, []string{"Ingestion/day", cell})
	}
	return ui.Table([]string{"Billing", "Value"}, rows)
}

// formatCounter renders a BillingCounter as "used / limit (pct%)".
// When `bytes` is true, both numbers are passed through humanBytes.
// Unlimited plans (limit <= 0) render as "used / ∞".
func formatCounter(c api.BillingCounter, bytes bool) string {
	fmtNum := func(n int64) string {
		if bytes {
			return humanBytes(n)
		}
		return strconv.FormatInt(n, 10)
	}
	if c.Limit <= 0 {
		return fmt.Sprintf("%s / ∞", fmtNum(c.Used))
	}
	pct := float64(c.Used) * 100 / float64(c.Limit)
	return fmt.Sprintf("%s / %s (%.0f%%)", fmtNum(c.Used), fmtNum(c.Limit), pct)
}

// humanBytes renders a byte count with a binary-prefix suffix. Kept
// local to status.go because it's the only current caller; promote to
// internal/ui if a second caller appears.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatExpiry mirrors src/commands/status.ts:formatExpiry — humanises
// an ISO timestamp as "in N days" / "expires today" / "expired".
func formatExpiry(iso string) string {
	if iso == "" {
		return "unknown"
	}
	exp, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		exp, err = time.Parse(time.RFC3339, iso)
		if err != nil {
			return "unknown"
		}
	}
	days := int(time.Until(exp).Hours() / 24)
	if days < 0 {
		return "expired"
	}
	if days == 0 {
		return "expires today"
	}
	if days == 1 {
		return "in 1 day"
	}
	return fmt.Sprintf("in %d days", days)
}
