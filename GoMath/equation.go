package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// solveEquation solves "left = right" for its one variable. It moves
// everything to the left, then handles the degrees it can answer exactly:
// linear equations, and quadratics whose roots are rational. The result is a
// solution set such as {9} or {-3, 5}, or "no solution" or "all real numbers".
func solveEquation(ascii string, steps *[]Step) (rep Report) {
	if r, ok := solveRationalPower(ascii, steps); ok {
		return r
	}

	if strings.Contains(ascii, "sqrt") {
		return solveRadicalEquation(ascii, steps)
	}

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

	// Multiply through by the least common denominator to clear fractions.
	if lcd := diff.commonDenominator(); lcd > 1 {
		diff = diff.Mul(constPoly(lcd))
		*steps = append(*steps, Step{"Compute", fmt.Sprintf("Multiply both sides by %d to clear fractions", lcd), diff.String() + " = 0"})
	}
	if err := diff.requireWhole(); err != nil {
		rep.Err = err
		return rep
	}

	set, special, err := solvePoly(diff, steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Result = special
	if special == "" {
		rep.Result = formatSet(set)
	}
	*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
	return rep
}

// root is one solution, as exact text and as a number for checking.
type root struct {
	text string
	val  float64
}

// formatSet writes roots as a solution set in ascending order, such as {-3, 5}.
func formatSet(set []root) string {
	if len(set) == 0 {
		return "no solution"
	}
	texts := make([]string, len(set))
	for i, r := range set {
		texts[i] = r.text
	}
	return "{" + strings.Join(texts, ", ") + "}"
}

// solvePoly finds the roots of diff = 0. Degrees up to 2 are solved exactly;
// higher degrees are handled by solveByRationalRoots. special is set instead
// of the roots for "all real numbers", "no solution", and "no real solution".
func solvePoly(diff Poly, steps *[]Step) (set []root, special string, err error) {
	name, coeffs, err := diff.univariate()
	if err != nil {
		return nil, "", err
	}

	switch len(coeffs) {
	case 0:
		*steps = append(*steps, Step{"Compute", "Both sides are equal", "0 = 0 is always true"})
		return nil, "all real numbers", nil

	case 1:
		*steps = append(*steps, Step{"Compute", "Nothing left to solve", fmt.Sprintf("%d = 0 is never true", coeffs[0])})
		return nil, "no solution", nil

	case 2:
		a, b := coeffs[1], coeffs[0]
		*steps = append(*steps, Step{"Compute", "Isolate the variable", fmt.Sprintf("%s = %s", name, fraction(-b, a))})
		return []root{{fraction(-b, a), float64(-b) / float64(a)}}, "", nil

	case 3:
		a, b, c := coeffs[2], coeffs[1], coeffs[0]
		disc := b*b - 4*a*c
		*steps = append(*steps, Step{"Compute", "Discriminant", fmt.Sprintf("b^2 - 4ac = %d", disc)})
		if disc < 0 {
			return nil, "no real solution", nil
		}
		rt := isqrt(disc)
		if rt*rt != disc {
			return nil, "", fmt.Errorf("the roots of %s = 0 are not rational, which is not supported", diff)
		}
		seen := map[string]bool{}
		for _, num := range []int{-b - rt, -b + rt} {
			text := fraction(num, 2*a)
			if !seen[text] {
				seen[text] = true
				set = append(set, root{text, float64(num) / float64(2*a)})
			}
		}
		sort.Slice(set, func(i, j int) bool { return set[i].val < set[j].val })
		*steps = append(*steps, Step{"Compute", "Quadratic formula", fmt.Sprintf("%s = (-b ± sqrt(%d)) / 2a", name, disc)})
		return set, "", nil
	}
	return solveByRationalRoots(name, coeffs, steps)
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

// solveRadicalEquation solves an equation with one radical, such as
// sqrt(x + 4) = 8. It isolates the radical, raises both sides to its index,
// solves the polynomial that is left, and then checks every candidate in the
// original equation, because raising to a power can add extraneous roots.
func solveRadicalEquation(ascii string, steps *[]Step) (rep Report) {
	matches := radicalPattern.FindAllStringSubmatch(ascii, -1)
	if len(matches) != 1 {
		rep.Err = fmt.Errorf("a radical equation here needs exactly one radical, with no parentheses inside it, got %q", ascii)
		return rep
	}
	k := 2
	if matches[0][1] != "" {
		k, _ = strconv.Atoi(matches[0][1])
		if k < 2 {
			rep.Err = fmt.Errorf("the root index must be at least 2, got %d", k)
			return rep
		}
	}

	// Stand in a free letter for the radical so both sides are polynomials.
	letter := ""
	for _, r := range "zyxwvutsrqponmlkjihgfedcba" {
		if !strings.ContainsRune(ascii, r) {
			letter = string(r)
			break
		}
	}
	if letter == "" {
		rep.Err = fmt.Errorf("no free letter to stand for the radical")
		return rep
	}
	rad, _, err := toPolyText(matches[0][2])
	if err != nil {
		rep.Err = err
		return rep
	}
	replaced := radicalPattern.ReplaceAllString(ascii, "("+letter+")")
	*steps = append(*steps, Step{"Input", "Treat the radical as a variable", fmt.Sprintf("%s = %s, so the equation is %s", matches[0][0], letter, replaced)})

	sides := strings.Split(replaced, "=")
	if len(sides) != 2 {
		rep.Err = fmt.Errorf("solve needs one equation with a single =, got %q", ascii)
		return rep
	}
	left, _, err := toPolyText(sides[0])
	if err != nil {
		rep.Err = err
		return rep
	}
	right, _, err := toPolyText(sides[1])
	if err != nil {
		rep.Err = err
		return rep
	}
	diff := left.Sub(right)

	// Split diff into c*letter plus the rest. The radical must appear alone
	// and to the first power.
	c := 0
	var rest []Term
	for _, t := range diff {
		switch {
		case t.Vars[letter] == 0:
			rest = append(rest, t)
		case t.Vars[letter] == 1 && len(t.Vars) == 1:
			c += t.Coeff
		default:
			rep.Err = fmt.Errorf("the radical must appear alone and to the first power, got %s", diff)
			return rep
		}
	}
	if c == 0 {
		rep.Err = fmt.Errorf("the radical cancels out of %s", diff)
		return rep
	}
	restPoly := normalize(rest)
	isolated := restPoly.Neg() // c * radical = isolated
	*steps = append(*steps, Step{"Compute", "Isolate the radical", fmt.Sprintf("%d%s = %s", c, matches[0][0], isolated)})

	// c^k * radicand = isolated^k
	lhs := rad.Mul(constPoly(ipow(c, k)))
	rhs := isolated.Pow(k)
	*steps = append(*steps, Step{"Compute", fmt.Sprintf("Raise both sides to the power %d", k), fmt.Sprintf("%s = %s", lhs, rhs)})

	set, special, err := solvePoly(lhs.Sub(rhs), steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	if special != "" {
		rep.Err = fmt.Errorf("this radical equation reduced to %q, which is not supported", special)
		return rep
	}

	var good []root
	for _, r := range set {
		env := map[string]float64{}
		for _, name := range append(diff.Vars(), rad.Vars()...) {
			if name != letter {
				env[name] = r.val
			}
		}
		under := evalPoly(rad, env)
		var val float64
		ok := true
		switch {
		case under < -1e-9 && k%2 == 0:
			ok = false
		default:
			val = math.Pow(math.Abs(under), 1/float64(k))
			if under < 0 {
				val = -val
			}
		}
		if ok {
			env[letter] = val
			ok = math.Abs(evalPoly(diff, env)) < 1e-9
		}
		verdict := "checks"
		if !ok {
			verdict = "is extraneous, so it is dropped"
		} else {
			good = append(good, r)
		}
		*steps = append(*steps, Step{"Compute", "Check the candidate", fmt.Sprintf("%s %s", r.text, verdict)})
	}
	rep.Result = formatSet(good)
	*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
	return rep
}

// toPolyText parses ASCII and reads it as a polynomial.
func toPolyText(s string) (Poly, SExpression, error) {
	tree, _, err := ParseWithEvents(s)
	if err != nil {
		return nil, nil, err
	}
	p, err := toPoly(tree, &[]Step{})
	if err == nil {
		err = p.requireWhole()
	}
	return p, tree, err
}

// evalPoly evaluates p with the given variable values.
func evalPoly(p Poly, env map[string]float64) float64 {
	total := 0.0
	for _, t := range p {
		v := float64(t.Coeff) / float64(t.den())
		for name, e := range t.Vars {
			v *= math.Pow(env[name], float64(e))
		}
		total += v
	}
	return total
}

// rationalPowerSide reads v^(p/q) with whole p and q, allowing TeX's extra
// parentheses around the numerator and denominator.
var rationalPowerSide = regexp.MustCompile(`^([a-zA-Z])\^\(\(?(\d+)\)?/\(?(\d+)\)?\)$`)

// solveRationalPower solves v^(p/q) = K for a whole number K, such as
// x^(5/2) = 243. Raising both sides to the power q gives v^p = K^q. Each
// whole-number root of that is then checked in the original equation, with
// v^(p/q) read as the real q-th root of v, raised to p.
func solveRationalPower(ascii string, steps *[]Step) (rep Report, ok bool) {
	sides := strings.Split(strings.ReplaceAll(ascii, " ", ""), "=")
	if len(sides) != 2 {
		return rep, false
	}
	var g []string
	var other string
	for i, side := range sides {
		if m := rationalPowerSide.FindStringSubmatch(side); m != nil {
			g, other = m, sides[1-i]
			break
		}
	}
	if g == nil {
		return rep, false
	}

	tree, _, err := ParseWithEvents(other)
	if err != nil {
		rep.Err = err
		return rep, true
	}
	kf, err := tree.Eval(nil)
	if err != nil || kf != math.Trunc(kf) {
		rep.Err = fmt.Errorf("the other side must be a whole number, got %q", other)
		return rep, true
	}
	p, _ := strconv.Atoi(g[2])
	q, _ := strconv.Atoi(g[3])
	if p == 0 || q == 0 {
		rep.Err = fmt.Errorf("the exponent %d/%d is not supported", p, q)
		return rep, true
	}
	if d := gcd(p, q); d > 1 {
		p, q = p/d, q/d
	}
	name := g[1]

	rhs := math.Pow(kf, float64(q))
	*steps = append(*steps, Step{"Compute", fmt.Sprintf("Raise both sides to the power %d", q), fmt.Sprintf("%s^%d = %s", name, p, strconv.FormatFloat(rhs, 'f', -1, 64))})

	// Roots of v^p = rhs: one real root when p is odd, a pair when p is even.
	var cands []float64
	r := math.Pow(math.Abs(rhs), 1/float64(p))
	if rr := math.Round(r); math.Abs(r-rr) < 1e-9*math.Max(1, r) {
		r = rr
	} else {
		rep.Err = fmt.Errorf("the solution %s is not a whole number, which is not supported", strconv.FormatFloat(r, 'f', -1, 64))
		return rep, true
	}
	switch {
	case rhs == 0:
		cands = []float64{0}
	case p%2 == 1 && rhs < 0:
		cands = []float64{-r}
	case p%2 == 1:
		cands = []float64{r}
	case rhs > 0:
		cands = []float64{-r, r}
	}

	var good []root
	for _, v := range cands {
		ok := !(v < 0 && q%2 == 0)
		if ok {
			rt := math.Pow(math.Abs(v), 1/float64(q))
			if v < 0 {
				rt = -rt
			}
			ok = math.Abs(math.Pow(rt, float64(p))-kf) < 1e-9*math.Max(1, math.Abs(kf))
		}
		text := strconv.FormatFloat(v, 'f', -1, 64)
		verdict := "checks"
		if ok {
			good = append(good, root{text, v})
		} else {
			verdict = "is extraneous, so it is dropped"
		}
		*steps = append(*steps, Step{"Compute", "Check the candidate", fmt.Sprintf("%s %s", text, verdict)})
	}
	rep.Result = formatSet(good)
	*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
	return rep, true
}
