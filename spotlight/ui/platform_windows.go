//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procKeybdEvent          = user32.NewProc("keybd_event")
)

func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c
}

// openPath opens a file with its default application.
func openPath(path string) error {
	return hidden(exec.Command("rundll32", "url.dll,FileProtocolHandler", path)).Start()
}

// revealPath shows the file selected in Explorer. Explorer wants the
// "/select," argument and the path as one string, with no extra quoting.
func revealPath(path string) error {
	c := exec.Command("explorer.exe")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return c.Start()
}

// focusWindow pulls the launcher to the foreground. Windows only lets the
// foreground app do that, so a synthetic Alt tap first marks us as having
// just received input.
func focusWindow(title string) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
	if hwnd == 0 {
		return
	}
	const vkMenu, keyUp = 0x12, 0x2
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyUp, 0)
	procSetForegroundWindow.Call(hwnd)
}
