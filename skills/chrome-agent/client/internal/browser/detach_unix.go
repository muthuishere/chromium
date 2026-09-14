//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// detach starts the browser in its own session, so it survives the CLI exiting and a Ctrl-C in the
// terminal that ran `up`.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
