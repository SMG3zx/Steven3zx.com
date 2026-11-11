package main

import "testing"

func TestPolyStandardForm(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"(x + 4)(x^2 - 4x + 16)", "x^3 + 64"},
		{"(3 - 8x)^2", "64x^2 - 48x + 9"},
		{"-x^2 + 1", "-x^2 + 1"},
		{"(7x^2y + 6xy - 1) + (-4x^2y + 2xy - 10)", "3x^2y + 8xy - 11"},
		{"(4x + 3y)(4x - 3y)", "16x^2 - 9y^2"},
		{"x - x", "0"},
		{"x^-6 * y", "y/x^6"},
		{"x^-1 + 2", "(2x + 1)/x"},
		{"y / x^6", "y/x^6"},
		{"x * x^-1", "1"},
		{"(x^2)^-1", "1/x^2"},
	}
	for _, c := range cases {
		p, err := ToPoly(expr(c.input))
		if err != nil {
			t.Errorf("ToPoly(%q) returned error: %v", c.input, err)
			continue
		}
		if got := p.String(); got != c.want {
			t.Errorf("ToPoly(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestPolyDegree(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"(7x^2y + 6xy - 1) + (-4x^2y + 2xy - 10)", 3},
		{"(7x^4y^2 + 10x^3y - 7y) - (4x^4y^2 + 5x^3y + 4y + 4x)", 6},
		{"(7x^2 - 6x - 5) + (3x^2 - 5x + 7) - (x^2 - 2x - 1)", 2},
	}
	for _, c := range cases {
		p, err := ToPoly(expr(c.input))
		if err != nil {
			t.Fatalf("ToPoly(%q) returned error: %v", c.input, err)
		}
		if got := p.Degree(); got != c.want {
			t.Errorf("Degree(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestToPolyRejectsNonPolynomials(t *testing.T) {
	for _, input := range []string{"1 / (x + 1)", "x / 0", "x ^ y", "1.5x"} {
		if _, err := ToPoly(expr(input)); err == nil {
			t.Errorf("ToPoly(%q) returned no error", input)
		}
	}
}

func TestPolyFractionalCoefficients(t *testing.T) {
	cases := []struct{ input, want string }{
		{"x / 2", "x/2"},
		{"x^2/3 - (x - 4)/3", "(x^2 - x + 4)/3"},
		{"x/2 + x/2", "x"},
		{"x/2 + x/3", "5x/6"},
		{"1/2 + 1/3", "5/6"},
		{"x / (2x)", "1/2"},
		{"1 / (2x)", "1/(2x)"},
		{"x/2 - 1/3", "(3x - 2)/6"},
	}
	for _, c := range cases {
		p, err := ToPoly(expr(c.input))
		if err != nil {
			t.Fatalf("ToPoly(%q) returned error: %v", c.input, err)
		}
		if got := p.String(); got != c.want {
			t.Errorf("ToPoly(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFractionalCoefficientsRejectedWhereWholeNeeded(t *testing.T) {
	for _, c := range []struct {
		input string
		op    Operation
	}{{"x/2 + 1", OpFactor}} {
		if rep := Solve(c.input, c.op); rep.Err == nil {
			t.Errorf("Solve(%q, %v) returned %q, want an error", c.input, c.op, rep.Result)
		}
	}
}
