package main

import (
	"strings"
	"testing"
)

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
		{"1 / (x + 1) + x ^ y", OpSimplify},
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

func TestSolveEquation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"7x - 8 = 55", "{9}"},
		{"2x + 1 = 4", "{3/2}"},
		{"3x = 3x + 1", "no solution"},
		{"x + 1 = 1 + x", "all real numbers"},
		{"x^2 - 5x + 6 = 0", "{2, 3}"},
		{"x^2 = 9", "{-3, 3}"},
		{"x^2 + 2x + 1 = 0", "{-1}"},
		{"x^2 + 1 = 0", "no real solution"},
		{"6x^2 - 5x = -1", "{1/3, 1/2}"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpSolve)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
	for _, in := range []string{"x^2 = 2", "x^3 = 8", "x + y = 1", "x + 1", "x = 1 = 2"} {
		if rep := Solve(in, OpSolve); rep.Err == nil {
			t.Errorf("%q: expected an error, got %q", in, rep.Result)
		}
	}
}

func TestSolveRadicalEquation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sqrt(x + 4) = 8", "{60}"},
		{`\sqrt{x + 4} = 8`, "{60}"},
		{"sqrt(x + 4) = -8", "no solution"},
		{"sqrt(2x + 3) = x", "{3}"},
		{"sqrt(19x - 52) = x + 2", "{7, 8}"},
		{"x = sqrt(x + 2)", "{2}"},
		{"sqrt(x) + 1 = 4", "{9}"},
		{"sqrt[3](x - 1) = 2", "{9}"},
		{"2sqrt(x) = 6", "{9}"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpSolve)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
	for _, in := range []string{"sqrt(x) = sqrt(x + 1)", "sqrt(x) * sqrt(x) = 1"} {
		if rep := Solve(in, OpSolve); rep.Err == nil {
			t.Errorf("%q: expected an error, got %q", in, rep.Result)
		}
	}
}

func TestSolveRationalExponentEquation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"x^(5/2) = 243", "{9}"},
		{`x^{\frac{5}{2}} = 243`, "{9}"},
		{"243 = x^(5/2)", "{9}"},
		{"x^(3/2) = -8", "no solution"},
		{"x^(2/3) = 4", "{-8, 8}"},
		{"x^(1/3) = -2", "{-8}"},
		{"x^(3/2) = 0", "{0}"},
		{"x^(4/2) = 9", "{-3, 3}"},
	}
	for _, c := range cases {
		rep := Solve(c.in, OpSolve)
		if rep.Err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, rep.Err)
		}
		if rep.Result != c.want {
			t.Errorf("%q: Result = %q, want %q", c.in, rep.Result, c.want)
		}
	}
	if rep := Solve("x^(2/3) = 5", OpSolve); rep.Err == nil {
		t.Errorf("x^(2/3) = 5: expected an error, got %q", rep.Result)
	}
}

func TestSolveEvaluateDividesBySum(t *testing.T) {
	rep := SolveAt("3(x + 3)/(6x - 18)", OpEvaluate, map[string]float64{"x": 14})
	if rep.Err != nil || rep.Result != "17/22" {
		t.Fatalf("got %q, %v; want 17/22", rep.Result, rep.Err)
	}
}

func TestSolveDomain(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"4/(x + 6)", "{-6}"},
		{"1/(x^2 - 5x + 6)", "{2, 3}"},
		{"1/(x^2 + 4)", "all real numbers"},
		{"x/3", "all real numbers"},
		{"1/(2x - 1)", "{1/2}"},
		{"1/x + 1/(x - 1)", "{0, 1}"},
		{"(2x^2 - 14x)/(x^2 - 4x - 21) / ((x^2 - 5x)/(x + 3))", "{-3, 0, 5, 7}"},
	} {
		rep := Solve(c.input, OpDomain)
		if rep.Err != nil || rep.Result != c.want {
			t.Errorf("Solve(%q, OpDomain) = %q, %v; want %q", c.input, rep.Result, rep.Err, c.want)
		}
	}
}

