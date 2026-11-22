package main

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Systems take their equations separated by semicolons. The operations are:
//
//	OpSystem       "x + y = 5; x - y = -15"                       -> {(-5, 10)}
//	OpCheckPoint   "(2, -6); y = 4x - 14; 2x + 4y = -20"          -> Yes
//	OpEliminate    "x + 5y = 2; 5x + 4y = -11; eliminate x; multiply second by 1" -> -5
var eliminateMultiplier = regexp.MustCompile(`multiply\s+(first|second)\s+by\s+(-?\d+(?:/\d+)?)`)

// readLinear reads one linear equation such as "2x + 4y = -20". It returns the
// coefficient of each variable and the constant on the right, so that the
// equation is sum(coef[v] * v) = rhs.
func readLinear(text string, steps *[]Step) (map[string]*big.Rat, *big.Rat, error) {
	sides := strings.Split(text, "=")
	if len(sides) != 2 || strings.TrimSpace(sides[0]) == "" || strings.TrimSpace(sides[1]) == "" {
		return nil, nil, fmt.Errorf("each equation needs one =, got %q", strings.TrimSpace(text))
	}
	var polys [2]Poly
	for i, side := range sides {
		p, err := polyOf(side, steps)
		if err != nil {
			return nil, nil, err
		}
		polys[i] = p
	}
	diff := polys[0].Sub(polys[1])

	coef := map[string]*big.Rat{}
	rhs := new(big.Rat)
	for _, t := range diff {
		c := big.NewRat(int64(t.Coeff), int64(t.den()))
		switch len(t.Vars) {
		case 0:
			// The constant term moves to the right side, so it changes sign.
			rhs.Sub(rhs, c)
		case 1:
			for v, e := range t.Vars {
				if e != 1 {
					return nil, nil, fmt.Errorf("the equation %q is not linear", strings.TrimSpace(text))
				}
				if coef[v] == nil {
					coef[v] = new(big.Rat)
				}
				coef[v].Add(coef[v], c)
			}
		default:
			return nil, nil, fmt.Errorf("the equation %q is not linear", strings.TrimSpace(text))
		}
	}
	return coef, rhs, nil
}

// polyOf parses one side of an equation into a polynomial.
func polyOf(text string, steps *[]Step) (Poly, error) {
	tree, _, err := ParseWithEvents(text)
	if err != nil {
		return nil, err
	}
	return toPoly(tree, steps)
}

func coefOf(coef map[string]*big.Rat, v string) *big.Rat {
	if c, ok := coef[v]; ok {
		return c
	}
	return new(big.Rat)
}

// twoVariables returns the names of the variables in two linear equations,
// in alphabetical order. A system must have exactly two.
func twoVariables(c1, c2 map[string]*big.Rat) (string, string, error) {
	seen := map[string]bool{}
	for _, c := range []map[string]*big.Rat{c1, c2} {
		for v := range c {
			seen[v] = true
		}
	}
	var names []string
	for v := range seen {
		names = append(names, v)
	}
	sort.Strings(names)
	if len(names) != 2 {
		return "", "", fmt.Errorf("a system needs exactly two variables, found %d", len(names))
	}
	return names[0], names[1], nil
}

