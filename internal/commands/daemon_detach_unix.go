//go:build !windows

package commands

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the spawned daemon into a new session so it
// outlives the parent CLI invocation. Unix-only — on Windows the
// caller relies on CREATE_NEW_PROCESS_GROUP via a separate build file.
func detachProcess(p *exec.Cmd) {
	p.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
