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
