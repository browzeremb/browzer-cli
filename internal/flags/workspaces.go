package flags

import (
	"errors"
	"strings"
)

// ParseWorkspacesFlag splits comma-separated workspace IDs.
// Empty string returns empty slice without error.
// Trailing empty tokens (e.g. from a trailing comma) are silently skipped.
func ParseWorkspacesFlag(s string) ([]string, error) {
	if s == "" {
		return []string{}, nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue // skip empty tokens between or after commas
		}
		out = append(out, p)
	}
	return out, nil
}

// ValidateWorkspaceFlags ensures --workspaces and --all-workspaces are
// mutually exclusive. A non-nil, non-empty workspaceIDs slice combined
// with allWorkspaces=true is a conflict. An empty (or nil) slice with
// allWorkspaces=true is fine — it means the caller did not set --workspaces.
func ValidateWorkspaceFlags(workspaceIDs []string, allWorkspaces bool) error {
	if len(workspaceIDs) > 0 && allWorkspaces {
		return errors.New("cannot use --workspaces and --all-workspaces together")
	}
	return nil
}
