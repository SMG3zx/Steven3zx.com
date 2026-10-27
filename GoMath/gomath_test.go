package main

import "testing"

func TestExpr(t *testing.T) {
	s := expr("1 + 2 * 3")
	if got, want := s.String(), "(+ 1 (* 2 3))"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExprCases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"1", "1"},
		{"1 + 2 * 3", "(+ 1 (* 2 3))"},
		{"a + b * c * d + e", "(+ (+ a (* (* b c) d)) e)"},
	}
	for _, c := range cases {
		if got := expr(c.input).String(); got != c.want {
			t.Errorf("expr(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
