package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RevokeBestEffort POSTs the bearer token to /api/device/revoke using
// the package-local auth HTTP client (so it honors context, has a
// timeout, and bypasses HTTP_PROXY).
//
// Unlike the previous in-place implementation in commands/logout.go,
// this version returns the underlying error instead of swallowing it.
// The caller (commands/logout.go) decides how to surface the warning;
// the local credentials are still cleared regardless.
func RevokeBestEffort(ctx context.Context, server, token string) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(server, "/")+"/api/device/revoke",
		nil,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("revoke returned HTTP %d", resp.StatusCode)
	}
	return nil
}
