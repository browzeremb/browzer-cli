package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/auth"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/browzeremb/browzer-cli/internal/urlvalidate"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

// cliClientID is an alias for auth.DefaultClientID, kept as a local
// const so login.go reads naturally without an extra qualifier.
const cliClientID = auth.DefaultClientID

func registerLogin(parent *cobra.Command) {
	var serverFlag string
	var noBrowser bool
	var keyFlag string
	var keyFlagSet bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate the CLI against a Browzer server",
		Long: `Authenticate the CLI against a Browzer server.

Without --key, runs the OAuth 2.0 device flow (RFC 8628):
prints a one-time code, opens the browser, and polls until you approve.

With --key (or $BROWZER_API_KEY), skips the device flow and verifies a
pre-issued API key directly. Use this in CI and agent setups.

Examples:
  browzer login
  browzer login --server http://localhost:8080
  browzer login --key $BROWZER_API_KEY                # non-interactive
  BROWZER_API_KEY=... browzer login --key ''          # picks up env fallback
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve --server: explicit flag → env → default.
			rawServer := serverFlag
			if rawServer == "" {
				if env := config.ServerOverride(); env != "" {
					rawServer = env
				} else {
					rawServer = config.DefaultServer
				}
			}
			serverURL, err := urlvalidate.Validate(rawServer)
			if err != nil {
				return cliErrors.New(err.Error())
			}
			server := serverURL.String()

			ctx := rootContext(cmd)

			// Non-interactive --key path.
			if keyFlagSet {
				return loginWithKey(ctx, server, keyFlag)
			}

			return loginWithDeviceFlow(ctx, server, !noBrowser)
		},
	}

	cmd.Flags().StringVar(&serverFlag, "server", "", "Browzer server URL (default: $BROWZER_SERVER or "+config.DefaultServer+")")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not open the browser automatically")
	cmd.Flags().StringVar(&keyFlag, "key", "", "Skip device flow and log in with a pre-issued API key (reads $BROWZER_API_KEY when value is empty)")
	// cobra has no native "was the flag set?" — we read PreRunE.
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		keyFlagSet = cmd.Flags().Changed("key")
		return nil
	}

	parent.AddCommand(cmd)
}

// loginWithKey verifies the supplied API key against /api/auth/me and
// stores it as a credential with a far-future expiry. The key is never
// printed back to stdout.
func loginWithKey(ctx context.Context, server, keyArg string) error {
	if keyArg == "" {
		// Empty value (or `--key ''`) opts into the env fallback so the
		// key never hits process args / shell history on shared hosts.
		keyArg = os.Getenv(config.EnvAPIKey)
	}
	if keyArg == "" {
		return cliErrors.WithCode(
			"--key was passed but neither an inline value nor $BROWZER_API_KEY is set.",
			2,
		)
	}

	client := api.NewClient(server, keyArg, 0)
	me, err := client.GetMe(ctx)
	if err != nil {
		return cliErrors.WithCode(fmt.Sprintf("Failed to verify API key against %s (%s).", server, err.Error()), 2)
	}

	creds := &auth.Credentials{
		Server:         server,
		AccessToken:    keyArg,
		UserID:         me.UserID,
		OrganizationID: me.OrganizationID,
		// Far-future ISO timestamp — API keys have no fixed lifetime
		// known to the CLI; server-side revocation is the source of truth.
		ExpiresAt: "2099-12-31T23:59:59Z",
	}
	if err := auth.SaveCredentials(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	fmt.Println()
	ui.Success(fmt.Sprintf("Logged in as %s (%s) via API key.", me.Email, me.OrganizationName))
	return nil
}

// loginWithDeviceFlow runs the full RFC 8628 device flow with optional
// browser auto-open and TTY confirmation of the resolved identity.
func loginWithDeviceFlow(ctx context.Context, server string, openBrowser bool) error {
	ui.Arrow(fmt.Sprintf("Initiating device flow against %s...", server))

	device, err := auth.RequestDeviceCode(ctx, server, cliClientID)
	if err != nil {
		return cliErrors.New(err.Error())
	}

	verifyURL := device.VerificationURIComplete
	if verifyURL == "" {
		verifyURL = device.VerificationURI
	}

	// Validate the server-supplied URL BEFORE printing or opening it —
	// otherwise a compromised server can make the user copy a hijacked
	// link before we get a chance to reject it.
	if verifyURL != "" {
		if _, err := urlvalidate.Validate(verifyURL); err != nil {
			return cliErrors.Newf("Server returned an unsafe verification URL: %s", err.Error())
		}
	}

	fmt.Printf("\nFirst copy your one-time code: %s\nThen open: %s\n\n", device.UserCode, verifyURL)

	if openBrowser && verifyURL != "" {
		if err := browser.OpenURL(verifyURL); err != nil {
			fmt.Println("(Could not open browser automatically — visit the URL manually.)")
		}
	}

	fmt.Println("Waiting for authorization...")
	tokens, err := auth.PollForToken(ctx, auth.PollParams{
		Server:     server,
		DeviceCode: device.DeviceCode,
		Interval:   device.Interval,
		ExpiresIn:  device.ExpiresIn,
	})
	if err != nil {
		return err
	}

	// Verify identity BEFORE persisting credentials. Catches misconfigured
	// servers and confirms to the user that the device code they approved
	// was theirs.
	tempClient := api.NewClient(server, tokens.AccessToken, 0)
	me, err := tempClient.GetMe(ctx)
	if err != nil {
		return cliErrors.Newf("Failed to verify identity (%s). Aborting login.", err.Error())
	}

	fmt.Printf("\n  Signed in as: %s\n  Organization: %s\n\n", me.Email, me.OrganizationName)

	creds := &auth.Credentials{
		Server:         server,
		AccessToken:    tokens.AccessToken,
		UserID:         me.UserID,
		OrganizationID: me.OrganizationID,
		ExpiresAt:      time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).UTC().Format(time.RFC3339),
	}
	if err := auth.SaveCredentials(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	fmt.Println()
	ui.Success("Logged in.")
	return nil
}
