// Package urlvalidate sanitises server URLs before handing them to
// browser launches or HTTP clients. Mirrors the legacy
// validateServerUrl in src/commands/login.ts.
//
// Rules:
//   - Only http:// and https:// schemes (rejects file://, javascript:,
//     etc — defends against handing arbitrary URIs to `open`).
//   - http:// is only allowed for loopback (127.0.0.1, ::1, localhost)
//     and *.railway.internal hosts. Use BROWZER_ALLOW_INSECURE=1 to
//     bypass for non-prod testing.
//   - All other URLs must be https://.
package urlvalidate

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/config"
)

// Validate parses raw and returns a *url.URL or an error explaining
// why it was rejected.
func Validate(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL must use http or https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("server URL is missing a host")
	}
	if u.Scheme == "https" {
		return u, nil
	}
	// http:// — allow only loopback / *.railway.internal / opt-in.
	if isLoopback(u.Hostname()) || isRailwayInternal(u.Hostname()) {
		return u, nil
	}
	if config.AllowInsecure() {
		return u, nil
	}
	return nil, fmt.Errorf(
		"refusing to use http:// for non-loopback host %q "+
			"(set BROWZER_ALLOW_INSECURE=1 to override)",
		u.Hostname(),
	)
}

func isLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost", "[::1]":
		return true
	}
	return false
}

func isRailwayInternal(host string) bool {
	return strings.HasSuffix(host, ".railway.internal")
}
