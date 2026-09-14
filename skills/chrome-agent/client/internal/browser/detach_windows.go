//go:build windows

package browser

import "os/exec"

func detach(cmd *exec.Cmd) {}
