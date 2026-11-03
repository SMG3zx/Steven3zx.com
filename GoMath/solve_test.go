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
		{"sqrt[3](343)", "7"},
		{`\sqrt[3]{343}`, "7"},
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

func TestSolveSimplifyQuotientRule(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt(1/144)", "1/12"},
		{`\sqrt{\frac{1}{144}}`, "1/12"},
		{"sqrt(20/9)", "2sqrt(5)/3"},
		{"sqrt(25/100)", "1/2"},
		{"sqrt(8/50)", "2/5"},
		{"sqrt(9/4)", "3/2"},
		{"sqrt(81/4)", "9/2"},
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
	if rep := Solve("sqrt(1/5)", OpSimplify); rep.Err == nil {
		t.Errorf("sqrt(1/5): expected an error, got %q", rep.Result)
	}
}

func TestSolveSimplifyQuotientOfRadicals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt(147x^3) / sqrt(3x)", "7x"},
		{`\frac{\sqrt{147x^3}}{\sqrt{3x}}`, "7x"},
		{"sqrt(20) / sqrt(5)", "2"},
		{"sqrt(432x^8) / sqrt(6x)", "6x^3sqrt(2x)"},
		{"sqrt(18x) / sqrt(x)", "3sqrt(2)"},
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

func TestSolveSimplifyHigherRoots(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt[3](72)", "2sqrt[3](9)"},
		{`\sqrt[3]{72}`, "2sqrt[3](9)"},
		{"sqrt[3](343)", "7"},
		{"sqrt[3](16x^4)", "2xsqrt[3](2x)"},
		{"sqrt[4](48)", "2sqrt[4](3)"},
		{"sqrt[3](54) + sqrt[3](2)", "4sqrt[3](2)"},
		{"sqrt[3](49) * sqrt[3](14)", "7sqrt[3](2)"},
		{`\sqrt[3]{49} \cdot \sqrt[3]{14}`, "7sqrt[3](2)"},
		{"sqrt[3](54) / sqrt[3](2)", "3"},
		{"sqrt(2) * sqrt[3](4)", "sqrt[3](4)sqrt(2)"},
		{"sqrt[3](2) + sqrt(2)", "sqrt(2) + sqrt[3](2)"},
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

func TestSolveRadicalForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"169^(1/2)", "sqrt(169)"},
		{`169^{\frac{1}{2}}`, "sqrt(169)"},
		{"8^(1/3)", "sqrt[3](8)"},
		{"8^(2/3)", "sqrt[3](8^2)"},
		{"(x+1)^(2/5)", "sqrt[5]((x+1)^2)"},
		{"x^(3/4)", "sqrt[4](x^3)"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpRadicalForm)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
	for _, in := range []string{"169", "x^(1/1)", "x^(-1/2)"} {
		if rep := Solve(in, OpRadicalForm); rep.Err == nil {
			t.Errorf("%q: expected an error, got %q", in, rep.Result)
		}
	}
}

func TestSolveEvaluateFractionalExponents(t *testing.T) {
	cases := []struct{ in, want string }{
		{"169^(1/2)", "13"},
		{"27^(1/3)", "3"},
		{"8^(2/3)", "4"},
		{"21^(1/2) * 21^(1/2)", "21"},
		{"21^(1/2) • 21^(1/2)", "21"},
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

func TestSolveSimplifyPowerOfFractionalPower(t *testing.T) {
	cases := []struct{ in, want string }{
		{"(x^(6/7))^7", "x^6"},
		{`(x^{6/7})^{7}`, "x^6"},
		{"(x^(1/2))^2", "x"},
		{"(x^(2/3))^3", "x^2"},
		{"(x^(2/3))^(1/2)", "x^(1/3)"},
		{"(x^(1/2))^-2", "1/x"},
		{"(x^(1/2))^-4", "1/x^2"},
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
