package hotkey

import "testing"

func TestParse(t *testing.T) {
	good := map[string]Combo{
		"win+space":       {Win: true, Key: 0x20},
		"Ctrl + Alt + K":  {Ctrl: true, Alt: true, Key: 'K'},
		"alt+space":       {Alt: true, Key: 0x20},
		"super+shift+f12": {Win: true, Shift: true, Key: 0x7B},
		"ctrl+`":          {Ctrl: true, Key: 0xC0},
		"windows+1":       {Win: true, Key: '1'},
	}
	for spec, want := range good {
		got, err := Parse(spec)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", spec, got, err, want)
		}
	}
	for _, spec := range []string{"", "space", "k", "win+", "foo+space", "win+nokey", "ctrl+f99", "ctrl+f" + "x"} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) should fail", spec)
		}
	}
}

const (
	vkLWin, vkRWin, vkLCtrl, vkLShift, vkSpace = 0x5B, 0x5C, 0xA2, 0xA0, 0x20
)

func TestMatcherWinSpace(t *testing.T) {
	m := newMatcher(Combo{Win: true, Key: vkSpace})
	if s, f := m.event(vkLWin, true); s || f {
		t.Fatal("the Win key itself must pass through")
	}
	if s, f := m.event(vkSpace, true); !s || !f {
		t.Fatal("Win+Space should be swallowed and fire")
	}
	if s, f := m.event(vkSpace, true); !s || f {
		t.Fatal("auto-repeat should be swallowed without firing again")
	}
	if s, f := m.event(vkSpace, false); !s || f {
		t.Fatal("the matching key-up must be swallowed")
	}
	if s, _ := m.event(vkLWin, false); s {
		t.Fatal("Win release must pass through")
	}
	// Plain space afterwards is untouched.
	if s, f := m.event(vkSpace, true); s || f {
		t.Fatal("space without Win must pass through")
	}
}

func TestMatcherRequiresExactModifiers(t *testing.T) {
	m := newMatcher(Combo{Win: true, Key: vkSpace})
	m.event(vkRWin, true)
	m.event(vkLShift, true)
	if s, f := m.event(vkSpace, true); s || f {
		t.Fatal("Win+Shift+Space must not trigger Win+Space")
	}
	m2 := newMatcher(Combo{Ctrl: true, Key: 'K'})
	m2.event(vkLCtrl, true)
	if _, f := m2.event('K', true); !f {
		t.Fatal("Ctrl+K should fire")
	}
}
