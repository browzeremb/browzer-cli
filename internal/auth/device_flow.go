package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// RFC 8628 §3.5 mandates a 5s floor; cap to 60s to keep UX snappy.
const (
	minIntervalSeconds = 5
	maxIntervalSeconds = 60
	// Clamp the device-code lifetime so a hostile server cannot pin
	// the CLI to a year-long poll.
	minExpiresSeconds = 60
	maxExpiresSeconds = 1800
	// Belt-and-braces ceiling on poll attempts.
	maxPollCount        = 720
	slowDownBumpSeconds = 5
)

const grantType = "urn:ietf:params:oauth:grant-type:device_code"

// DeviceCodeResponse mirrors POST /api/device/code's body.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// TokenResponse mirrors POST /api/device/token's success body.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// PollParams mirrors the legacy PollParams in src/lib/device-flow.ts.
type PollParams struct {
	Server     string
	DeviceCode string
	Interval   int
	ExpiresIn  int
	ClientID   string
}

// RequestDeviceCode calls POST /api/device/code.
func RequestDeviceCode(ctx context.Context, server, clientID string) (*DeviceCodeResponse, error) {
	body := url.Values{}
	body.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(server, "/")+"/api/device/code", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("device code request failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var out DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func clamp(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// PollForToken polls POST /api/device/token until success or terminal
// error. Mirrors src/lib/device-flow.ts:pollForToken byte-for-byte:
// clamps the interval/expiresIn against hostile servers and caps total
// attempts at maxPollCount.
func PollForToken(ctx context.Context, p PollParams) (*TokenResponse, error) {
	clientID := p.ClientID
	if clientID == "" {
		clientID = "browzer-cli"
	}

	interval := clamp(p.Interval, minIntervalSeconds, maxIntervalSeconds)
	expiresIn := clamp(p.ExpiresIn, minExpiresSeconds, maxExpiresSeconds)
	startedAt := time.Now()
	attempt := 0

	for {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(interval) * time.Second):
			}
		}
		attempt++
		if attempt > maxPollCount {
			return nil, cliErrors.New("Authorization polling exceeded the safety cap. Run `browzer login` again.")
		}
		if int(time.Since(startedAt).Seconds()) > expiresIn {
			return nil, cliErrors.New("Authorization window timeout. Run `browzer login` again.")
		}

		body := url.Values{}
		body.Set("grant_type", grantType)
		body.Set("device_code", p.DeviceCode)
		body.Set("client_id", clientID)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.Server, "/")+"/api/device/token", strings.NewReader(body.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusOK {
			var token TokenResponse
			err := json.NewDecoder(resp.Body).Decode(&token)
			resp.Body.Close()
			if err != nil {
				return nil, err
			}
			return &token, nil
		}

		var errBody struct {
			Error string `json:"error"`
		}
		// Try to parse the error code; ignore decode errors.
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		resp.Body.Close()

		switch errBody.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += slowDownBumpSeconds
			if interval > maxIntervalSeconds {
				interval = maxIntervalSeconds
			}
			continue
		case "expired_token":
			return nil, cliErrors.New("Authorization window expired. Run `browzer login` again.")
		case "invalid_grant":
			return nil, cliErrors.New("Device code rejected by server. Run `browzer login` again.")
		case "unauthorized_client":
			return nil, cliErrors.New("CLI client_id not allowed by server.")
		default:
			return nil, cliErrors.Newf("Unexpected token endpoint response (status %d, error=%s).", resp.StatusCode, errBody.Error)
		}
	}
}

// ErrPollTimeout is the sentinel returned when the device-code window
// expires before the user approves.
var ErrPollTimeout = errors.New("device-code authorization window expired")