// solveSystem solves two linear equations in two variables by Cramer's rule,
// and writes the solution as a set containing one ordered pair.
func solveSystem(ascii string, steps *[]Step) (rep Report) {
	parts := strings.Split(ascii, ";")
	if len(parts) != 2 {
		rep.Err = fmt.Errorf(`a system needs two equations separated by ";", got %q`, ascii)
		return rep
	}
	c1, r1, err := readLinear(parts[0], steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	c2, r2, err := readLinear(parts[1], steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	x, y, err := twoVariables(c1, c2)
	if err != nil {
		rep.Err = err
		return rep
	}
	a1, b1, a2, b2 := coefOf(c1, x), coefOf(c1, y), coefOf(c2, x), coefOf(c2, y)

	det := new(big.Rat).Sub(new(big.Rat).Mul(a1, b2), new(big.Rat).Mul(a2, b1))
	*steps = append(*steps, Step{"Compute", "Determinant", ratText(det)})
	if det.Sign() == 0 {
		// The lines are parallel or the same line.
		sameX := new(big.Rat).Sub(new(big.Rat).Mul(a1, r2), new(big.Rat).Mul(a2, r1))
		sameY := new(big.Rat).Sub(new(big.Rat).Mul(b1, r2), new(big.Rat).Mul(b2, r1))
		if sameX.Sign() == 0 && sameY.Sign() == 0 {
			rep.Result = "infinitely many solutions"
		} else {
			rep.Result = "no solution"
		}
		*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
		return rep
	}

	// Cramer's rule: x = (r1 b2 - r2 b1) / det and y = (a1 r2 - a2 r1) / det.
	xNum := new(big.Rat).Sub(new(big.Rat).Mul(r1, b2), new(big.Rat).Mul(r2, b1))
	yNum := new(big.Rat).Sub(new(big.Rat).Mul(a1, r2), new(big.Rat).Mul(a2, r1))
	xv := new(big.Rat).Quo(xNum, det)
	yv := new(big.Rat).Quo(yNum, det)
	*steps = append(*steps, Step{"Compute", x + " = " + ratText(xv), y + " = " + ratText(yv)})
	rep.Result = fmt.Sprintf("{(%s, %s)}", ratText(xv), ratText(yv))
	*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
	return rep
}

// solveCheckPoint says whether an ordered pair satisfies every equation.
func solveCheckPoint(ascii string, steps *[]Step) (rep Report) {
	parts := strings.Split(ascii, ";")
	if len(parts) < 2 {
		rep.Err = fmt.Errorf(`expected a point and its equations separated by ";", got %q`, ascii)
		return rep
	}
	pt := pointPattern.FindStringSubmatch(parts[0])
	if pt == nil {
		rep.Err = fmt.Errorf(`expected an ordered pair like "(2, -6)", got %q`, strings.TrimSpace(parts[0]))
		return rep
	}
	x, _ := strconv.ParseFloat(pt[1], 64)
	y, _ := strconv.ParseFloat(pt[2], 64)
	env := map[string]float64{"x": x, "y": y}

	holds := true
	for _, eq := range parts[1:] {
		sides := strings.Split(eq, "=")
		if len(sides) != 2 {
			rep.Err = fmt.Errorf("each equation needs one =, got %q", strings.TrimSpace(eq))
			return rep
		}
		l, err := evalSide(sides[0], env)
		if err != nil {
			rep.Err = err
			return rep
		}
		r, err := evalSide(sides[1], env)
		if err != nil {
			rep.Err = err
			return rep
		}
		*steps = append(*steps, Step{"Compute", "Substitute (" + formatValue(x) + ", " + formatValue(y) + ") into " + strings.TrimSpace(eq), formatValue(l) + " = " + formatValue(r)})
		if math.Abs(l-r) > 1e-9 {
			holds = false
		}
	}
	rep.Result = "No"
	if holds {
		rep.Result = "Yes"
	}
	*steps = append(*steps, Step{"Result", "Solution of the system", rep.Result})
	return rep
}

func evalSide(text string, env map[string]float64) (float64, error) {
	tree, _, err := ParseWithEvents(text)
	if err != nil {
		return 0, err
	}
	return tree.Eval(env)
}

// solveEliminate answers the blank in the addition method. The input fixes the
// multiplier of one equation, and the answer is the multiplier for the other
// one, which makes the chosen variable cancel when the equations are added.
// For example, "multiply second by 1" answers the first equation's multiplier.
func solveEliminate(ascii string, steps *[]Step) (rep Report) {
	parts := strings.Split(ascii, ";")
	if len(parts) != 4 {
		rep.Err = fmt.Errorf(`elimination needs two equations, "eliminate x", and "multiply first by n" or "multiply second by n", separated by ";", got %q`, ascii)
		return rep
	}
	c1, _, err := readLinear(parts[0], steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	c2, _, err := readLinear(parts[1], steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(parts[2]), "eliminate"))
	m := eliminateMultiplier.FindStringSubmatch(parts[3])
	if m == nil {
		rep.Err = fmt.Errorf(`expected "multiply first by n" or "multiply second by n", got %q`, strings.TrimSpace(parts[3]))
		return rep
	}
	given, other := c1, c2
	givenName, otherName := "first", "second"
	if m[1] == "second" {
		given, other = c2, c1
		givenName, otherName = "second", "first"
	}
	mult, ok := new(big.Rat).SetString(m[2])
	if !ok {
		rep.Err = fmt.Errorf("%q is not a number", m[2])
		return rep
	}

	aGiven, aOther := coefOf(given, v), coefOf(other, v)
	if aGiven.Sign() == 0 || aOther.Sign() == 0 {
		rep.Err = fmt.Errorf("both equations need a %s term to eliminate it", v)
		return rep
	}
	// The x terms cancel when mult * aGiven + otherMult * aOther = 0.
	otherMult := new(big.Rat).Quo(new(big.Rat).Mul(new(big.Rat).Neg(mult), aGiven), aOther)
	*steps = append(*steps, Step{"Compute", "Cancel " + v, fmt.Sprintf("multiply the %s equation by %s and the %s equation by %s", givenName, ratText(mult), otherName, ratText(otherMult))})
	rep.Result = ratText(otherMult)
	*steps = append(*steps, Step{"Result", "Multiplier for the " + otherName + " equation", rep.Result})
	return rep
}
