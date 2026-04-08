// Package commands — preflight helper used by `init` and `sync`.
//
// Both commands walk the docs tree before uploading. Phase 7 adds a
// server-authoritative chunk-budget check in front of the upload so
// users hit "your plan is full" with an actionable message BEFORE a
// workspace is half-indexed.
//
// The helper is intentionally shared — both call sites pass the same
// []walker.DocFile slice and want the same failure surface.
package commands

import (
	"context"
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/api"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/browzeremb/browzer-cli/internal/walker"
)

// preflightFileCap caps the number of files we ship to the server in a
// single preflight request. 10k is comfortably above any realistic
// monorepo docs tree but keeps the request body bounded — the server
// enforces its own cap too, but we'd rather surface a clean local
// warning than a 413 on the preflight call itself.
const preflightFileCap = 10_000

// runPreflight converts a []walker.DocFile into the PreflightFile wire
// shape, calls POST /api/ingestion/preflight, and either prints a
// success line or returns a CliError (exit code 5) with actionable
// next-steps.
//
// On very large trees (> preflightFileCap), we truncate + warn. The
// server's estimate is still useful because the truncated tail tends
// to be the "tail of the long tail" — if the head overflows the
// budget, the warning is moot anyway.
func runPreflight(ctx context.Context, client *api.Client, docs []walker.DocFile) error {
	if len(docs) == 0 {
		return nil
	}

	truncated := false
	if len(docs) > preflightFileCap {
		ui.Warn(fmt.Sprintf(
			"Preflight: truncating to %d of %d files (server-side estimate will be a lower bound)",
			preflightFileCap, len(docs),
		))
		docs = docs[:preflightFileCap]
		truncated = true
	}

	files := make([]api.PreflightFile, 0, len(docs))
	for _, d := range docs {
		files = append(files, api.PreflightFile{
			Path:      d.RelativePath,
			SizeBytes: d.Size,
		})
	}

	sp := ui.StartSpinner("Preflight: checking plan budget...")
	resp, err := client.Preflight(ctx, files)
	if err != nil {
		sp.Failure("Preflight request failed")
		return fmt.Errorf("preflight failed: %w", err)
	}

	if !resp.Fits {
		sp.Failure("Preflight: plan budget exceeded")
		available := resp.LimitChunks - resp.CurrentChunks
		if available < 0 {
			available = 0
		}
		msg := fmt.Sprintf(
			"Este repositório geraria %d chunks (%d bytes) mas você tem %d chunks disponíveis (%d total no plano).",
			resp.ProjectedChunks, resp.ProjectedBytes, available, resp.LimitChunks,
		)
		if resp.Reason != "" {
			msg += "\n" + resp.Reason
		}
		msg += "\n\nAções:" +
			"\n  • Delete um workspace:  browzer workspace delete" +
			"\n  • Faça upgrade do plano: https://browzeremb.com/dashboard/settings/billing"
		return cliErrors.NewQuotaExceededError(msg)
	}

	available := resp.LimitChunks - resp.CurrentChunks
	okMsg := fmt.Sprintf(
		"Preflight OK: %d chunks projetados, %d disponíveis",
		resp.ProjectedChunks, available,
	)
	if truncated {
		okMsg += " (amostra truncada — ver aviso acima)"
	}
	sp.Success(okMsg)
	return nil
}
