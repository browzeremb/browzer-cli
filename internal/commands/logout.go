package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/auth"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

func registerLogout(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke and forget the local credentials",
		Long: `Best-effort revocation. Calls POST /api/device/revoke with the
current bearer token (tolerates failure), then deletes
~/.browzer/credentials. Always leaves the local machine in a
logged-out state even when the server is unreachable.

Examples:
  browzer logout
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			creds := auth.LoadCredentials()
			if creds != nil {
				_ = revokeBestEffort(rootContext(cmd), creds.Server, creds.AccessToken)
			}
			if err := auth.ClearCredentials(); err != nil {
				return fmt.Errorf("clear credentials: %w", err)
			}
			fmt.Println("Logged out.")
			return nil
		},
	}
	parent.AddCommand(cmd)
}

// revokeBestEffort POSTs the bearer token to /api/device/revoke. Errors
// are intentionally swallowed — logout must succeed locally even when
// the server is unreachable.
func revokeBestEffort(ctx context.Context, server, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(server, "/")+"/api/device/revoke", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}
