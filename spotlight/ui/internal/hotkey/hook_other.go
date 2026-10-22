//go:build !windows

package hotkey

import "errors"

// Listen is not implemented off Windows: there is no portable global hotkey
// API (X11, Wayland and macOS each differ). Bind a desktop shortcut to the
// launcher instead; launching it again toggles the window.
func Listen(Combo, func()) (func(), error) {
	return nil, errors.New("global hotkeys are only implemented on Windows; bind a desktop shortcut to the spotlight-ui executable instead")
}
