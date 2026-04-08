package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// newStubServer returns a test server that always responds with the
// given status + body + headers. Keeps the table-driven test below
// compact.
func newStubServer(t *testing.T, status int, body string, headers map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestHTTPStatusErrors covers the new 402/413/423/429/403-model
// branches in httpStatusError. For each case we assert both the exit
// code and that the rendered message contains a meaningful fragment.
func TestHTTPStatusErrors(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		headers     map[string]string
		wantExit    int
		wantContain string
	}{
		{
			name:        "402 quota exceeded",
			status:      402,
			body:        `{"error":"quota_exceeded","message":"monthly cap hit","hint":"upgrade your plan","details":{"resetAt":"2026-05-01T00:00:00Z"}}`,
			wantExit:    cliErrors.ExitQuotaError,
			wantContain: "monthly cap hit",
		},
		{
			name:        "402 includes resetAt",
			status:      402,
			body:        `{"error":"quota_exceeded","message":"cap","hint":"upgrade","details":{"resetAt":"2026-05-01"}}`,
			wantExit:    cliErrors.ExitQuotaError,
			wantContain: "Renova em: 2026-05-01",
		},
		{
			name:        "413 input too large",
			status:      413,
			body:        `{"error":"input_tokens_exceeded","message":"too many tokens","hint":"shorter prompt"}`,
			wantExit:    cliErrors.ExitQuotaError,
			wantContain: "too many tokens",
		},
		{
			name:        "423 api key blocked",
			status:      423,
			body:        `{"error":"api_key_blocked","message":"circuit open","hint":"wait 5m"}`,
			wantExit:    cliErrors.ExitAuthError,
			wantContain: "circuit open",
		},
		{
			name:        "429 rate limit with Retry-After",
			status:      429,
			body:        `{"error":"rate_limit_exceeded","message":"slow down"}`,
			headers:     map[string]string{"Retry-After": "42"},
			wantExit:    cliErrors.ExitRateLimit,
			wantContain: "42",
		},
		{
			name:        "429 concurrency limit",
			status:      429,
			body:        `{"error":"concurrency_limit_exceeded","message":"too many inflight"}`,
			wantExit:    cliErrors.ExitRateLimit,
			wantContain: "simultâneas",
		},
		{
			name:        "403 model_restricted_by_plan",
			status:      403,
			body:        `{"error":"model_restricted_by_plan","message":"gpt-4o not on free","hint":"upgrade"}`,
			wantExit:    cliErrors.ExitError,
			wantContain: "gpt-4o not on free",
		},
		{
			name:        "403 generic forbidden",
			status:      403,
			body:        `forbidden`,
			wantExit:    cliErrors.ExitError,
			wantContain: "Forbidden",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStubServer(t, tc.status, tc.body, tc.headers)
			defer srv.Close()

			c := NewClient(srv.URL, "tok", 0)
			// Use POST so 429 is not retried (POST is non-retryable)
			// — otherwise the retry loop would exhaust all 4 attempts
			// before returning and slow the test.
			err := c.postJSON(context.Background(), "api/anything", map[string]string{"k": "v"}, nil)
			if err == nil {
				t.Fatalf("expected error")
			}
			var ce *cliErrors.CliError
			if !errors.As(err, &ce) {
				t.Fatalf("expected CliError, got %T: %v", err, err)
			}
			if ce.ExitCode != tc.wantExit {
				t.Errorf("exit code: want %d, got %d", tc.wantExit, ce.ExitCode)
			}
			if !strings.Contains(ce.Message, tc.wantContain) {
				t.Errorf("message missing %q: %q", tc.wantContain, ce.Message)
			}
		})
	}
}
