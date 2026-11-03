package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Operation is the question asked of an expression.
type Operation int

const (
	// OpSimplify writes the expression in standard form and gives its degree.
	OpSimplify Operation = iota
	// OpFactor writes the expression in factored form.
	OpFactor
	// OpTechnique names the factoring method that fits the expression.
	OpTechnique
	// OpPrime reports whether the expression is prime (irreducible over the integers).
	OpPrime
	// OpEvaluate substitutes the given variable values and reports the number.
	OpEvaluate
	// OpSquareRoot fills the blank in "sqrt(n) = r because ___ = n", giving r^2.
	OpSquareRoot
)

func (o Operation) String() string {
	switch o {
	case OpSimplify:
		return "simplify"
	case OpFactor:
		return "factor"
	case OpTechnique:
		return "technique"
	case OpPrime:
		return "prime check"
	case OpEvaluate:
		return "evaluate"
	case OpSquareRoot:
		return "square root blank"
	}
	return "unknown"
}

// Step is one visible moment of the walkthrough.
// Phase is one of Input, Tokens, Parse, Compute, or Result.
type Step struct {
	Phase  string
	Title  string
	Detail string
}

// Report is everything Solve learned about an input.
type Report struct {
	Steps  []Step
	Result string
	Degree int
	Err    error
	// Scenes holds the parser's state for each Parse step, keyed by step index.
	Scenes map[int]ParseEvent
}

// Solve runs the whole pipeline on input with no variable values.
func Solve(input string, op Operation) Report {
	return SolveAt(input, op, nil)
}

// SolveAt runs the whole pipeline on input: TeX to ASCII, tokens, the Pratt
// parse, then the operation. env gives the value of each variable, which
// OpEvaluate needs. Every stage is recorded in Steps. Errors are returned in
// Err rather than panicking.
func SolveAt(input string, op Operation, env map[string]float64) (rep Report) {
	var steps []Step
	defer func() {
		rep.Steps = steps
		if r := recover(); r != nil {
			rep.Err = fmt.Errorf("%v", r)
		}
	}()

	steps = append(steps, Step{"Input", "Read the input", input})
	ascii, notes := normalizeTeX(input)
	for _, note := range notes {
		steps = append(steps, Step{"Input", "Rewrite TeX", note})
	}
	steps = append(steps, Step{"Input", "ASCII form", ascii})

	if op == OpSquareRoot {
		return solveSquareRoot(ascii, &steps)
	}

	var radicals map[string]string
	if op == OpSimplify {
		var rnotes []string
		var err error
		ascii, radicals, rnotes, err = radicalSubst(ascii)
		if err != nil {
			rep.Err = err
			return rep
		}
		for _, n := range rnotes {
			steps = append(steps, Step{"Input", "Treat the radical as a variable", n})
		}
		if radicals != nil {
			steps = append(steps, Step{"Input", "Like radicals combine like terms", ascii})
		}
	}

	var tokens []string
	for _, t := range NewLexer(ascii).Forward() {
		tokens = append(tokens, tokenText(t))
	}
	steps = append(steps, Step{"Tokens", "Split into tokens", strings.Join(tokens, "  |  ")})

	tree, events, err := ParseWithEvents(ascii)
	rep.Scenes = map[int]ParseEvent{}
	for _, ev := range events {
		rep.Scenes[len(steps)] = ev
		steps = append(steps, Step{"Parse", "Pratt parser", ev.Text})
	}
	if err != nil {
		rep.Err = err
		return rep
	}
	steps = append(steps, Step{"Parse", "Syntax tree", tree.String()})

	poly, err := toPoly(tree, &steps)
	if err != nil {
		rep.Err = err
		return rep
	}

	// Only simplifying and evaluating accept negative exponents. Factoring
	// works on whole-number exponents, so reject the rest here.
	if op != OpSimplify && op != OpEvaluate && poly.hasNegativeExponent() {
		rep.Err = fmt.Errorf("%s needs positive exponents, got %s", op, poly)
		return rep
	}

	switch op {
	case OpSimplify:
		rep.Result = radicalRestore(poly.String(), radicals)
		rep.Degree = poly.Degree()
		steps = append(steps, Step{"Result", "Standard form", rep.Result})
		if radicals == nil {
			steps = append(steps, Step{"Result", "Degree", strconv.Itoa(rep.Degree)})
		}

	case OpFactor:
		fz, err := Factor(poly, &steps)
		if err != nil {
			rep.Err = err
			return rep
		}
		rep.Result = fz.String()
		steps = append(steps, Step{"Result", "Factored form", rep.Result})

	case OpTechnique:
		rep.Result = Technique(poly)
		steps = append(steps, Step{"Result", "Technique", rep.Result})

	case OpPrime:
		fz, err := Factor(poly, &steps)
		if err != nil {
			rep.Err = err
			return rep
		}
		rep.Result = strconv.FormatBool(fz.IsPrime())
		steps = append(steps, Step{"Result", "Prime?", rep.Result})

	case OpEvaluate:
		v, err := tree.Eval(env)
		if err != nil {
			rep.Err = err
			return rep
		}
		rep.Result = strconv.FormatFloat(v, 'f', -1, 64)
		steps = append(steps, Step{"Result", "Value", rep.Result})
	}
	return rep
}

func tokenText(t Token) string {
	switch t := t.(type) {
	case Atom:
		return t.Value
	case Op:
		return string(t.Value)
	}
	return "end"
}

// solveSquareRoot answers "sqrt(n) = r because ___ = n". The input is
// sqrt(n) in ASCII, where n is any expression that evaluates to a perfect
// square. The blank is r^2, because squaring the root gives back n.
func solveSquareRoot(ascii string, steps *[]Step) (rep Report) {
	compact := strings.ReplaceAll(ascii, " ", "")
	if !strings.HasPrefix(compact, "sqrt(") || !strings.HasSuffix(compact, ")") {
		rep.Err = fmt.Errorf("square root blank needs sqrt(n), got %q", ascii)
		return rep
	}
	inner := compact[len("sqrt(") : len(compact)-1]

	tree, events, err := ParseWithEvents(inner)
	rep.Scenes = map[int]ParseEvent{}
	for _, ev := range events {
		rep.Scenes[len(*steps)] = ev
		*steps = append(*steps, Step{"Parse", "Pratt parser", ev.Text})
	}
	if err != nil {
		rep.Err = err
		return rep
	}
	*steps = append(*steps, Step{"Parse", "Syntax tree", tree.String()})

	n, err := tree.Eval(nil)
	if err != nil {
		rep.Err = err
		return rep
	}
	if n < 0 || n != math.Trunc(n) {
		rep.Err = fmt.Errorf("sqrt needs a whole number that is not negative, got %s", strconv.FormatFloat(n, 'f', -1, 64))
		return rep
	}
	r := math.Round(math.Sqrt(n))
	if r*r != n {
		rep.Err = fmt.Errorf("%s is not a perfect square", strconv.FormatFloat(n, 'f', -1, 64))
		return rep
	}

	root := strconv.FormatFloat(r, 'f', -1, 64)
	nText := strconv.FormatFloat(n, 'f', -1, 64)
	*steps = append(*steps, Step{"Compute", "Find the root", fmt.Sprintf("sqrt(%s) = %s", nText, root)})
	*steps = append(*steps, Step{"Compute", "Square the root", fmt.Sprintf("%s^2 = %s", root, nText)})
	rep.Result = root + "^2"
	*steps = append(*steps, Step{"Result", "Blank", rep.Result})
	return rep
}
