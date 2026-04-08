// Package config exposes env-var overrides and the project config
// file (.browzer/config.json) reader/writer.
package config

import "os"

// Env-var names honored by the CLI. Mirrors the legacy Node CLI.
const (
	EnvHome           = "BROWZER_HOME"
	EnvServer         = "BROWZER_SERVER"
	EnvAPIKey         = "BROWZER_API_KEY"
	EnvAllowInsecure  = "BROWZER_ALLOW_INSECURE"
)

// DefaultServer is the production server URL used when neither
// BROWZER_SERVER nor --server is set.
const DefaultServer = "https://browzeremb.com"

// ServerOverride returns BROWZER_SERVER when set, otherwise empty.
// Callers should fall back to flag value or DefaultServer.
func ServerOverride() string { return os.Getenv(EnvServer) }

// HomeOverride returns BROWZER_HOME when set, otherwise empty.
func HomeOverride() string { return os.Getenv(EnvHome) }

// AllowInsecure returns true when BROWZER_ALLOW_INSECURE=1 is set.
// Used to bypass the HTTPS-only check on non-loopback hosts.
func AllowInsecure() bool { return os.Getenv(EnvAllowInsecure) == "1" }
