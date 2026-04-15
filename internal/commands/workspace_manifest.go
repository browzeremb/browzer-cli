package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/config"
)

// pullAndSaveManifest fetches the workspace manifest from apps/api and
// writes it to `~/.browzer/workspaces/<id>/manifest.json` so the daemon's
// ManifestCache.FileForPath lookup can back the `filterLevel:
// "aggressive"` code path in `browzer read` and the rewrite-read hook.
//
// Non-fatal: callers treat any error as a warning — the local manifest
// is a cache, not a source of truth. Missing or stale cache just means
// the filter engine downgrades aggressive → minimal.
func pullAndSaveManifest(ctx context.Context, client *api.Client, workspaceID string) error {
	manifest, err := client.GetWorkspaceManifest(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}
	path := config.ManifestCachePath(workspaceID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir manifest cache dir: %w", err)
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write manifest tmp: %w", err)
	}
	// Atomic rename — SIGINT mid-write won't leave a half-file in place.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}
