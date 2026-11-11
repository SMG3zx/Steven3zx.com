package main

import (
	"fmt"
	"sort"
	"strings"
)

// solveDomain finds the numbers a rational expression must exclude from its
// domain, which are the roots of each denominator. The result is a set such as
// {-6}, or "all real numbers" when no denominator can reach zero.
func solveDomain(ascii string, steps *[]Step) (rep Report) {
	tree, events, err := ParseWithEvents(ascii)
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

	var denominators []Poly
	collectDenominators(tree, &denominators)
	if len(denominators) == 0 {
		*steps = append(*steps, Step{"Compute", "No denominators", "nothing can be divided by zero"})
		rep.Result = "all real numbers"
		*steps = append(*steps, Step{"Result", "Excluded numbers", rep.Result})
		return rep
	}

	excluded := map[string]float64{}
	for _, poly := range denominators {
		if err := poly.requireWhole(); err != nil {
			rep.Err = err
			return rep
		}
		if poly.hasNegativeExponent() {
			rep.Err = fmt.Errorf("a denominator here must be a polynomial, got %s", poly)
			return rep
		}
		*steps = append(*steps, Step{"Compute", "Set the denominator to zero", poly.String() + " = 0"})
		set, special, err := solvePoly(poly, steps)
		if err != nil {
			rep.Err = err
			return rep
		}
		switch special {
		case "all real numbers":
			rep.Err = fmt.Errorf("the denominator is always 0, so the expression is never defined")
			return rep
		case "no solution", "no real solution":
			*steps = append(*steps, Step{"Compute", "Never zero", poly.String() + " is never 0, so it excludes nothing"})
			continue
		}
		for _, r := range set {
			excluded[r.text] = r.val
		}
	}

	if len(excluded) == 0 {
		rep.Result = "all real numbers"
	} else {
		texts := make([]string, 0, len(excluded))
		for text := range excluded {
			texts = append(texts, text)
		}
		sort.Slice(texts, func(i, j int) bool { return excluded[texts[i]] < excluded[texts[j]] })
		rep.Result = "{" + strings.Join(texts, ", ") + "}"
	}
	*steps = append(*steps, Step{"Result", "Excluded numbers", rep.Result})
	return rep
}

// collectDenominators gathers every polynomial that must not be zero: the
// right side of each division, and for a fraction that is divided by, its
// numerator too, since dividing by a fraction multiplies by its flip.
func collectDenominators(e SExpression, out *[]Poly) {
	c, ok := e.(Cons)
	if !ok {
		return
	}
	if c.Head == '/' && len(c.Rest) == 2 {
		if num, _, ok := rationalParts(c.Rest[1]); ok {
			*out = append(*out, num)
		} else if p, err := toPoly(c.Rest[1], nil); err == nil {
			*out = append(*out, p)
		}
	}
	for _, r := range c.Rest {
		collectDenominators(r, out)
	}
}
