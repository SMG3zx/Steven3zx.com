package main

import (
	"math"
	"testing"
)

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

func TestPowerIsRightAssociative(t *testing.T) {
	if got, want := expr("2 ^ 3 ^ 2").String(), "(^ 2 (^ 3 2))"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLettersAreSeparateVariables(t *testing.T) {
	if got, want := expr("12 + xy").String(), "(+ 12 (* x y))"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestImplicitMultiplication(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"2x", "(* 2 x)"},
		{"7x^2y", "(* (* 7 (^ x 2)) y)"},
		{"2(x + 1)", "(* 2 (+ x 1))"},
		{"(x + 4)(x - 4)", "(* (+ x 4) (- x 4))"},
	}
	for _, c := range cases {
		if got := expr(c.input).String(); got != c.want {
			t.Errorf("expr(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestUnaryMinus(t *testing.T) {
	if got, want := expr("-x^2").String(), "(- (^ x 2))"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	cases := []struct {
		input string
		env   map[string]float64
		want  float64
	}{
		{"-x^2", map[string]float64{"x": 3}, -9},
		{"-4x", map[string]float64{"x": 2}, -8},
		{"-x + 1", map[string]float64{"x": 2}, -1},
	}
	for _, c := range cases {
		got, err := expr(c.input).Eval(c.env)
		if err != nil || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Eval(%q) = %v, %v; want %v", c.input, got, err, c.want)
		}
	}
}

func TestParseWithTrace(t *testing.T) {
	tree, lines, err := ParseWithTrace("1 + 2 * 3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := tree.String(), "(+ 1 (* 2 3))"; got != want {
		t.Fatalf("tree = %q, want %q", got, want)
	}
	for _, want := range []string{"build (* 2 3)", "build (+ 1 (* 2 3))"} {
		found := false
		for _, line := range lines {
			if line == want {
				found = true
			}
		}
		if !found {
			t.Errorf("trace is missing %q; got %q", want, lines)
		}
	}
}

func TestParseWithTraceReportsErrors(t *testing.T) {
	if _, _, err := ParseWithTrace("x +"); err == nil {
		t.Error("ParseWithTrace(\"x +\") returned no error")
	}
}

func TestParentheses(t *testing.T) {
	if got, want := expr("(1 + 2) * 3").String(), "(* (+ 1 2) 3)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnbalancedParenthesesPanic(t *testing.T) {
	for _, input := range []string{"(1 + 2", "1 + 2)"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("expr(%q) did not panic", input)
				}
			}()
			expr(input)
		}()
	}
}

func TestEval(t *testing.T) {
	env := map[string]float64{"x": 2, "y": 3}
	cases := []struct {
		input string
		want  float64
	}{
		{"x", 2},
		{"12 - y", 9},
		{"x + 2 * y", 8},
		{"(x + 2) * y", 12},
		{"x ^ 2 ^ 2", 16},
		{"1 / x", 0.5},
	}
	for _, c := range cases {
		got, err := expr(c.input).Eval(env)
		if err != nil {
			t.Errorf("Eval(%q) returned error: %v", c.input, err)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Eval(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	env := map[string]float64{"x": 2}
	for _, input := range []string{"z + 1", "1 / (x - 2)"} {
		if _, err := expr(input).Eval(env); err == nil {
			t.Errorf("Eval(%q) returned no error", input)
		}
	}
}

func TestDiff(t *testing.T) {
	cases := []struct {
		input string
		wrt   string
		env   map[string]float64
		want  float64
	}{
		{"x ^ 2", "x", map[string]float64{"x": 3}, 6},
		{"x * y", "x", map[string]float64{"x": 2, "y": 3}, 3},
		{"x * y", "y", map[string]float64{"x": 2, "y": 3}, 2},
		{"x * x", "x", map[string]float64{"x": 3}, 6},
		{"3 * x ^ 3 + x", "x", map[string]float64{"x": 2}, 37},
		{"x - y", "x", map[string]float64{"x": 2, "y": 3}, 1},
		{"1 / x", "x", map[string]float64{"x": 2}, -0.25},
		{"y", "x", map[string]float64{"x": 2, "y": 3}, 0},
	}
	for _, c := range cases {
		got, err := expr(c.input).Diff(c.wrt).Eval(c.env)
		if err != nil {
			t.Errorf("d/d%s %q returned error: %v", c.wrt, c.input, err)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("d/d%s %q = %v, want %v", c.wrt, c.input, got, c.want)
		}
	}
}

func TestDiffNonConstantExponentPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Diff of x ^ y with respect to x did not panic")
		}
	}()
	expr("x ^ y").Diff("x")
}
