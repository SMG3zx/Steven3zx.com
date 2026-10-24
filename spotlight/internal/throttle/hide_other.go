//go:build !windows

package throttle

import "os/exec"

func hideConsole(*exec.Cmd) {}
