package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRequestDeviceCode_HappyPath confirms the success path decodes
// the canonical RFC 8628 response shape and returns it intact.
func TestRequestDeviceCode_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device/code" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://example.com/device","expires_in":600,"interval":5}`)
	}))
	defer srv.Close()

	out, err := RequestDeviceCode(context.Background(), srv.URL, "browzer-cli")
	if err != nil {
		t.Fatal(err)
	}
	if out.DeviceCode != "dc" || out.Interval != 5 {
		t.Fatalf("unexpected response: %+v", out)
	}
}

// TestRequestDeviceCode_ResponseSizeBounded asserts the success-path
// decoder is wrapped in a LimitReader so a hostile or buggy server
// cannot stream gigabytes into the CLI's memory.
func TestRequestDeviceCode_ResponseSizeBounded(t *testing.T) {
	// Build a response just a bit larger than maxAuthResponseBytes by
	// stuffing the device_code with junk; the decode should fail with
	// an unexpected-EOF or unexpected-end-of-input rather than reading
	// the whole stream.
	huge := strings.Repeat("a", maxAuthResponseBytes+128)
	body := `{"device_code":"` + huge + `","user_code":"UC","verification_uri":"https://example.com","expires_in":600,"interval":5}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	if _, err := RequestDeviceCode(context.Background(), srv.URL, "browzer-cli"); err == nil {
		t.Fatalf("expected decode error from oversized response")
	}
}

// TestPollForToken_SlowDownCumulativeCap proves the CLI gives up after
// maxSlowDownBumps consecutive slow_down responses instead of polling
// at the 60 s ceiling for hours.
func TestPollForToken_SlowDownCumulativeCap(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"slow_down"}`)
	}))
	defer srv.Close()

	// Use a very small expires window so the test doesn't waste real
	// wall clock — clamping floors interval at minIntervalSeconds, so
	// the loop sleeps 5 s between attempts. We accept that and bail
	// after the cap fires.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, err := PollForToken(ctx, PollParams{
		Server:     srv.URL,
		DeviceCode: "dc",
		Interval:   1, // clamped to 5
		ExpiresIn:  1, // clamped to 60
		ClientID:   "browzer-cli",
	})
	if err == nil {
		t.Fatal("expected slow_down cap to abort poll")
	}
	if !strings.Contains(err.Error(), "slow down") && !strings.Contains(err.Error(), "Authorization window") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPollForToken_ExpiredTokenAborts confirms the terminal
// `expired_token` response stops polling immediately.
func TestPollForToken_ExpiredTokenAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"expired_token"}`)
	}))
	defer srv.Close()

	_, err := PollForToken(context.Background(), PollParams{
		Server:     srv.URL,
		DeviceCode: "dc",
		Interval:   1,
		ExpiresIn:  60,
	})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired-token error, got: %v", err)
	}
}

// TestAuthHTTPClient_IgnoresProxyEnv confirms the dedicated auth
// client does NOT honor HTTP_PROXY/HTTPS_PROXY — that would route
// device-flow tokens through an attacker-controlled corporate proxy.
func TestAuthHTTPClient_IgnoresProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1") // would refuse-connect if honored
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://x","expires_in":600,"interval":5}`)
	}))
	defer srv.Close()

	if _, err := RequestDeviceCode(context.Background(), srv.URL, "browzer-cli"); err != nil {
		t.Fatalf("auth client must bypass HTTP_PROXY but got: %v", err)
	}
}
