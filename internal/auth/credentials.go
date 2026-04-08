// Package auth handles ~/.browzer/credentials I/O, token expiry checks,
// and the RFC 8628 device-flow polling.
package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
)

// Credentials is the stored credential set for a single profile.
//
// RefreshToken is kept for backward compatibility with credentials
// files written before Sub-fase 2.B.1; new logins leave it empty.
type Credentials struct {
	Server         string `json:"server"`
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
	ExpiresAt      string `json:"expires_at"`
}

// credentialsFile is the on-disk structure. Keyed by profile name so
// multi-profile support drops in without a file format migration.
type credentialsFile struct {
	Default Credentials `json:"default"`
}

// reloginWindow: refuse early when this close to expiry.
const reloginWindow = 5 * time.Minute

func browzerHome() string {
	if h := config.HomeOverride(); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// CredentialsDir returns the directory holding the credentials file
// (~/.browzer or $BROWZER_HOME/.browzer).
func CredentialsDir() string {
	return filepath.Join(browzerHome(), ".browzer")
}

// CredentialsPath returns the absolute path to the credentials file.
func CredentialsPath() string {
	return filepath.Join(CredentialsDir(), "credentials")
}

// LoadCredentials reads ~/.browzer/credentials and returns the default
// profile. Returns nil when the file is missing, malformed, or has no
// default profile. Never returns an error from this function — callers
// should treat nil as "not authenticated".
func LoadCredentials() *Credentials {
	data, err := os.ReadFile(CredentialsPath())
	if err != nil {
		return nil
	}
	var cf credentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil
	}
	if cf.Default.AccessToken == "" {
		return nil
	}
	return &cf.Default
}

// SaveCredentials writes credentials atomically (temp file + rename)
// with chmod 600. Ensures the .browzer/ directory exists with chmod 700.
func SaveCredentials(c *Credentials) error {
	dir := CredentialsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Tighten perms even if the dir already existed with wider bits.
	_ = os.Chmod(dir, 0o700)

	final := CredentialsPath()
	tmp := final + ".tmp"

	data, err := json.MarshalIndent(credentialsFile{Default: *c}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	return os.Rename(tmp, final)
}

// ClearCredentials removes the credentials file. No-op when absent.
func ClearCredentials() error {
	err := os.Remove(CredentialsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// IsTokenExpiring returns true when the access token is missing
// expires_at, already expired, or within the relogin window of expiring.
func IsTokenExpiring(c *Credentials) bool {
	if c == nil || c.ExpiresAt == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if err != nil {
		// Try plain RFC3339 for credentials written without nanos.
		exp, err = time.Parse(time.RFC3339, c.ExpiresAt)
		if err != nil {
			return true
		}
	}
	return time.Until(exp) < reloginWindow
}
