// Package errors defines the CLI's exit-code-aware error types.
//
// All commands wrap their errors in CliError so the top-level handler in
// cmd/browzer/main.go can call os.Exit with the conventional code:
//
//	0   success
//	1   generic error
//	2   not authenticated
//	3   no project
//	4   not found
//	5   quota / plan limit exceeded (402, 413)
//	6   rate limit / concurrency limit (429)
//	10  CLI outdated (browzer upgrade --check)
//	130 SIGINT
//	143 SIGTERM
package errors

import "fmt"

// Exit-code constants. Prefer these over bare integers when constructing
// a CliError so the mapping stays discoverable in one place.
const (
	ExitOK             = 0
	ExitError          = 1
	ExitAuthError      = 2
	ExitNoProject      = 3
	ExitNotFound       = 4
	ExitQuotaError     = 5  // 402 Payment Required, 413 Input Too Large
	ExitRateLimit      = 6  // 429 Too Many Requests / concurrency cap
	ExitPartialFailure = 7  // some docs failed ingestion (poll returned partial success)
	ExitTotalFailure   = 8  // all docs failed ingestion (poll returned no completions)
	ExitOutdated       = 10 // `browzer upgrade --check` found a newer release
)

// CliError is the base error type carrying an exit code. Wrap any
// command error in CliError before returning to ensure the right exit
// code is surfaced.
type CliError struct {
	Message  string
	ExitCode int
}

func (e *CliError) Error() string { return e.Message }

// New constructs a generic CliError with exit code 1.
func New(msg string) *CliError {
	return &CliError{Message: msg, ExitCode: 1}
}

// Newf constructs a CliError with formatting and exit code 1.
func Newf(format string, args ...any) *CliError {
	return &CliError{Message: fmt.Sprintf(format, args...), ExitCode: 1}
}

// WithCode constructs a CliError with a specific exit code.
func WithCode(msg string, code int) *CliError {
	return &CliError{Message: msg, ExitCode: code}
}

// NotAuthenticated returns a CliError with exit code 2. Use when the
// user has no credentials or the token is expired.
func NotAuthenticated() *CliError {
	return &CliError{
		Message:  "Not logged in. Run `browzer login` first.",
		ExitCode: 2,
	}
}

// NoProject returns a CliError with exit code 3. Use when .browzer/
// config.json is missing in the current git tree.
func NoProject() *CliError {
	return &CliError{
		Message:  "No Browzer project here. Run `browzer init` first.",
		ExitCode: 3,
	}
}

// NewQuotaExceededError returns a CliError with exit code 5. Use when
// the server rejects the request because the caller has exhausted a
// plan-level quota (402 Payment Required) or exceeded an input-size cap
// (413 Payload Too Large).
func NewQuotaExceededError(msg string) *CliError {
	return &CliError{Message: msg, ExitCode: ExitQuotaError}
}

// NewRateLimitError returns a CliError with exit code 6 and embeds the
// Retry-After hint (in seconds) directly into the message when > 0.
func NewRateLimitError(msg string, retryAfter int) *CliError {
	if retryAfter > 0 {
		msg = fmt.Sprintf("%s (retry after %ds)", msg, retryAfter)
	}
	return &CliError{Message: msg, ExitCode: ExitRateLimit}
}

// NotFound returns a CliError with exit code 4. Use when a resource
// (workspace, batch, document) cannot be found server-side.
func NotFound(what string) *CliError {
	return &CliError{
		Message:  fmt.Sprintf("%s not found.", what),
		ExitCode: 4,
	}
}
