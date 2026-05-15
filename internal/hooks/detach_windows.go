//go:build windows

package hooks

import "os/exec"

// ApplyDetachSysProcAttr is a no-op on Windows. Process-group
// separation on Windows would use CREATE_NEW_PROCESS_GROUP in the
// SysProcAttr.CreationFlags, but the Browzer daemon is not a primary
// Windows target (Claude Code's hooks run on macOS / Linux / WSL).
// Keeping this empty mirrors the analogous helper in
// internal/commands/daemon_detach_windows.go.
func ApplyDetachSysProcAttr(_ *exec.Cmd) {}
