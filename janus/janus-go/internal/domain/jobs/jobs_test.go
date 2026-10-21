package jobs

import "testing"

func TestCanMove(t *testing.T) {
	cases := []struct {
		name     string
		machine  Machine
		from, to string
		want     bool
	}{
		{"import queued to cloning", Import, "queued", "cloning", true},
		{"import skips scanning on resume", Import, "cloning", "storing", true},
		{"import progress in place", Import, "scanning", "scanning", true},
		{"import terminal stays put", Import, "complete", "complete", false},
		{"import terminal cannot restart", Import, "failed", "queued", false},
		{"import cannot skip to complete", Import, "queued", "complete", false},
		{"wasm queued to building", WASM, "queued", "building", true},
		{"wasm building back to queued", WASM, "building", "queued", true},
		{"wasm cannot scan", WASM, "queued", "scanning", false},
		{"wasm cancelled is terminal", WASM, "cancelled", "queued", false},
		{"unknown status", WASM, "nonsense", "queued", false},
	}
	for _, c := range cases {
		if got := c.machine.CanMove(c.from, c.to); got != c.want {
			t.Errorf("%s: CanMove(%q, %q) = %v, want %v", c.name, c.from, c.to, got, c.want)
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, status := range []string{"complete", "failed", "cancelled"} {
		if !Import.Terminal(status) || !WASM.Terminal(status) {
			t.Errorf("%s is not terminal", status)
		}
	}
	for _, status := range []string{"queued", "cloning", "scanning", "storing"} {
		if Import.Terminal(status) {
			t.Errorf("import status %s is terminal", status)
		}
	}
	if WASM.Terminal("building") {
		t.Error("building is terminal")
	}
}
