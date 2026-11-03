package main

import "testing"

func TestSolveSimplifyReportsResultAndDegree(t *testing.T) {
	rep := Solve("(x + 4)(x^2 - 4x + 16)", OpSimplify)
	if rep.Err != nil {
		t.Fatalf("unexpected error: %v", rep.Err)
	}
	if rep.Result != "x^3 + 64" {
		t.Errorf("Result = %q, want %q", rep.Result, "x^3 + 64")
	}
	if rep.Degree != 3 {
		t.Errorf("Degree = %d, want 3", rep.Degree)
	}
}

func TestSolveStepsWalkThroughEachPhase(t *testing.T) {
	rep := Solve(`\left(x+1\right)^{2}`, OpSimplify)
	if rep.Err != nil {
		t.Fatalf("unexpected error: %v", rep.Err)
	}
	phases := map[string]bool{}
	for _, s := range rep.Steps {
		phases[s.Phase] = true
	}
	for _, want := range []string{"Input", "Tokens", "Parse", "Compute", "Result"} {
		if !phases[want] {
			t.Errorf("no step in phase %q; steps: %+v", want, rep.Steps)
		}
	}
}

func TestSolveStepsShowTeXRewrite(t *testing.T) {
	rep := Solve(`\left(x+1\right)^{2}`, OpSimplify)
	found := false
	for _, s := range rep.Steps {
		if s.Phase == "Input" && s.Detail != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected an Input step describing the TeX rewrite")
	}
}

func TestSolveReportsErrors(t *testing.T) {
	cases := []struct {
		input string
		op    Operation
	}{
		{"x +", OpSimplify},
		{"x ^ y", OpSimplify},
		{"1 / (x + 1)", OpSimplify},
	}
	for _, c := range cases {
		if rep := Solve(c.input, c.op); rep.Err == nil {
			t.Errorf("Solve(%q, %v) returned no error", c.input, c.op)
		}
	}
}

func TestSolveRootBlank(t *testing.T) {
	for _, in := range []string{"sqrt(169)", `\sqrt{169}`, "sqrt(13^2)"} {
		rep := Solve(in, OpRoot)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", in, rep.Err)
		}
		if rep.Result != "13^2" {
			t.Errorf("%q: Result = %q, want %q", in, rep.Result, "13^2")
		}
	}
}

func TestSolveRootBlankRejectsNonSquares(t *testing.T) {
	for _, in := range []string{"sqrt(x)", "sqrt(170)", "sqrt(-4)", "169"} {
		if rep := Solve(in, OpRoot); rep.Err == nil {
			t.Errorf("%q: expected an error, got result %q", in, rep.Result)
		}
	}
}

func TestSolveSimplifyCombinesLikeRadicals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"7sqrt(2) + 11sqrt(2)", "18sqrt(2)"},
		{`7\sqrt{2} + 11\sqrt{2}`, "18sqrt(2)"},
		{"sqrt(8) + sqrt(2)", "3sqrt(2)"},
		{"sqrt(9) + 1", "4"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpSimplify)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
}

func TestSolveRootBlankHigherIndex(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt[5](-3125)", "(-5)^5"},
		{`\sqrt[5]{-3125}`, "(-5)^5"},
		{"sqrt[3](27)", "3^3"},
		{"sqrt[4](16)", "2^4"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpRoot)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
	for _, in := range []string{"sqrt[4](-16)", "sqrt[3](10)", "sqrt[1](4)"} {
		if rep := Solve(in, OpRoot); rep.Err == nil {
			t.Errorf("%q: expected an error, got %q", in, rep.Result)
		}
	}
}

func TestSolveEvaluateRoots(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt(100 - 36)", "8"},
		{`\sqrt{100 - 36}`, "8"},
		{"sqrt(36 - 100)", "not a real number"},
		{"sqrt[3](27 - 35)", "-2"},
		{"2 + sqrt(sqrt(16))", "4"},
		{"sqrt(2)", "1.4142135623730951"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpEvaluate)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
}

func TestSolveSimplifyProductRule(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt(6x) * sqrt(30x)", "6xsqrt(5)"},
		{`\sqrt{6x} \cdot \sqrt{30x}`, "6xsqrt(5)"},
		{"sqrt(6x) • sqrt(30x)", "6xsqrt(5)"},
		{"sqrt(2)sqrt(8)", "4"},
		{"sqrt(x^3)", "xsqrt(x)"},
		{"sqrt(8x^2) * sqrt(24x)", "8xsqrt(3x)"},
		{"sqrt(x^21)", "x^10sqrt(x)"},
		{"sqrt(12y^2)", "2ysqrt(3)"},
		{"sqrt(2) * sqrt(3) * sqrt(5)", "sqrt(30)"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpSimplify)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
	if rep := Solve("sqrt(x + 1)", OpSimplify); rep.Err == nil {
		t.Errorf("sqrt(x + 1): expected an error, got %q", rep.Result)
	}
}
