package main

import "testing"

func TestNormalizeTeX(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`x - 1`, `x - 1`},
		{`x − 1`, `x - 1`},
		{`x \cdot y`, `x * y`},
		{`16x \times 2`, `16x * 2`},
		{`\left(x+1\right)^{2}`, `(x+1)^(2)`},
		{`4x^{2}-7x`, `4x^(2)-7x`},
		{`\frac{1}{x}`, `(1)/(x)`},
		{`\frac{x^{2}}{2}`, `(x^(2))/(2)`},
	}
	for _, c := range cases {
		got, _ := normalizeTeX(c.input)
		if got != c.want {
			t.Errorf("normalizeTeX(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestNormalizeTeXExplainsRewrites(t *testing.T) {
	_, notes := normalizeTeX(`\left(x \cdot 2\right)^{2}`)
	if len(notes) == 0 {
		t.Fatal("expected at least one rewrite note")
	}
}
