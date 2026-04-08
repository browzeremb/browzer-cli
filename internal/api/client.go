package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/auth"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// DefaultTimeout matches ky's 30s default in src/lib/api-client.ts.
const DefaultTimeout = 30 * time.Second

// Client is a thin wrapper around net/http that injects the bearer
// token, prepends the base URL, and applies retry/backoff to idempotent
// verbs (GET/HEAD/PUT/DELETE only — POST is intentionally non-retryable
// because /documents/batch and /workspaces/parse aren't idempotent).
type Client struct {
	BaseURL    string
	Token      string
	HTTP       *http.Client
	UserAgent  string
}

// NewClient builds a Client. Use NewAuthenticatedClient when you need
// to read credentials from disk.
func NewClient(server, token string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		BaseURL: strings.TrimRight(server, "/"),
		Token:   token,
		HTTP: &http.Client{
			Timeout: timeout,
		},
		UserAgent: "browzer-cli/go",
	}
}

// AuthenticatedClient is the analog of createAuthenticatedClient in the
// Node CLI. Returns the credentials alongside the ready-to-use client
// so callers can persist `server` etc. without re-loading from disk.
type AuthenticatedClient struct {
	Client      *Client
	Credentials *auth.Credentials
}

// NewAuthenticatedClient loads credentials from disk and returns an
// authenticated Client. Returns NotAuthenticated if no credentials are
// stored, or a CliError if the token is expired/expiring.
func NewAuthenticatedClient(timeout time.Duration) (*AuthenticatedClient, error) {
	creds := auth.LoadCredentials()
	if creds == nil {
		return nil, cliErrors.NotAuthenticated()
	}
	if auth.IsTokenExpiring(creds) {
		return nil, cliErrors.New("Your Browzer credentials are expired or about to expire. Run `browzer login` again.")
	}
	return &AuthenticatedClient{
		Client:      NewClient(creds.Server, creds.AccessToken, timeout),
		Credentials: creds,
	}, nil
}

// retryableStatuses are the HTTP status codes treated as transient. The
// list mirrors ky's defaults: 408, 429, 500, 502, 503, 504.
var retryableStatuses = map[int]bool{
	408: true,
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
}

// retryableMethods are the verbs we retry. POST is intentionally absent.
var retryableMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodHead:   true,
	http.MethodPut:    true,
	http.MethodDelete: true,
}

// do is the core request runner. It builds an absolute URL, attaches
// auth headers, retries idempotent verbs on transient failures with a
// capped backoff, and returns the raw http.Response on success.
//
// The caller is responsible for closing the response body.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	full := c.BaseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	const maxAttempts = 4 // 1 initial + 3 retries
	var resp *http.Response

	// Buffer the body once so we can replay on retry. Non-retryable
	// methods (POST) skip this and pass the io.Reader through directly.
	var bodyBuf []byte
	if body != nil && retryableMethods[method] {
		var err error
		bodyBuf, err = io.ReadAll(body)
		if err != nil {
			return nil, err
		}
	}

	backoffs := []time.Duration{500 * time.Millisecond, 2 * time.Second, 8 * time.Second}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var reqBody io.Reader
		if bodyBuf != nil {
			reqBody = bytes.NewReader(bodyBuf)
		} else {
			reqBody = body
		}

		req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
		if err != nil {
			return nil, err
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Accept", "application/json")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}

		resp, err = c.HTTP.Do(req)
		if err != nil {
			// Network-level error: retry only on idempotent verbs.
			if !retryableMethods[method] || attempt == maxAttempts-1 {
				return nil, err
			}
		} else if !retryableStatuses[resp.StatusCode] || !retryableMethods[method] {
			return resp, nil
		} else {
			// Retryable status — drain and close before sleeping.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		if attempt < maxAttempts-1 {
			delay := backoffs[attempt]
			if delay > 30*time.Second {
				delay = 30 * time.Second // backoffLimit cap
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	if resp == nil {
		return nil, errors.New("request failed after retries")
	}
	return resp, nil
}

// getJSON is a convenience wrapper that issues a GET and decodes the
// JSON body into out. Returns a CliError with the right exit code on
// 401/404, otherwise a generic error with the status code.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeJSONResponse(resp, out)
}

// postJSON marshals body as JSON, POSTs it, and decodes the response
// into out (when non-nil).
func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	resp, err := c.do(ctx, http.MethodPost, path, nil, reader, "application/json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeJSONResponse(resp, out)
}

// deleteCall issues a DELETE and discards the body.
func (c *Client) deleteCall(ctx context.Context, path string, query url.Values) error {
	resp, err := c.do(ctx, http.MethodDelete, path, query, nil, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return httpStatusError(resp)
}

// decodeJSONResponse handles common status codes and decodes the body
// into out when non-nil.
func decodeJSONResponse(resp *http.Response, out any) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return httpStatusError(resp)
}

// httpStatusError reads up to 1 KiB of the response body and wraps it
// in a CliError carrying the appropriate exit code.
func httpStatusError(resp *http.Response) error {
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	bodyStr := strings.TrimSpace(string(bodyBytes))
	switch resp.StatusCode {
	case 401:
		return cliErrors.WithCode("Unauthorized — run `browzer login` again.", 2)
	case 403:
		return cliErrors.WithCode("Forbidden — your token does not have access to this resource.", 1)
	case 404:
		return cliErrors.WithCode("Not found.", 4)
	default:
		msg := fmt.Sprintf("server returned %d", resp.StatusCode)
		if bodyStr != "" {
			msg += ": " + bodyStr
		}
		return cliErrors.New(msg)
	}
}
