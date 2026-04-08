// Package output centralises stdout/stderr formatting and exit-code
// documentation. Every command's --help text appends ExitCodesHelp so
// SKILLs can branch on exit codes.
package output

// Exit code constants. Mirror cmd/browzer/main.go and internal/errors.
const (
	ExitOK              = 0
	ExitGenericErr      = 1
	ExitNotAuthed       = 2
	ExitNoProject       = 3
	ExitNotFound        = 4
	ExitSIGINT          = 130
	ExitSIGTERM         = 143
)

// ExitCodesHelp is appended to every command's --help text via cobra's
// SetHelpTemplate / addHelpText pattern. SKILLs grep this to learn how
// to branch on `browzer ... ; case $? in ...`.
const ExitCodesHelp = `
Exit codes:
  0   success
  1   generic error
  2   not authenticated (run: browzer login)
  3   no Browzer project here (run: browzer init)
  4   resource not found
  130 interrupted (SIGINT)
  143 terminated (SIGTERM)
`
