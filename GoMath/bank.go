package main

import "fmt"

// WorksheetEntry is one problem from the worksheet screenshots in equations/.
// Expected is the answer the worksheet gives, written in ASCII.
type WorksheetEntry struct {
	Source   string
	Input    string
	Op       Operation
	Expected string
	// Degree is the degree the worksheet asks for, or 0 when it does not ask.
	Degree int
}

// Worksheet holds one entry per screenshot in equations/, in file order.
var Worksheet = []WorksheetEntry{
	{"image.png", "(-4x^3 + 4x^2 - 7x + 7) + (7x^3 + 4x^2 - 3x - 7)", OpSimplify, "3x^3 + 8x^2 - 10x", 3},
	{"image_1.png", "(2x^3 - 9x^2 + 8x - 5) - (5x^3 - 10x^2 - 5x + 6)", OpSimplify, "-3x^3 + x^2 + 13x - 11", 3},
	{"image_2.png", "(7x^2 - 6x - 5) + (3x^2 - 5x + 7) - (x^2 - 2x - 1)", OpSimplify, "9x^2 - 9x + 3", 2},
	{"image_3.png", "(x + 4)(x^2 - 4x + 16)", OpSimplify, "x^3 + 64", 0},
	{"image_4.png", "(x + 6)(x + 5)", OpSimplify, "x^2 + 11x + 30", 0},
	{"image_5.png", "(x - 14)(x + 8)", OpSimplify, "x^2 - 6x - 112", 0},
	{"image_6.png", "(4x - 7)(3x + 8)", OpSimplify, "12x^2 + 11x - 56", 0},
	{"image_7.png", "(x - 1)(x + 1)", OpSimplify, "x^2 - 1", 0},
	{"image_8.png", "(4x + 3)(4x - 3)", OpSimplify, "16x^2 - 9", 0},
	{"image_9.png", "(x + 11)^2", OpSimplify, "x^2 + 22x + 121", 0},
	{"image_10.png", "(x - 5)^2", OpSimplify, "x^2 - 10x + 25", 0},
	{"image_11.png", "(3 - 8x)^2", OpSimplify, "64x^2 - 48x + 9", 0},
	{"image_12.png", "(7x^2y + 6xy - 1) + (-4x^2y + 2xy - 10)", OpSimplify, "3x^2y + 8xy - 11", 3},
	{"image_13.png", "(7x^4y^2 + 10x^3y - 7y) - (4x^4y^2 + 5x^3y + 4y + 4x)", OpSimplify, "3x^4y^2 + 5x^3y - 11y - 4x", 6},
	{"image_14.png", "(4x + 3y)(4x - 3y)", OpSimplify, "16x^2 - 9y^2", 0},
	{"image_15.png", "64x^3 - 125", OpTechnique, "difference of cubes", 0},
	{"image_16.png", "16x + 40", OpFactor, "8(2x + 5)", 0},
	{"image_17.png", "4x^4 - 44x^3 + 20x^2", OpFactor, "4x^2(x^2 - 11x + 5)", 0},
	{"image_18.png", "x(x + 3) - 5(x + 3)", OpFactor, "(x + 3)(x - 5)", 0},
	{"image_19.png", "x^2(x - 8) + 12(x - 8)", OpFactor, "(x - 8)(x^2 + 12)", 0},
	{"image_20.png", "x^3 - 5x^2 + 8x - 40", OpFactor, "(x - 5)(x^2 + 8)", 0},
	{"image_21.png", "2x^3 - 5x^2 - 12x + 30", OpFactor, "(2x - 5)(x^2 - 6)", 0},
	{"image_22.png", "x^2 + 14x + 33", OpFactor, "(x + 3)(x + 11)", 0},
	{"image_23.png", "x^2 - 12x - 45", OpFactor, "(x - 15)(x + 3)", 0},
	{"image_24.png", "x^2 - 17x + 72", OpFactor, "(x - 8)(x - 9)", 0},
	{"image_25.png", "7x^2 - 20x - 3", OpFactor, "(7x + 1)(x - 3)", 0},
	{"image_26.png", "6x^2 - 23x + 21", OpFactor, "(3x - 7)(2x - 3)", 0},
	{"image_27.png", "x^2 - 64", OpFactor, "(x - 8)(x + 8)", 0},
	{"image_28.png", "4x^2 - 81", OpFactor, "(2x - 9)(2x + 9)", 0},
	{"image_29.png", "y^2 + 18y + 81", OpFactor, "(y + 9)^2", 0},
	{"image_30.png", "x^2 - 6x + 9", OpFactor, "(x - 3)^2", 0},
	{"image_31.png", "36x^2 - 12x + 1", OpFactor, "(6x - 1)^2", 0},
	{"image_32.png", "2x^3 - 32x", OpFactor, "2x(x - 4)(x + 4)", 0},
	{"image_33.png", "x^2 + 4", OpPrime, "true", 0},
	{"image_34.png", "x^2 - 2x - 2", OpPrime, "true", 0},
}

// Check solves the entry and compares the result with the worksheet answer.
// It returns false with a short explanation when they differ.
func (e WorksheetEntry) Check() (bool, string) {
	rep := Solve(e.Input, e.Op)
	if rep.Err != nil {
		return false, fmt.Sprintf("error: %v", rep.Err)
	}

	want := e.Expected
	switch e.Op {
	case OpSimplify, OpFactor:
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
