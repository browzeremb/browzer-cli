package commands

import (
	"fmt"

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
				if err := auth.RevokeBestEffort(rootContext(cmd), creds.Server, creds.AccessToken); err != nil {
					// Surface to stderr but never fail the command:
					// the user MUST end up logged out locally even
					// when the server is unreachable.
					output.Errf("Warning: could not revoke token server-side (%s). Local credentials will still be cleared.\n", err.Error())
				}
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
