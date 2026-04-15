// Package api — GitHub releases client for `browzer upgrade`.
//
// Stateless, unauthenticated GET against the public releases API.
// GitHub rate-limits anonymous callers to 60 req/h/IP which is plenty
// for human-triggered upgrade checks.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// LatestReleaseURL is the public endpoint the upgrade check polls. Exposed
// as a package var (not a const) so tests can redirect it at an
// httptest.Server without touching the command wiring.
var LatestReleaseURL = "https://api.github.com/repos/browzeremb/browzer-cli/releases/latest"

// GitHubRelease is a minimal projection of the upstream release payload —
// only fields the CLI actually renders. The JSON decoder drops unknown
// keys silently, which keeps us forward-compatible with GitHub's schema.
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Prerelease  bool      `json:"prerelease"`
}

// FetchLatestRelease hits api.github.com with a 5 s timeout. Returns nil
// plus the underlying error on any transport/decode failure or non-200.
func FetchLatestRelease(ctx context.Context) (*GitHubRelease, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestReleaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "browzer-cli/upgrade-check")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases API returned %d", resp.StatusCode)
	}
	var r GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}
