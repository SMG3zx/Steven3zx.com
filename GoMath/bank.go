package main

import "fmt"

// WorksheetEntry is one problem from the worksheet screenshots in equations/
// or the question bank screenshots.
// Expected is the answer the worksheet gives, written in ASCII.
type WorksheetEntry struct {
	Source   string
	Input    string
	Op       Operation
	Expected string
	// Degree is the degree the worksheet asks for, or 0 when it does not ask.
	Degree int
	// Vars gives the value of each variable for OpEvaluate, or nil otherwise.
	Vars map[string]float64
}

// Worksheet holds one entry per screenshot, in file order.
var Worksheet = []WorksheetEntry{
	{"image.png", "(-4x^3 + 4x^2 - 7x + 7) + (7x^3 + 4x^2 - 3x - 7)", OpSimplify, "3x^3 + 8x^2 - 10x", 3, nil},
	{"image_1.png", "(2x^3 - 9x^2 + 8x - 5) - (5x^3 - 10x^2 - 5x + 6)", OpSimplify, "-3x^3 + x^2 + 13x - 11", 3, nil},
	{"image_2.png", "(7x^2 - 6x - 5) + (3x^2 - 5x + 7) - (x^2 - 2x - 1)", OpSimplify, "9x^2 - 9x + 3", 2, nil},
	{"image_3.png", "(x + 4)(x^2 - 4x + 16)", OpSimplify, "x^3 + 64", 0, nil},
	{"image_4.png", "(x + 6)(x + 5)", OpSimplify, "x^2 + 11x + 30", 0, nil},
	{"image_5.png", "(x - 14)(x + 8)", OpSimplify, "x^2 - 6x - 112", 0, nil},
	{"image_6.png", "(4x - 7)(3x + 8)", OpSimplify, "12x^2 + 11x - 56", 0, nil},
	{"image_7.png", "(x - 1)(x + 1)", OpSimplify, "x^2 - 1", 0, nil},
	{"image_8.png", "(4x + 3)(4x - 3)", OpSimplify, "16x^2 - 9", 0, nil},
	{"image_9.png", "(x + 11)^2", OpSimplify, "x^2 + 22x + 121", 0, nil},
	{"image_10.png", "(x - 5)^2", OpSimplify, "x^2 - 10x + 25", 0, nil},
	{"image_11.png", "(3 - 8x)^2", OpSimplify, "64x^2 - 48x + 9", 0, nil},
	{"image_12.png", "(7x^2y + 6xy - 1) + (-4x^2y + 2xy - 10)", OpSimplify, "3x^2y + 8xy - 11", 3, nil},
	{"image_13.png", "(7x^4y^2 + 10x^3y - 7y) - (4x^4y^2 + 5x^3y + 4y + 4x)", OpSimplify, "3x^4y^2 + 5x^3y - 11y - 4x", 6, nil},
	{"image_14.png", "(4x + 3y)(4x - 3y)", OpSimplify, "16x^2 - 9y^2", 0, nil},
	{"image_15.png", "64x^3 - 125", OpTechnique, "difference of cubes", 0, nil},
	{"image_16.png", "16x + 40", OpFactor, "8(2x + 5)", 0, nil},
	{"image_17.png", "4x^4 - 44x^3 + 20x^2", OpFactor, "4x^2(x^2 - 11x + 5)", 0, nil},
	{"image_18.png", "x(x + 3) - 5(x + 3)", OpFactor, "(x + 3)(x - 5)", 0, nil},
	{"image_19.png", "x^2(x - 8) + 12(x - 8)", OpFactor, "(x - 8)(x^2 + 12)", 0, nil},
	{"image_20.png", "x^3 - 5x^2 + 8x - 40", OpFactor, "(x - 5)(x^2 + 8)", 0, nil},
	{"image_21.png", "2x^3 - 5x^2 - 12x + 30", OpFactor, "(2x - 5)(x^2 - 6)", 0, nil},
	{"image_22.png", "x^2 + 14x + 33", OpFactor, "(x + 3)(x + 11)", 0, nil},
	{"image_23.png", "x^2 - 12x - 45", OpFactor, "(x - 15)(x + 3)", 0, nil},
	{"image_24.png", "x^2 - 17x + 72", OpFactor, "(x - 8)(x - 9)", 0, nil},
	{"image_25.png", "7x^2 - 20x - 3", OpFactor, "(7x + 1)(x - 3)", 0, nil},
	{"image_26.png", "6x^2 - 23x + 21", OpFactor, "(3x - 7)(2x - 3)", 0, nil},
	{"image_27.png", "x^2 - 64", OpFactor, "(x - 8)(x + 8)", 0, nil},
	{"image_28.png", "4x^2 - 81", OpFactor, "(2x - 9)(2x + 9)", 0, nil},
	{"image_29.png", "y^2 + 18y + 81", OpFactor, "(y + 9)^2", 0, nil},
	{"image_30.png", "x^2 - 6x + 9", OpFactor, "(x - 3)^2", 0, nil},
	{"image_31.png", "36x^2 - 12x + 1", OpFactor, "(6x - 1)^2", 0, nil},
	{"image_32.png", "2x^3 - 32x", OpFactor, "2x(x - 4)(x + 4)", 0, nil},
	{"image_33.png", "x^2 + 4", OpPrime, "true", 0, nil},
	{"image_34.png", "x^2 - 2x - 2", OpPrime, "true", 0, nil},
	{"evaluate_1.png", "8 + 7x", OpEvaluate, "57", 0, map[string]float64{"x": 7}},
	{"evaluate_2.png", "x^2 - 2x + 3", OpEvaluate, "27", 0, map[string]float64{"x": 6}},
	{"simplify_1.png", "7(5y - 6) + 4(3y + 5)", OpSimplify, "47y - 22", 1, nil},
	{"simplify_2.png", "-(9x - 2y - 5)", OpSimplify, "-9x + 2y + 5", 1, nil},
	{"evaluate_3.png", "7^2 * 4", OpEvaluate, "196", 0, nil},
	{"evaluate_4.png", "7^2 * 7^3", OpEvaluate, "16807", 0, nil},
	{"simplify_3.png", "x^-6 * y", OpSimplify, "y/x^6", 0, nil},
	{"simplify_4.png", "x^-4 * x^8", OpSimplify, "x^4", 0, nil},
	{"simplify_5.png", "x^2 - x^6 + x^8 - 3", OpSimplify, "x^8 - x^6 + x^2 - 3", 0, nil},
	{"simplify_6.png", "(7x^2 - 7x - 1) + (2x^2 - 3x + 5) - (x^2 - 2x - 1)", OpSimplify, "8x^2 - 8x + 5", 2, nil},
	{"simplify_7.png", "(x + 3)(x + 7)", OpSimplify, "x^2 + 10x + 21", 0, nil},
	{"simplify_8.png", "(x - 2)(x + 2)", OpSimplify, "x^2 - 4", 0, nil},
	{"simplify_9.png", "(7x + 4)(7x - 4)", OpSimplify, "49x^2 - 16", 0, nil},
	{"simplify_10.png", "(x - 3)^2", OpSimplify, "x^2 - 6x + 9", 0, nil},
	{"factor_1.png", "9x + 12", OpFactor, "3(3x + 4)", 0, nil},
	{"factor_2.png", "x^3 - 6x^2 + 6x - 36", OpFactor, "(x - 6)(x^2 + 6)", 0, nil},
	{"factor_3.png", "x^2 + 16x + 55", OpFactor, "(x + 5)(x + 11)", 0, nil},
	{"factor_4.png", "3x^2 - 32x - 11", OpFactor, "(3x + 1)(x - 11)", 0, nil},
	{"prime_1.png", "x^2 + 9", OpPrime, "true", 0, nil},
	{"radical_1.png", "7sqrt(2) + 11sqrt(2)", OpSimplify, "18sqrt(2)", 0, nil},
	{"evaluate_5.png", "sqrt(100 - 36)", OpEvaluate, "8", 0, nil},
	{"evaluate_6.png", "sqrt(169) + sqrt(64)", OpEvaluate, "21", 0, nil},
	{"radical_2.png", "sqrt(175)", OpSimplify, "5sqrt(7)", 0, nil},
	{"radical_3.png", "sqrt(6x) * sqrt(30x)", OpSimplify, "6xsqrt(5)", 0, nil},
	{"radical_4.png", "sqrt(x^21)", OpSimplify, "x^10sqrt(x)", 0, nil},
	{"radical_5.png", "sqrt(8x^2) * sqrt(24x)", OpSimplify, "8xsqrt(3x)", 0, nil},
	{"radical_6.png", "sqrt(1/144)", OpSimplify, "1/12", 0, nil},
	{"radical_7.png", "sqrt(81/4)", OpSimplify, "9/2", 0, nil},
	{"radical_8.png", "sqrt(147x^3) / sqrt(3x)", OpSimplify, "7x", 0, nil},
	{"radical_9.png", "sqrt(432x^8) / sqrt(6x)", OpSimplify, "6x^3sqrt(2x)", 0, nil},
	{"radical_10.png", "3sqrt(5) + 6sqrt(5)", OpSimplify, "9sqrt(5)", 0, nil},
	{"radical_11.png", "3sqrt(11x) - 8sqrt(11x)", OpSimplify, "-5sqrt(11x)", 0, nil},
	{"radical_12.png", "sqrt(72x) - sqrt(18x)", OpSimplify, "3sqrt(2x)", 0, nil},
	{"radical_13.png", "2sqrt(8) + 3sqrt(50)", OpSimplify, "19sqrt(2)", 0, nil},
	{"evaluate_7.png", "sqrt[3](343)", OpEvaluate, "7", 0, nil},
	{"radical_14.png", "sqrt[3](72)", OpSimplify, "2sqrt[3](9)", 0, nil},
	{"radical_15.png", "sqrt[3](49) * sqrt[3](14)", OpSimplify, "7sqrt[3](2)", 0, nil},
	{"radical_16.png", "169^(1/2)", OpRadicalForm, "sqrt(169)", 0, nil},
	{"evaluate_8.png", "169^(1/2)", OpEvaluate, "13", 0, nil},
	{"radical_17.png", "125^(2/3)", OpRadicalForm, "sqrt[3](125^2)", 0, nil},
	{"exponent_1.png", "(x^(6/7))^7", OpSimplify, "x^6", 0, nil},
	{"evaluate_9.png", "21^(1/2) * 21^(1/2)", OpEvaluate, "21", 0, nil},
	{"solve_1.png", "7x - 8 = 55", OpSolve, "{9}", 0, nil},
	{"solve_2.png", "(8x - 7) - 5 = 9(x + 7)", OpSolve, "{-75}", 0, nil},
	{"solve_3.png", "sqrt(x + 4) = 8", OpSolve, "{60}", 0, nil},
	{"solve_4.png", "sqrt(19x - 52) = x + 2", OpSolve, "{7, 8}", 0, nil},
	{"solve_5.png", "x^(5/2) = 243", OpSolve, "{9}", 0, nil},
	{"evaluate_10.png", "3(x + 3)/(6x - 18)", OpEvaluate, "17/22", 0, map[string]float64{"x": 14}},
	{"evaluate_11.png", "(5x + y)/(xy - 5x)", OpEvaluate, "2", 0, map[string]float64{"x": -2, "y": 6}},
	{"simplify_11.png", "x^2/3 - (x - 4)/3", OpSimplify, "(x^2 - x + 4)/3", 2, nil},
	{"domain_1.png", "4/(x + 6)", OpDomain, "{-6}", 0, nil},
	{"domain_2.png", "(y + 2)/(y^2 - 4)", OpDomain, "{-2, 2}", 0, nil},
	{"domain_3.png", "(x - 2)/(x^2 + 3x + 2)", OpDomain, "{-2, -1}", 0, nil},
	{"simplify_12.png", "(3x - 6)/(x^2 - 4x + 4)", OpSimplify, "3/(x - 2)", 0, nil},
	{"domain_4.png", "(3x - 6)/(x^2 - 4x + 4)", OpDomain, "{2}", 0, nil},
	{"simplify_13.png", "(x^2 - 8x + 16)/(3x - 12)", OpSimplify, "(x - 4)/3", 0, nil},
	{"domain_5.png", "(x^2 - 8x + 16)/(3x - 12)", OpDomain, "{4}", 0, nil},
	{"simplify_14.png", "(5x - 5)/x * 3x^2/(7x - 7)", OpSimplify, "15x/7", 0, nil},
	{"simplify_15.png", "(x^2 - 13x + 42)/(x^2 + x - 42) * (x^2 - 36)/(x^2 - x - 42)", OpSimplify, "(x - 6)/(x + 7)", 0, nil},
	{"simplify_16.png", "m/2 * m/10", OpSimplify, "m^2/20", 0, nil},
	{"simplify_17.png", "(x - 2)/(5x + 25) * (6x + 30)/(6x - 12)", OpSimplify, "1/5", 0, nil},
	{"domain_6.png", "(x - 2)/(5x + 25) * (6x + 30)/(6x - 12)", OpDomain, "{-5, 2}", 0, nil},
	{"simplify_18.png", "(x^2 - 16)/(x^2 - 8x + 16) * (3x - 12)/(x + 4)", OpSimplify, "3", 0, nil},
	{"domain_7.png", "(x^2 - 16)/(x^2 - 8x + 16) * (3x - 12)/(x + 4)", OpDomain, "{-4, 4}", 0, nil},
	{"simplify_19.png", "(2x^2 - 14x)/(x^2 - 4x - 21) / ((x^2 - 5x)/(x + 3))", OpSimplify, "2/(x - 5)", 0, nil},
	{"domain_8.png", "(2x^2 - 14x)/(x^2 - 4x - 21) / ((x^2 - 5x)/(x + 3))", OpDomain, "{-3, 0, 5, 7}", 0, nil},
	{"simplify_20.png", "(x^2 - 25)/(7x - 7) / ((x^2 + 10x + 25)/(x^2 + 4x - 5))", OpSimplify, "(x - 5)/7", 0, nil},
	{"simplify_21.png", "(x + 1)/7 / ((5x + 5)/6)", OpSimplify, "6/35", 0, nil},
	{"domain_9.png", "(x + 1)/7 / ((5x + 5)/6)", OpDomain, "{-1}", 0, nil},
	{"root_2.png", "sqrt[5](-3125)", OpRoot, "(-5)^5", 0, nil},
	{"root_1.png", "sqrt(169)", OpRoot, "13^2", 0, nil},
}

// Check solves the entry and compares the result with the worksheet answer.
// It returns false with a short explanation when they differ.
func (e WorksheetEntry) Check() (bool, string) {
	rep := SolveAt(e.Input, e.Op, e.Vars)
	if rep.Err != nil {
		return false, fmt.Sprintf("error: %v", rep.Err)
	}

	want := e.Expected
	switch e.Op {
	case OpSimplify, OpFactor:
		// A root of a fraction answers with a fraction, which Solve cannot parse.
		if _, _, quotient := quotientRoot(e.Input); quotient {
			break
		}
		// Compare canonical forms, so the worksheet's ordering of terms and factors does not matter.
		expected := Solve(e.Expected, e.Op)
		if expected.Err != nil {
			return false, fmt.Sprintf("bad expected answer %q: %v", e.Expected, expected.Err)
		}
		want = expected.Result
	}

	if rep.Result != want {
		return false, fmt.Sprintf("got %q, want %q", rep.Result, want)
	}
	if e.Degree != 0 && rep.Degree != e.Degree {
		return false, fmt.Sprintf("degree %d, want %d", rep.Degree, e.Degree)
	}
	return true, ""
}
