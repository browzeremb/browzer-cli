// Package urlvalidate sanitises server URLs before handing them to
// browser launches or HTTP clients. Mirrors the legacy
// validateServerUrl in src/commands/login.ts and is the CLI's first
// line of defense against SSRF.
//
// Rules:
//
//   - Only http:// and https:// schemes (rejects file://, javascript:,
//     data:, etc — defends against handing arbitrary URIs to `open`).
//   - Hostnames that resolve to private / link-local / loopback /
//     unspecified IP literals are REJECTED unless they are explicitly
//     whitelisted (loopback for local dev; *.railway.internal for
//     Railway private networking).
//   - http:// is only allowed for the same loopback / railway-internal
//     whitelist. BROWZER_ALLOW_INSECURE=1 still bypasses the http→https
//     constraint, but it does NOT bypass the SSRF private-range guard.
//   - Hostnames ending in .local / .localhost / .test / .invalid /
//     .example are rejected (mDNS + reserved suffixes).
//   - 169.254.169.254 (AWS/GCP/Azure metadata) is always rejected.
//   - IPv6 literals must be bracketed (`http://[::1]:8080`); a bare
//     `http://::1:8080` is parsed by net/url as host `::1` + port `8080`
//     in a confusing way and is rejected.
package urlvalidate

import (
	"fmt"
	"net"
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
	// Reject embedded credentials in URL — they leak into logs and
	// would be sent on every request.
	if u.User != nil {
		return nil, fmt.Errorf("server URL must not include userinfo")
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("server URL is missing a host")
	}

	// Reject IPv6 literals that were not bracketed in the original
	// string. url.Parse silently accepts `http://::1:8080` and exposes
	// it as host `::1` + port `8080`, which is a foot-gun.
	if strings.Count(host, ":") >= 2 && !strings.HasPrefix(u.Host, "[") {
		return nil, fmt.Errorf("IPv6 hosts must be bracketed (e.g. http://[::1]:8080)")
	}

	// 1. Reserved / mDNS suffixes — never legitimate Browzer servers.
	if isReservedSuffix(host) {
		return nil, fmt.Errorf("refusing reserved/mDNS host %q", host)
	}

	loopback := isLoopbackHost(host)
	railway := isRailwayInternal(host)

	// 2. SSRF guard for IP literals: link-local, multicast, unspecified,
	//    private CIDRs, and the cloud metadata IP are always rejected
	//    unless the host is the loopback whitelist (which IS a private
	//    range but is a documented dev affordance).
	if ip := net.ParseIP(host); ip != nil {
		if isMetadataIP(ip) {
			return nil, fmt.Errorf("refusing cloud metadata IP %q", host)
		}
		if !loopback && isUnsafeIP(ip) {
			return nil, fmt.Errorf("refusing private/link-local/unspecified host %q", host)
		}
	}

	// 3. Scheme-vs-host rules — same as before, but tightened.
	if u.Scheme == "https" {
		return u, nil
	}
	// http:// — allow only loopback / *.railway.internal / opt-in.
	if loopback || railway {
		return u, nil
	}
	if config.AllowInsecure() {
		return u, nil
	}
	return nil, fmt.Errorf(
		"refusing to use http:// for non-loopback host %q "+
			"(set BROWZER_ALLOW_INSECURE=1 to override)",
		host,
	)
}

// isLoopbackHost returns true for the documented loopback aliases. We
// keep the literal-string list (not net.IP.IsLoopback) because we want
// `localhost` to count even when DNS isn't consulted.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isRailwayInternal(host string) bool {
	return strings.HasSuffix(strings.ToLower(host), ".railway.internal")
}

// isReservedSuffix flags hostnames that target the local network or
// reserved TLDs. We reject mDNS (`.local`), the IETF special-use names
// (`.localhost`, `.test`, `.invalid`, `.example`) and bare `0`.
func isReservedSuffix(host string) bool {
	h := strings.ToLower(host)
	if h == "0" {
		return true
	}
	for _, suf := range []string{".local", ".localhost", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(h, suf) {
			// `localhost` itself is whitelisted by isLoopbackHost above;
			// only the *.localhost suffix is rejected here.
			return true
		}
	}
	return false
}

// isMetadataIP matches the cloud metadata service across providers.
func isMetadataIP(ip net.IP) bool {
	return ip.Equal(net.ParseIP("169.254.169.254")) ||
		ip.Equal(net.ParseIP("fd00:ec2::254"))
}

// isUnsafeIP returns true for any IP that should never be a CLI target:
// private CIDRs, link-local, multicast, unspecified (0.0.0.0 / ::), and
// the broadcast address. Loopback is checked separately so callers can
// keep it as a documented dev affordance.
func isUnsafeIP(ip net.IP) bool {
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// 0.0.0.0 is covered by IsUnspecified, but the IPv4 broadcast
	// 255.255.255.255 is not — block it too.
	if v4 := ip.To4(); v4 != nil && v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255 {
		return true
	}
	return false
}
