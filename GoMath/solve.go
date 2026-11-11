package main

import (
	"fmt"
	"math"
	"regexp"
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
	// OpRoot fills the blank in "root(n) = r because ___ = n", giving r^k.
	OpRoot
	// OpRadicalForm rewrites a^(m/n) as a radical without evaluating it.
	OpRadicalForm
	// OpSolve solves an equation for its one variable, answering with a solution set.
	OpSolve
	// OpDomain lists the numbers a rational expression must exclude from its domain.
	OpDomain
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
	case OpRoot:
		return "root blank"
	case OpRadicalForm:
		return "radical form"
	case OpSolve:
		return "solve"
	case OpDomain:
		return "excluded numbers"
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

	if op == OpSimplify {
		if r, ok := solvePowerOfPower(ascii, &steps); ok {
			return r
		}
		if num, den, ok := quotientRoot(ascii); ok {
			return solveQuotientRoot(num, den, &steps)
		}
	}

	if op == OpSolve {
		return solveEquation(ascii, &steps)
	}

	if op == OpDomain {
		return solveDomain(ascii, &steps)
	}

	if op == OpRadicalForm {
		return solveRadicalForm(ascii, &steps)
	}

	if op == OpRoot {
		return solveRoot(ascii, &steps)
	}

	tookRoots := op == OpEvaluate && (strings.Contains(ascii, "sqrt") || fractionalExponent.MatchString(ascii))
	if tookRoots {
		var rnotes []string
		var err error
		ascii, err = evalRoots(ascii, env, &rnotes)
		for _, n := range rnotes {
			steps = append(steps, Step{"Compute", "Take the root", n})
		}
		if err == errNotReal {
			rep.Result = "not a real number"
			steps = append(steps, Step{"Result", "Value", rep.Result})
			return rep
		}
		if err != nil {
			rep.Err = err
			return rep
		}
		steps = append(steps, Step{"Input", "Roots replaced by their values", ascii})
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

	if tookRoots {
		// Root values are often decimals, which the polynomial form rejects.
		v, err := tree.Eval(env)
		if err != nil {
			rep.Err = err
			return rep
		}
		// Powers like 27^(1/3) come out a hair off a whole number.
		if r := math.Round(v); math.Abs(v-r) < 1e-9 {
			v = r
		}
		rep.Result = strconv.FormatFloat(v, 'f', -1, 64)
		steps = append(steps, Step{"Result", "Value", rep.Result})
		return rep
	}

	poly, err := toPoly(tree, &steps)
	if err != nil && op == OpSimplify {
		// A quotient of polynomials cancels common factors instead.
		if n, d, ok := divisionParts(tree); ok {
			np, nerr := toPoly(n, &steps)
			dp, derr := toPoly(d, &steps)
			if nerr == nil && derr == nil {
				text, serr := simplifyRational(np, dp, &steps)
				if serr != nil {
					rep.Err = serr
					return rep
				}
				var scratch []Step
				if dom := solveDomain(ascii, &scratch); dom.Err == nil {
					steps = append(steps, Step{"Compute", "Numbers excluded from the domain", dom.Result})
				}
				rep.Result = text
				steps = append(steps, Step{"Result", "Simplified", rep.Result})
				return rep
			}
		}
	}
	if err != nil && op == OpEvaluate {
		// A polynomial cannot divide by a sum such as 6x - 18, but a number can.
		v, evalErr := tree.Eval(env)
		if evalErr != nil {
			rep.Err = err
			return rep
		}
		rep.Result = formatValue(v)
		steps = append(steps, Step{"Result", "Value", rep.Result})
		return rep
	}
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

	if op != OpSimplify && op != OpEvaluate {
		if err := poly.requireWhole(); err != nil {
			rep.Err = err
			return rep
		}
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

// rootPattern reads sqrt(n) or sqrt[k](n), with k the index of the root.
var rootPattern = regexp.MustCompile(`^sqrt(?:\[(\d+)\])?\((.*)\)$`)

// solveRoot answers "root(n) = r because ___ = n". The input is sqrt(n) or
// sqrt[k](n) in ASCII, where n is any expression whose k-th root is a whole
// number. The blank is r^k, because raising the root to k gives back n. A
// negative root is written in parentheses, as in (-5)^5.
func solveRoot(ascii string, steps *[]Step) (rep Report) {
	compact := strings.ReplaceAll(ascii, " ", "")
	m := rootPattern.FindStringSubmatch(compact)
	if m == nil {
		rep.Err = fmt.Errorf("root blank needs sqrt(n) or sqrt[k](n), got %q", ascii)
		return rep
	}
	k := 2
	if m[1] != "" {
		k, _ = strconv.Atoi(m[1])
		if k < 2 {
			rep.Err = fmt.Errorf("the root index must be at least 2, got %d", k)
			return rep
		}
	}

	tree, events, err := ParseWithEvents(m[2])
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
	nText := strconv.FormatFloat(n, 'f', -1, 64)
	if n != math.Trunc(n) {
		rep.Err = fmt.Errorf("root needs a whole number, got %s", nText)
		return rep
	}
	if n < 0 && k%2 == 0 {
		rep.Err = fmt.Errorf("an even root of %s is not a real number", nText)
		return rep
	}
	r := math.Round(math.Pow(math.Abs(n), 1/float64(k)))
	if n < 0 {
		r = -r
	}
	if math.Pow(r, float64(k)) != n {
		rep.Err = fmt.Errorf("%s is not a perfect power of %d", nText, k)
		return rep
	}

	root := strconv.FormatFloat(r, 'f', -1, 64)
	base := root
	if r < 0 {
		base = "(" + root + ")"
	}
	*steps = append(*steps, Step{"Compute", "Find the root", fmt.Sprintf("the root of index %d of %s is %s", k, nText, root)})
	*steps = append(*steps, Step{"Compute", "Raise the root to the index", fmt.Sprintf("%s^%d = %s", base, k, nText)})
	rep.Result = fmt.Sprintf("%s^%d", base, k)
	*steps = append(*steps, Step{"Result", "Blank", rep.Result})
	return rep
}

// formatValue writes v as a whole number, or as a simplified fraction such as
// 17/22 when v is a ratio of small integers, or else as a decimal. Trying the
// denominators in increasing order finds the fraction in lowest terms.
func formatValue(v float64) string {
	if r := math.Round(v); math.Abs(v-r) < 1e-9 {
		return strconv.FormatFloat(r, 'f', -1, 64)
	}
	for q := 2; q <= 10000; q++ {
		p := math.Round(v * float64(q))
		if math.Abs(v-p/float64(q)) < 1e-9 {
			return fmt.Sprintf("%d/%d", int(p), q)
		}
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
