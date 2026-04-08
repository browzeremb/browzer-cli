package urlvalidate

import (
	"strings"
	"testing"
)

// allowed lists URLs that the CLI must accept under default config
// (no BROWZER_ALLOW_INSECURE). Loopback aliases + Railway internal
// hosts are the documented dev/prod affordances.
func TestValidate_AllowedHosts(t *testing.T) {
	cases := []string{
		"https://browzeremb.com",
		"https://api.example.com:8080/path",
		"http://localhost:3001",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"http://api.railway.internal:3002",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := Validate(raw); err != nil {
				t.Fatalf("expected %q to be allowed, got: %v", raw, err)
			}
		})
	}
}

// rejected lists URLs the SSRF / scheme guards must block. Each entry
// includes a substring expected in the error so we can detect drift.
func TestValidate_BlockedHosts(t *testing.T) {
	cases := []struct {
		raw, contains string
	}{
		// Cloud metadata services (AWS / GCP / Azure share 169.254.169.254).
		{"http://169.254.169.254", "metadata"},
		{"https://169.254.169.254/latest/meta-data/", "metadata"},
		// Private CIDRs.
		{"https://10.0.0.1", "private"},
		{"https://172.16.5.5", "private"},
		{"https://192.168.1.1", "private"},
		// Link-local outside the metadata IP.
		{"https://169.254.1.2", "private"},
		// Unspecified.
		{"https://0.0.0.0", "private"},
		// Reserved suffixes / mDNS.
		{"https://router.local", "reserved"},
		{"https://service.localhost", "reserved"},
		{"https://foo.test", "reserved"},
		// IPv6 missing brackets.
		{"http://::1:8080", "bracketed"},
		// Embedded credentials.
		{"https://user:pass@example.com", "userinfo"},
		// Unsupported schemes.
		{"file:///etc/passwd", "http or https"},
		{"javascript:alert(1)", "http or https"},
		{"data:text/plain,foo", "http or https"},
		// http:// to a non-loopback host without the opt-in.
		{"http://example.com", "non-loopback"},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			t.Setenv("BROWZER_ALLOW_INSECURE", "")
			_, err := Validate(c.raw)
			if err == nil {
				t.Fatalf("expected %q to be rejected", c.raw)
			}
			if !strings.Contains(err.Error(), c.contains) {
				t.Fatalf("expected error to mention %q, got: %v", c.contains, err)
			}
		})
	}
}

// BROWZER_ALLOW_INSECURE=1 lets http:// reach a public host but MUST
// NOT bypass the SSRF private-range guard.
func TestValidate_InsecureBypassDoesNotBypassSSRF(t *testing.T) {
	t.Setenv("BROWZER_ALLOW_INSECURE", "1")
	if _, err := Validate("http://example.com"); err != nil {
		t.Fatalf("BROWZER_ALLOW_INSECURE should allow http://example.com, got: %v", err)
	}
	if _, err := Validate("http://169.254.169.254"); err == nil {
		t.Fatalf("BROWZER_ALLOW_INSECURE must NOT bypass metadata-IP guard")
	}
	if _, err := Validate("http://10.0.0.1"); err == nil {
		t.Fatalf("BROWZER_ALLOW_INSECURE must NOT bypass private-CIDR guard")
	}
}
