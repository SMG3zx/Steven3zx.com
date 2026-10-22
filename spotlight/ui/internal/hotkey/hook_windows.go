//go:build windows

package hotkey

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procKeybdEvent          = user32.NewProc("keybd_event")
	procGetCurrentThreadID  = kernel32.NewProc("GetCurrentThreadId")
)

const (
	whKeyboardLL  = 13
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105
	wmQuit        = 0x0012
	llkhfInjected = 0x10
	keyEventKeyUp = 0x0002
	vkUnassigned  = 0xE8 // never mapped to anything; used to cancel the Start menu
)

type kbdLLHook struct {
	vkCode, scanCode, flags, time uint32
	extra                         uintptr
}

// Listen installs a low-level keyboard hook and calls onFire (on its own
// goroutine) each time the combo is pressed. The combo's keystrokes are
// swallowed so Windows and the focused app never see them — which is what
// makes shortcuts the OS reserves, such as Win+Space, usable.
//
// A hook only sees keystrokes sent to windows at the same or lower privilege,
// so it does not fire while an elevated (administrator) window has focus
// unless this program is elevated too.
func Listen(c Combo, onFire func()) (stop func(), err error) {
	fire := make(chan struct{}, 1)
	done := make(chan struct{})
	ready := make(chan error, 1)
	var threadID uint32

	go func() {
		for {
			select {
			case <-fire:
				onFire()
			case <-done:
				return
			}
		}
	}()

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid, _, _ := procGetCurrentThreadID.Call()
		threadID = uint32(tid)

		m := newMatcher(c)
		cb := syscall.NewCallback(func(nCode int, wParam uintptr, lParam uintptr) uintptr {
			next := func() uintptr {
				r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
				return r
			}
			if nCode < 0 {
				return next()
			}
			ev := *(**kbdLLHook)(unsafe.Pointer(&lParam)) // lParam is a pointer owned by Windows for this call
			if ev.flags&llkhfInjected != 0 {              // ours (the Start-menu cancel) or another tool's
				return next()
			}
			var down bool
			switch wParam {
			case wmKeyDown, wmSysKeyDown:
				down = true
			case wmKeyUp, wmSysKeyUp:
			default:
				return next()
			}
			swallow, trigger := m.event(ev.vkCode, down)
			if trigger {
				if c.Win {
					cancelStartMenu()
				}
				select {
				case fire <- struct{}{}:
				default:
				}
			}
			if swallow {
				return 1
			}
			return next()
		})

		h, _, e := procSetWindowsHookExW.Call(whKeyboardLL, cb, 0, 0)
		if h == 0 {
			ready <- fmt.Errorf("install keyboard hook: %w", e)
			return
		}
		ready <- nil

		var msg [48]byte // MSG
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT or error
				break
			}
		}
		procUnhookWindowsHookEx.Call(h)
	}()

	if err := <-ready; err != nil {
		close(done)
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			if threadID != 0 {
				procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
			}
		})
	}, nil
}

// cancelStartMenu stops Windows opening the Start menu when the Win key is
// released after we swallowed the combo: a Win press followed by any other key
// is not "Win alone". An unassigned key does the job without side effects.
func cancelStartMenu() {
	procKeybdEvent.Call(vkUnassigned, 0, 0, 0)
	procKeybdEvent.Call(vkUnassigned, 0, keyEventKeyUp, 0)
}
