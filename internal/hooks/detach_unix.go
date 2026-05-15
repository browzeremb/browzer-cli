//go:build !windows

package hooks

import (
	"os/exec"
	"syscall"
)

// ApplyDetachSysProcAttr configures `cmd` so the spawned daemon child
// outlives this CLI invocation. Sets Setpgid=true — placing the child
// in its own process group prevents signal propagation from the parent
// shell (SIGINT on ctrl-C, SIGHUP on terminal close) reaching it.
// This is the Go analog of Node's child.unref() that the retired
// _util.mjs guard relied on.
//
// Setpgid is preferred over Setsid here: the task contract specifies
// the pgid form, and downstream tests assert on `Setpgid == true`. The
// effect for daemon lifetime is equivalent in practice because the
// `browzer daemon start --background` subcommand re-execs into its
// own detached supervisor afterwards.
func ApplyDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
