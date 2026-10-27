package main

import (
	"fmt"
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

// Solve runs the whole pipeline on input: TeX to ASCII, tokens, the Pratt
// parse, then the operation. Every stage is recorded in Steps. Errors are
// returned in Err rather than panicking.
func Solve(input string, op Operation) (rep Report) {
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

	switch op {
	case OpSimplify:
		rep.Result = poly.String()
		rep.Degree = poly.Degree()
		steps = append(steps, Step{"Result", "Standard form", rep.Result})
		steps = append(steps, Step{"Result", "Degree", strconv.Itoa(rep.Degree)})

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
