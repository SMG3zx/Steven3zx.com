package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// solveEquation solves "left = right" for its one variable. It moves
// everything to the left, then handles the degrees it can answer exactly:
// linear equations, and quadratics whose roots are rational. The result is a
// solution set such as {9} or {-3, 5}, or "no solution" or "all real numbers".
func solveEquation(ascii string, steps *[]Step) (rep Report) {
	sides := strings.Split(ascii, "=")
	if len(sides) != 2 || strings.TrimSpace(sides[0]) == "" || strings.TrimSpace(sides[1]) == "" {
		rep.Err = fmt.Errorf("solve needs one equation with a single =, got %q", ascii)
		return rep
	}

	var polys [2]Poly
	for i, side := range sides {
		tree, events, err := ParseWithEvents(side)
		if i == 0 {
			rep.Scenes = map[int]ParseEvent{}
			for _, ev := range events {
				rep.Scenes[len(*steps)] = ev
				*steps = append(*steps, Step{"Parse", "Pratt parser", ev.Text})
			}
		}
		if err != nil {
			rep.Err = err
			return rep
		}
		*steps = append(*steps, Step{"Parse", "Syntax tree", tree.String()})
		polys[i], err = toPoly(tree, steps)
		if err != nil {
			rep.Err = err
			return rep
		}
	}

	diff := polys[0].Sub(polys[1])
	*steps = append(*steps, Step{"Compute", "Move everything to the left", diff.String() + " = 0"})

	name, coeffs, err := diff.univariate()
	if err != nil {
		rep.Err = err
		return rep
	}

	switch len(coeffs) {
	case 0:
		rep.Result = "all real numbers"
		*steps = append(*steps, Step{"Compute", "Both sides are equal", "0 = 0 is always true"})

	case 1:
		rep.Result = "no solution"
		*steps = append(*steps, Step{"Compute", "Nothing left to solve", fmt.Sprintf("%d = 0 is never true", coeffs[0])})

	case 2:
		a, b := coeffs[1], coeffs[0]
		*steps = append(*steps, Step{"Compute", "Isolate the variable", fmt.Sprintf("%s = %s", name, fraction(-b, a))})
		rep.Result = "{" + fraction(-b, a) + "}"

	case 3:
		a, b, c := coeffs[2], coeffs[1], coeffs[0]
		disc := b*b - 4*a*c
		*steps = append(*steps, Step{"Compute", "Discriminant", fmt.Sprintf("b^2 - 4ac = %d", disc)})
		if disc < 0 {
			rep.Result = "no real solution"
			break
		}
		root := isqrt(disc)
		if root*root != disc {
			rep.Err = fmt.Errorf("the roots of %s = 0 are not rational, which is not supported", diff)
			return rep
		}
		set := map[string]float64{}
		for _, num := range []int{-b - root, -b + root} {
			set[fraction(num, 2*a)] = float64(num) / float64(2*a)
		}
		texts := make([]string, 0, len(set))
		for t := range set {
			texts = append(texts, t)
		}
		sort.Slice(texts, func(i, j int) bool { return set[texts[i]] < set[texts[j]] })
		*steps = append(*steps, Step{"Compute", "Quadratic formula", fmt.Sprintf("%s = (-b ± sqrt(%d)) / 2a", name, disc)})
		rep.Result = "{" + strings.Join(texts, ", ") + "}"

	default:
		rep.Err = fmt.Errorf("solve handles degree 2 and below, got degree %d", len(coeffs)-1)
		return rep
	}
	*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
	return rep
}

// fraction writes num/den in lowest terms, as a whole number when it divides.
func fraction(num, den int) string {
	if den < 0 {
		num, den = -num, -den
	}
	if g := gcd(abs(num), den); g > 1 {
		num, den = num/g, den/g
	}
	if den == 1 {
		return strconv.Itoa(num)
	}
	return fmt.Sprintf("%d/%d", num, den)
}

// isqrt returns the whole-number square root of n, rounded down.
func isqrt(n int) int {
	r := 0
	for (r+1)*(r+1) <= n {
		r++
	}
	return r
}
