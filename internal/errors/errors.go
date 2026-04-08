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
//	130 SIGINT
//	143 SIGTERM
package errors

import "fmt"

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

// NotFound returns a CliError with exit code 4. Use when a resource
// (workspace, batch, document) cannot be found server-side.
func NotFound(what string) *CliError {
	return &CliError{
		Message:  fmt.Sprintf("%s not found.", what),
		ExitCode: 4,
	}
}
