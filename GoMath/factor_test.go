package main

import "testing"

func TestFactorResults(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"16x + 40", "8(2x + 5)"},
		{"x^2 - 64", "(x - 8)(x + 8)"},
		{"2x^3 - 32x", "2x(x - 4)(x + 4)"},
		{"4x^4 - 44x^3 + 20x^2", "4x^2(x^2 - 11x + 5)"},
		{"x^3 - 5x^2 + 8x - 40", "(x - 5)(x^2 + 8)"},
		{"2x^3 - 5x^2 - 12x + 30", "(2x - 5)(x^2 - 6)"},
		{"64x^3 - 125", "(4x - 5)(16x^2 + 20x + 25)"},
		{"7x^2 - 20x - 3", "(x - 3)(7x + 1)"},
		{"x^2 - 6x + 9", "(x - 3)^2"},
		{"-x^2 + 1", "-(x - 1)(x + 1)"},
		{"y^2 + 18y + 81", "(y + 9)^2"},
	}
	for _, c := range cases {
		rep := Solve(c.input, OpFactor)
		if rep.Err != nil {
			t.Errorf("Factor(%q) returned error: %v", c.input, rep.Err)
			continue
		}
		if rep.Result != c.want {
			t.Errorf("Factor(%q) = %q, want %q", c.input, rep.Result, c.want)
		}
	}
}

func TestFactorIrreducible(t *testing.T) {
	for _, input := range []string{"x^2 + 4", "x^2 - 2x - 2"} {
		rep := Solve(input, OpPrime)
		if rep.Err != nil || rep.Result != "true" {
			t.Errorf("prime check of %q = %q, %v; want \"true\"", input, rep.Result, rep.Err)
		}
	}
	rep := Solve("x^2 - 9", OpPrime)
	if rep.Result != "false" {
		t.Errorf("prime check of x^2 - 9 = %q, want \"false\"", rep.Result)
	}
}

func TestFactorRejectsMixedVariables(t *testing.T) {
	if rep := Solve("x^2 + y", OpFactor); rep.Err == nil {
		t.Error("factoring x^2 + y returned no error")
	}
}

func TestTechnique(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"64x^3 - 125", "difference of cubes"},
		{"4x^2 - 81", "difference of two squares"},
		{"y^2 + 18y + 81", "perfect square trinomial"},
		{"36x^2 - 12x + 1", "perfect square trinomial"},
		{"x^2 + 14x + 33", "trinomial"},
		{"16x + 40", "greatest common factor"},
		{"x^3 - 5x^2 + 8x - 40", "factoring by grouping"},
	}
	for _, c := range cases {
		rep := Solve(c.input, OpTechnique)
		if rep.Err != nil {
			t.Errorf("Technique(%q) returned error: %v", c.input, rep.Err)
			continue
		}
		if rep.Result != c.want {
			t.Errorf("Technique(%q) = %q, want %q", c.input, rep.Result, c.want)
		}
	}
}
