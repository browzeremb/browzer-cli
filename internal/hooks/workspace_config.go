package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// workspaceConfigValue is the cached result for a single workspace root.
type workspaceConfigValue struct {
	globMode string // "block" | "soft"
}

// workspaceConfigCache caches WorkspaceGlobMode results keyed by workspace
// root path. A hook binary is invoked once per event and exits immediately —
// the cache is per-process and never stale.
var workspaceConfigCache sync.Map // key: string (wsRoot) → value: workspaceConfigValue

// WorkspaceGlobMode returns "block" when the workspace's .browzer/config.json
// has hooks.glob.mode set to "block". Falls back to "soft" when the config is
// missing, unreadable, or unparseable.
//
// Results are cached per-process by workspace root path — subsequent calls
// with the same root perform zero filesystem I/O.
func WorkspaceGlobMode(wsRoot string) string {
	if wsRoot == "" {
		return "soft"
	}

	if v, ok := workspaceConfigCache.Load(wsRoot); ok {
		return v.(workspaceConfigValue).globMode
	}

	mode := workspaceGlobModeSlow(wsRoot)
	workspaceConfigCache.Store(wsRoot, workspaceConfigValue{globMode: mode})
	return mode
}

// workspaceGlobModeSlow reads .browzer/config.json from disk. Called exactly
// once per workspace root per process lifetime.
func workspaceGlobModeSlow(wsRoot string) string {
	cfgPath := filepath.Join(wsRoot, ".browzer", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		// Missing or unreadable config — fall back to soft (permissive) mode.
		return "soft"
	}
	var cfg struct {
		Hooks struct {
			Glob struct {
				Mode string `json:"mode"`
			} `json:"glob"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "soft"
	}
	if cfg.Hooks.Glob.Mode == "block" {
		return "block"
	}
	return "soft"
}

// clearConfigCache resets the per-process config cache. Used in tests to
// prevent cross-test bleed when workspace roots are reused across TempDir
// fixtures. Not safe to call from production code.
func clearConfigCache() {
	workspaceConfigCache.Range(func(k, _ any) bool { workspaceConfigCache.Delete(k); return true })
}
