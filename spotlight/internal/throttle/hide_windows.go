package throttle

import (
	"os/exec"
	"syscall"
)

// hideConsole stops a child process from opening a console window. indexd runs
// without a console, so without this every nvidia-smi poll flashes a terminal.
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
