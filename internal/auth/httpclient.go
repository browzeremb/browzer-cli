package auth

import (
	"net/http"
	"time"
)

// authHTTPTimeout is the per-request ceiling for device-flow and revoke
// calls. Long enough to absorb a cold-start API but short enough that
// the user can reach SIGINT without `os.Exit` racing the kernel.
const authHTTPTimeout = 30 * time.Second

// newAuthHTTPClient returns an *http.Client suitable for the auth flow:
//
//   - explicit per-request Timeout (so context.Done is honored even if
//     the upstream forgets to attach a deadline);
//   - Transport.Proxy = nil so an attacker-controlled HTTP_PROXY env
//     var cannot intercept device tokens (the auth invariant from
//     packages/cli/CLAUDE.md);
//   - default TLS config (no InsecureSkipVerify).
//
// This client is internal to the auth package; api.Client uses its own
// retrying transport via api.NewClient.
func newAuthHTTPClient() *http.Client {
	return &http.Client{
		Timeout: authHTTPTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// authHTTPClient is the package-level client reused by every auth call.
// It is safe for concurrent use; tests that need to swap it out can
// override the variable.
var authHTTPClient = newAuthHTTPClient()
