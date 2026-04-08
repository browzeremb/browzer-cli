package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempHome isolates BROWZER_HOME for the test so we never touch the
// developer's real ~/.browzer/.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)
	return dir
}

func TestSaveAndLoadCredentials(t *testing.T) {
	withTempHome(t)
	c := &Credentials{
		Server:         "https://example.com",
		AccessToken:    "tok-123",
		OrganizationID: "org-1",
		UserID:         "user-1",
		ExpiresAt:      time.Now().Add(2 * time.Hour).Format(time.RFC3339Nano),
	}
	if err := SaveCredentials(c); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := LoadCredentials()
	if got == nil {
		t.Fatal("LoadCredentials returned nil")
	}
	if got.AccessToken != "tok-123" || got.OrganizationID != "org-1" {
		t.Errorf("round trip mismatch: %+v", got)
	}

	// File mode should be 600.
	info, err := os.Stat(CredentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", info.Mode().Perm())
	}

	// Dir mode should be 700.
	dirInfo, err := os.Stat(filepath.Dir(CredentialsPath()))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("credentials dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
}

func TestLoadCredentialsMissing(t *testing.T) {
	withTempHome(t)
	if got := LoadCredentials(); got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestClearCredentials(t *testing.T) {
	withTempHome(t)
	c := &Credentials{Server: "x", AccessToken: "y", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	_ = SaveCredentials(c)
	if err := ClearCredentials(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := LoadCredentials(); got != nil {
		t.Errorf("expected nil after clear, got %+v", got)
	}
	// Idempotent — clearing an already-clear store must not error.
	if err := ClearCredentials(); err != nil {
		t.Errorf("second clear errored: %v", err)
	}
}

func TestIsTokenExpiring(t *testing.T) {
	cases := []struct {
		name string
		exp  string
		want bool
	}{
		{"empty", "", true},
		{"malformed", "not-a-date", true},
		{"already expired", time.Now().Add(-time.Hour).Format(time.RFC3339), true},
		{"within 5min window", time.Now().Add(2 * time.Minute).Format(time.RFC3339), true},
		{"plenty of time left", time.Now().Add(2 * time.Hour).Format(time.RFC3339), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsTokenExpiring(&Credentials{ExpiresAt: c.exp})
			if got != c.want {
				t.Errorf("IsTokenExpiring(%q) = %v, want %v", c.exp, got, c.want)
			}
		})
	}
}