func TestSimplifyRational(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"(3x - 6)/(x^2 - 4x + 4)", "3/(x - 2)"},
		{"(y + 2)/(y^2 - 4)", "1/(y - 2)"},
		{"(x^2 - 1)/(x - 1)", "x + 1"},
		{"(x + 1)/(x^2 + 3x + 2)", "1/(x + 2)"},
		{"(2x + 4)/(4x + 8)", "1/2"},
		{"(x - 3)/(x + 5)", "(x - 3)/(x + 5)"},
		{"(x^2 - 9)/(x^2 + 6x + 9)", "(x - 3)/(x + 3)"},
		{"(5x - 5)/x * 3x^2/(7x - 7)", "15x/7"},
		{"(2x^2 - 14x)/(x^2 - 4x - 21) / ((x^2 - 5x)/(x + 3))", "2/(x - 5)"},
		{"(x + 1)/(x - 2) / ((x + 1)/(x + 3))", "(x + 3)/(x - 2)"},
		{"((x - 1)/(x + 1))^2", "(x - 1)^2/(x + 1)^2"},
	} {
		rep := Solve(c.input, OpSimplify)
		if rep.Err != nil || rep.Result != c.want {
			t.Errorf("Solve(%q) = %q, %v; want %q", c.input, rep.Result, rep.Err, c.want)
		}
	}
}

func TestSolveAddsFractions(t *testing.T) {
	rep := Solve("1/(x + 1) + 1", OpSimplify)
	if rep.Err != nil || rep.Result != "(x + 2)/(x + 1)" {
		t.Errorf("got %q, %v", rep.Result, rep.Err)
	}
}

func TestSolveClearsFractions(t *testing.T) {
	for input, want := range map[string]string{"x/2 = 3": "{6}", "x/2 = x/8 + 7": "{56/3}"} {
		if rep := Solve(input, OpSolve); rep.Err != nil || rep.Result != want {
			t.Errorf("Solve(%q) = %q, %v, want %q", input, rep.Result, rep.Err, want)
		}
	}
}

func TestLinearUnitOperations(t *testing.T) {
	cases := []struct {
		in   string
		op   Operation
		want string
	}{
		{"x + 1 > x", OpInequality, "all real numbers"},
		{"x + 1 < x", OpInequality, "no solution"},
		{"x > 2 > 5", OpInequality, "no solution"},
		{"2 <= x <= 2", OpInequality, "{2}"},
		{"x/2 < 3", OpInequality, "(-∞, 6)"},
		{"(2,1) and (2,5)", OpSlope, "undefined (vertical)"},
		{"(1,4) and (3,0)", OpSlope, "-2 (falls)"},
		{"(0,0) and (3,2)", OpSlope, "2/3 (rises)"},
		{"slope -2/3 through (3,-1)", OpPointSlope, "y + 1 = (-2/3)(x - 3)"},
		{"slope -2/3 through (3,-1)", OpSlopeIntercept, "y = (-2/3)x + 1"},
		{"(2,1) and (2,5)", OpSlopeIntercept, "x = 2"},
		{"P = 2l + 2w for w", OpSolveFor, "w = (P - 2l)/2"},
		{"d = rt for t", OpSolveFor, "t = d/r"},
	}
	for _, c := range cases {
		rep := Solve(c.in, c.op)
		if rep.Err != nil || rep.Result != c.want {
			t.Errorf("Solve(%q, %s) = %q, %v; want %q", c.in, c.op, rep.Result, rep.Err, c.want)
		}
	}
	for _, in := range []string{"x^2 > 4", "x >", "3 = 3"} {
		if rep := Solve(in, OpInequality); rep.Err == nil {
			t.Errorf("Solve(%q, OpInequality) returned no error", in)
		}
	}
}

func TestGraph(t *testing.T) {
	rep := Solve("y = x - 2", OpGraph)
	if rep.Err != nil || !strings.HasPrefix(rep.Result, "(-3, -5)") || !strings.HasSuffix(rep.Result, "(3, 1)") {
		t.Fatalf("got %q, %v", rep.Result, rep.Err)
	}
	plot := rep.Steps[len(rep.Steps)-2].Detail
	for _, want := range []string{"  1 ", "-5 ", "+", "*"} {
		if !strings.Contains(plot, want) {
			t.Errorf("plot lacks %q:\n%s", want, plot)
		}
	}
	if got := strings.Count(plot, "*"); got != 7 {
		t.Errorf("plot has %d points, want 7:\n%s", got, plot)
	}
	if rep := Solve("y = x^2; x = -100, 100", OpGraph); rep.Err == nil {
		t.Error("a 10000-unit graph returned no error")
	}
}
