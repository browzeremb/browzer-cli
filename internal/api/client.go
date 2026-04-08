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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/auth"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// DefaultTimeout matches ky's 30s default in src/lib/api-client.ts.
const DefaultTimeout = 30 * time.Second

// MaxResponseBytes caps every JSON response body the CLI will decode.
// 32 MiB is comfortably above the largest plausible workspace/document
// payload the API returns and well below anything that would OOM a
// developer machine. A hostile server cannot stream gigabytes into us.
const MaxResponseBytes = 32 * 1024 * 1024

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
// into out when non-nil. The body is wrapped in an io.LimitReader so a
// hostile or buggy server cannot stream gigabytes into the decoder.
func decodeJSONResponse(resp *http.Response, out any) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		return json.NewDecoder(io.LimitReader(resp.Body, MaxResponseBytes)).Decode(out)
	}
	return httpStatusError(resp)
}

// httpStatusError reads up to 1 KiB of the response body and wraps it
// in a CliError carrying the appropriate exit code.
//
// The server-supplied body excerpt is only echoed when BROWZER_DEBUG=1
// is set — otherwise we surface the bare HTTP status. This avoids
// leaking server-side debug payloads (stack traces, internal paths,
// SQL fragments) into stderr/CI logs by default.
func httpStatusError(resp *http.Response) error {
	// Read up to 4 KiB so JSON envelopes with `details` survive —
	// the legacy 1 KiB cap was fine for bare text bodies, but 402/413
	// envelopes carry structured hints the user needs to see.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyStr := strings.TrimSpace(string(bodyBytes))
	env := parseErrorEnvelope(bodyBytes)

	switch resp.StatusCode {
	case 401:
		return cliErrors.WithCode("Unauthorized — run `browzer login` again.", cliErrors.ExitAuthError)
	case 402:
		// Plan quota exhausted — /ask, /search, ingestion hot paths.
		msg := formatEnvelope("⚠ Cota esgotada", env, bodyStr)
		if reset := envString(env, "details", "resetAt"); reset != "" {
			msg += "\nRenova em: " + reset
		}
		return cliErrors.NewQuotaExceededError(msg)
	case 403:
		// The new `model_restricted_by_plan` error shares 403 with the
		// legacy "token does not have access" path. Disambiguate via
		// the envelope's `error` discriminator.
		if env != nil && env.Error == "model_restricted_by_plan" {
			return cliErrors.WithCode(formatEnvelope("⛔ Modelo não disponível no seu plano", env, bodyStr), cliErrors.ExitError)
		}
		return cliErrors.WithCode("Forbidden — your token does not have access to this resource.", cliErrors.ExitError)
	case 404:
		return cliErrors.WithCode("Not found.", cliErrors.ExitNotFound)
	case 413:
		// Input tokens exceed plan cap.
		return cliErrors.NewQuotaExceededError(formatEnvelope("⚠ Input muito grande", env, bodyStr))
	case 423:
		// Circuit breaker blocked this API key.
		return cliErrors.WithCode(formatEnvelope("🚫 API key bloqueada", env, bodyStr), cliErrors.ExitAuthError)
	case 429:
		retryAfter := 0
		if h := resp.Header.Get("Retry-After"); h != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil {
				retryAfter = n
			}
		}
		// `concurrency_limit_exceeded` is a distinct failure mode —
		// waiting doesn't help, only fewer inflight requests does.
		if env != nil && env.Error == "concurrency_limit_exceeded" {
			return cliErrors.NewRateLimitError("⏱ Muitas requests simultâneas. Aguarde as anteriores finalizarem ou faça upgrade.", 0)
		}
		msg := "⏱ Rate limit atingido — reduza o ritmo das requests"
		if retryAfter > 0 {
			msg = fmt.Sprintf("⏱ Rate limit atingido — aguarde %ds antes de tentar novamente", retryAfter)
		}
		return cliErrors.NewRateLimitError(msg, retryAfter)
	default:
		msg := fmt.Sprintf("server returned %d", resp.StatusCode)
		if bodyStr != "" && os.Getenv("BROWZER_DEBUG") == "1" {
			msg += ": " + bodyStr
		}
		return cliErrors.New(msg)
	}
}

// errorEnvelope mirrors the `{ error, message, hint, details, docsUrl }`
// shape that apps/api now returns for billing/quota failures. `details`
// is kept as raw JSON (decoded lazily via envString) so we don't have
// to enumerate every possible sub-key.
type errorEnvelope struct {
	Error   string                 `json:"error"`
	Message string                 `json:"message"`
	Hint    string                 `json:"hint"`
	Details map[string]any         `json:"details"`
	DocsURL string                 `json:"docsUrl"`
}

// parseErrorEnvelope best-effort decodes the response body. Returns nil
// if the body is empty or not valid JSON — callers MUST handle nil.
func parseErrorEnvelope(body []byte) *errorEnvelope {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil
	}
	return &env
}

// formatEnvelope renders an envelope as "prefix: message\n💡 hint".
// Falls back to the raw body snippet when no envelope is present so the
// user still sees *something* useful on an unexpected wire shape.
func formatEnvelope(prefix string, env *errorEnvelope, fallback string) string {
	if env == nil || (env.Message == "" && env.Hint == "") {
		if fallback != "" && os.Getenv("BROWZER_DEBUG") == "1" {
			return prefix + ": " + fallback
		}
		return prefix
	}
	out := prefix
	if env.Message != "" {
		out += ": " + env.Message
	}
	if env.Hint != "" {
		out += "\n💡 " + env.Hint
	}
	if env.DocsURL != "" {
		out += "\n" + env.DocsURL
	}
	return out
}

// envString walks env.Details following the provided key path and
// returns the leaf as a string. Returns "" on any miss — keeps the
// call site concise at the cost of silent lookups.
func envString(env *errorEnvelope, keys ...string) string {
	if env == nil || len(keys) == 0 {
		return ""
	}
	// First key is the top-level field name — only "details" is
	// supported today, but routing through a switch keeps the door
	// open for future top-level fields without breaking callers.
	if keys[0] != "details" || env.Details == nil {
		return ""
	}
	var cur any = env.Details
	for _, k := range keys[1:] {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}
