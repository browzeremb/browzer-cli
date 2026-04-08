package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProjectConfigVersion is the schema version of .browzer/config.json.
const ProjectConfigVersion = 1

// ProjectConfig is the contents of <repo>/.browzer/config.json. The
// file is committed to the repo so the workspace id can be shared
// across machines.
type ProjectConfig struct {
	Version        int    `json:"version"`
	WorkspaceID    string `json:"workspaceId"`
	WorkspaceName  string `json:"workspaceName"`
	Server         string `json:"server"`
	CreatedAt      string `json:"createdAt"`
	LastSyncCommit string `json:"lastSyncCommit,omitempty"`
}

// LoadProjectConfig walks up from cwd looking for .browzer/config.json
// in the git tree and returns the parsed config. Returns nil when no
// config is found.
func LoadProjectConfig(gitRoot string) (*ProjectConfig, error) {
	if gitRoot == "" {
		return nil, nil
	}
	path := filepath.Join(gitRoot, ".browzer", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.WorkspaceID == "" {
		return nil, nil
	}
	return &cfg, nil
}

// SaveProjectConfig writes <gitRoot>/.browzer/config.json with the
// given config. Stamps Version + CreatedAt if absent. Writes are
// atomic (temp file + rename) to honor the repo-wide invariant from
// packages/cli/CLAUDE.md: a SIGINT mid-sync must never leave the
// project config half-written.
func SaveProjectConfig(gitRoot string, cfg *ProjectConfig) error {
	if cfg.Version == 0 {
		cfg.Version = ProjectConfigVersion
	}
	if cfg.CreatedAt == "" {
		cfg.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	dir := filepath.Join(gitRoot, ".browzer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, "config.json")
	tmp := final + ".tmp"

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	// os.Rename is atomic on POSIX and on Windows (>= Go 1.5) when
	// both paths live on the same filesystem, which they do here
	// (same .browzer directory).
	if err := os.Rename(tmp, final); err != nil {
		// Best-effort cleanup so we don't leave a stale .tmp around.
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// AddCacheDirToGitignore appends `.browzer/.cache/` to <gitRoot>/.gitignore
// if not already present. Idempotent.
func AddCacheDirToGitignore(gitRoot string) error {
	const entry = ".browzer/.cache/"
	path := filepath.Join(gitRoot, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil // already present
		}
	}
	// Append (with leading newline if file doesn't end with one).
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	out := append(data, []byte(prefix+entry+"\n")...)
	return os.WriteFile(path, out, 0o644)
}
