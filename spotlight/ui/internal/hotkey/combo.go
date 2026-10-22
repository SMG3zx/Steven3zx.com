// Package hotkey parses shortcut specs like "win+space" and, on Windows,
// listens for them globally.
package hotkey

import (
	"fmt"
	"strings"
)

// Combo is a shortcut: a set of modifiers plus one key.
type Combo struct {
	Win, Ctrl, Alt, Shift bool
	Key                   uint32 // Windows virtual-key code
}

var modNames = map[string]string{
	"win": "win", "windows": "win", "super": "win", "meta": "win", "cmd": "win",
	"ctrl": "ctrl", "control": "ctrl",
	"alt": "alt", "option": "alt",
	"shift": "shift",
}

var namedKeys = map[string]uint32{
	"space": 0x20, "enter": 0x0D, "return": 0x0D, "tab": 0x09,
	"backspace": 0x08, "esc": 0x1B, "escape": 0x1B,
	"`": 0xC0, "grave": 0xC0, "backtick": 0xC0,
	"-": 0xBD, "=": 0xBB, "[": 0xDB, "]": 0xDD, ";": 0xBA, "'": 0xDE,
	",": 0xBC, ".": 0xBE, "/": 0xBF, "\\": 0xDC,
}

// Parse reads "mod+mod+key", case-insensitively. At least one modifier is
// required, so a bare letter can never be swallowed system-wide.
func Parse(spec string) (Combo, error) {
	var c Combo
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(spec, " ", "")), "+")
	if len(parts) < 2 {
		return c, fmt.Errorf("hotkey %q needs at least one modifier and a key, e.g. win+space", spec)
	}
	key := parts[len(parts)-1]
	for _, m := range parts[:len(parts)-1] {
		switch modNames[m] {
		case "win":
			c.Win = true
		case "ctrl":
			c.Ctrl = true
		case "alt":
			c.Alt = true
		case "shift":
			c.Shift = true
		default:
			return c, fmt.Errorf("hotkey %q: unknown modifier %q", spec, m)
		}
	}
	vk, ok := keyCode(key)
	if !ok {
		return c, fmt.Errorf("hotkey %q: unknown key %q", spec, key)
	}
	c.Key = vk
	return c, nil
}

func keyCode(k string) (uint32, bool) {
	if vk, ok := namedKeys[k]; ok {
		return vk, true
	}
	if len(k) == 1 {
		switch ch := k[0]; {
		case ch >= 'a' && ch <= 'z':
			return uint32(ch - 'a' + 'A'), true
		case ch >= '0' && ch <= '9':
			return uint32(ch), true
		}
	}
	if len(k) >= 2 && k[0] == 'f' {
		n := 0
		for _, ch := range k[1:] {
			if ch < '0' || ch > '9' {
				return 0, false
			}
			n = n*10 + int(ch-'0')
		}
		if n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), true
		}
	}
	return 0, false
}

func (c Combo) String() string {
	var p []string
	if c.Win {
		p = append(p, "Win")
	}
	if c.Ctrl {
		p = append(p, "Ctrl")
	}
	if c.Alt {
		p = append(p, "Alt")
	}
	if c.Shift {
		p = append(p, "Shift")
	}
	return strings.Join(p, "+") + fmt.Sprintf("+0x%X", c.Key)
}

// mods is a bitmask of the modifiers currently held.
type mods uint8

const (
	modWin mods = 1 << iota
	modCtrl
	modAlt
	modShift
)

func (c Combo) mask() mods {
	var m mods
	if c.Win {
		m |= modWin
	}
	if c.Ctrl {
		m |= modCtrl
	}
	if c.Alt {
		m |= modAlt
	}
	if c.Shift {
		m |= modShift
	}
	return m
}

// matcher is the platform-independent state machine behind the Windows hook:
// feed it key events and it says which ones to swallow and when to fire.
type matcher struct {
	combo   Combo
	held    map[uint32]bool // physical keys down, by virtual-key code
	eaten   bool            // the main key's down event was swallowed
	pressed bool
}

func newMatcher(c Combo) *matcher { return &matcher{combo: c, held: map[uint32]bool{}} }

// modsHeld folds left/right and generic virtual keys into a bitmask.
func (m *matcher) modsHeld() mods {
	var r mods
	for vk, down := range m.held {
		if !down {
			continue
		}
		switch vk {
		case 0x5B, 0x5C:
			r |= modWin
		case 0x11, 0xA2, 0xA3:
			r |= modCtrl
		case 0x12, 0xA4, 0xA5:
			r |= modAlt
		case 0x10, 0xA0, 0xA1:
			r |= modShift
		}
	}
	return r
}

// event handles one key transition. swallow means the OS must not see it;
// fire means the shortcut was just triggered.
func (m *matcher) event(vk uint32, down bool) (swallow, fire bool) {
	if !down {
		delete(m.held, vk)
		if vk == m.combo.Key && m.eaten {
			m.eaten = false
			return true, false
		}
		return false, false
	}
	m.held[vk] = true
	if vk != m.combo.Key {
		return false, false
	}
	if m.eaten { // auto-repeat while held
		return true, false
	}
	if m.modsHeld() != m.combo.mask() {
		return false, false
	}
	m.eaten = true
	return true, true
}
