//go:build windows

package commands

import "os/exec"

// detachProcess is a no-op on Windows.
//
// On Unix, Setsid puts the child in a new session so it outlives the
// parent CLI invocation. The Windows equivalent would be
// syscall.SysProcAttr{CreationFlags: CREATE_NEW_PROCESS_GROUP} but
// the Browzer daemon is not a supported Windows target — the CLI ships
// Windows binaries for workspace indexing commands only. A no-op here
// is intentional: `daemon start --background` on Windows will still
// launch the process but it will share the parent's console session.
func detachProcess(_ *exec.Cmd) {}
