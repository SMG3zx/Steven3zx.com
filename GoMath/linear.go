package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// This file holds the operations behind the linear-equation unit: inequalities
// in interval notation, slope, point-slope and slope-intercept form, tables of
// values, and solving a formula for one of its variables.

// decimalString writes p with decimal coefficients, such as "0.49x + 6", when
// every coefficient terminates as a decimal. It reports false otherwise.
func (p Poly) decimalString() (string, bool) {
	if len(p) == 0 || p.hasNegativeExponent() {
		return "", false
	}
	var sb strings.Builder
	for i, t := range p {
		d := t.den()
		for _, f := range []int{2, 5} {
			for d%f == 0 {
				d /= f
			}
		}
		if d != 1 {
			return "", false
		}
		c := float64(t.Coeff) / float64(t.den())
		switch {
		case i == 0 && c < 0:
			sb.WriteString("-")
		case i > 0 && c < 0:
			sb.WriteString(" - ")
		case i > 0:
			sb.WriteString(" + ")
		}
		mono := t.Vars.key()
		if mono == "" || math.Abs(c) != 1 {
			sb.WriteString(strconv.FormatFloat(math.Abs(c), 'f', -1, 64))
		}
		sb.WriteString(mono)
	}
	return sb.String(), true
}

// decimalAtom reads a literal such as 0.45 as the exact fraction 45/100.
func decimalAtom(s string) (Poly, error) {
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 9 {
		return nil, fmt.Errorf("%q has too many decimal places", s)
	}
	num, err := strconv.Atoi(whole + frac)
	if err != nil {
		return nil, fmt.Errorf("%q is not a number", s)
	}
	den := 1
	for range frac {
		den *= 10
	}
	return normalize([]Term{mkTerm(num, den, Monomial{})}), nil
}

// interval is a range of real numbers. An end that is infinite has no value.
type interval struct {
	loInf, hiInf   bool
	lo, hi         root
	loIncl, hiIncl bool
}

func newInterval() interval { return interval{loInf: true, hiInf: true} }

func (iv *interval) raiseLo(r root, incl bool) {
	if iv.loInf || r.val > iv.lo.val || (r.val == iv.lo.val && !incl) {
		iv.loInf, iv.lo, iv.loIncl = false, r, incl
	}
}

func (iv *interval) lowerHi(r root, incl bool) {
	if iv.hiInf || r.val < iv.hi.val || (r.val == iv.hi.val && !incl) {
		iv.hiInf, iv.hi, iv.hiIncl = false, r, incl
	}
}

func (iv interval) empty() bool {
	if iv.loInf || iv.hiInf {
		return false
	}
	return iv.lo.val > iv.hi.val || (iv.lo.val == iv.hi.val && !(iv.loIncl && iv.hiIncl))
}

// String writes interval notation, such as (3, ∞) or (-∞, -7].
func (iv interval) String() string {
	if iv.empty() {
		return "no solution"
	}
	if !iv.loInf && !iv.hiInf && iv.lo.val == iv.hi.val {
		return "{" + iv.lo.text + "}"
	}
	left, right := "(-∞", "∞)"
	if !iv.loInf {
		left = "(" + iv.lo.text
		if iv.loIncl {
			left = "[" + iv.lo.text
		}
	}
	if !iv.hiInf {
		right = iv.hi.text + ")"
		if iv.hiIncl {
			right = iv.hi.text + "]"
		}
	}
	return left + ", " + right
}

var relation = regexp.MustCompile(`<=|>=|<|>`)

// solveInequality solves a linear inequality such as 2x + 4 > 10, or a compound
// one such as 3 < 2x + 1 <= 9, and writes the solution in interval notation.
func solveInequality(ascii string, steps *[]Step) (rep Report) {
	s := strings.NewReplacer("≤", "<=", "≥", ">=", `\leq`, "<=", `\geq`, ">=", `\le`, "<=", `\ge`, ">=").Replace(ascii)
	ops := relation.FindAllString(s, -1)
	parts := relation.Split(s, -1)
	if len(ops) == 0 || len(ops) > 2 {
		rep.Err = fmt.Errorf("an inequality needs one or two of < <= > >=, got %q", ascii)
		return rep
	}

	polys := make([]Poly, len(parts))
	for i, part := range parts {
		if strings.TrimSpace(part) == "" {
			rep.Err = fmt.Errorf("an inequality needs an expression on each side, got %q", ascii)
			return rep
		}
		tree, _, err := ParseWithEvents(part)
		if err != nil {
			rep.Err = err
			return rep
		}
		*steps = append(*steps, Step{"Parse", "Syntax tree", tree.String()})
		if polys[i], err = toPoly(tree, steps); err != nil {
			rep.Err = err
			return rep
		}
	}

	iv := newInterval()
	for i, op := range ops {
		diff := polys[i].Sub(polys[i+1])
		*steps = append(*steps, Step{"Compute", "Move everything to the left", fmt.Sprintf("%s %s 0", diff, op)})
		if lcd := diff.commonDenominator(); lcd > 1 {
			diff = diff.Mul(constPoly(lcd))
			*steps = append(*steps, Step{"Compute", fmt.Sprintf("Multiply both sides by %d to clear fractions", lcd), fmt.Sprintf("%s %s 0", diff, op)})
		}
		name, coeffs, err := diff.univariate()
		if err != nil {
			rep.Err = err
			return rep
		}
		if len(coeffs) > 2 {
			rep.Err = fmt.Errorf("inequalities of degree %d are not supported", len(coeffs)-1)
			return rep
		}
		if len(coeffs) < 2 {
			c := 0
			if len(coeffs) == 1 {
				c = coeffs[0]
			}
			holds := map[string]bool{"<": c < 0, "<=": c <= 0, ">": c > 0, ">=": c >= 0}[op]
			*steps = append(*steps, Step{"Compute", "No variable is left", fmt.Sprintf("%d %s 0 is %v", c, op, holds)})
			if !holds {
				iv.raiseLo(root{"1", 1}, false)
				iv.lowerHi(root{"0", 0}, false)
			}
			continue
		}
		a, b := coeffs[1], coeffs[0]
		bound := root{fraction(-b, a), float64(-b) / float64(a)}
		if a < 0 {
			op = map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<="}[op]
			*steps = append(*steps, Step{"Compute", "Dividing by a negative flips the inequality", fmt.Sprintf("%s %s %s", name, op, bound.text)})
		} else {
			*steps = append(*steps, Step{"Compute", "Isolate the variable", fmt.Sprintf("%s %s %s", name, op, bound.text)})
		}
		if op[0] == '>' {
			iv.raiseLo(bound, op == ">=")
		} else {
			iv.lowerHi(bound, op == "<=")
		}
	}

	rep.Result = iv.String()
	if !iv.empty() && iv.loInf && iv.hiInf {
		rep.Result = "all real numbers"
	}
	*steps = append(*steps, Step{"Result", "Solution set", rep.Result})
	return rep
}

var (
	pointPattern = regexp.MustCompile(`\(\s*(-?[\d.]+)\s*,\s*(-?[\d.]+)\s*\)`)
	slopePattern = regexp.MustCompile(`slope\s*=?\s*(-?[\d./]+)`)
)

// line is a non-vertical line given by a slope and one point on it.
type line struct {
	m, x1, y1 float64
	mText     string
}

// readLine reads "(6,2) and (7,6)" or "slope 3 through (3,2)". For two points
// it reports vertical when they share an x-value, with no slope.
func readLine(ascii string, steps *[]Step) (l line, vertical bool, err error) {
	if compact := strings.Join(strings.Fields(ascii), ""); relativePattern.MatchString(compact) {
		return readRelativeLine(compact, steps)
	}
	pts := pointPattern.FindAllStringSubmatch(ascii, -1)
	num := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	switch {
	case len(pts) == 2:
		x1, y1, x2, y2 := num(pts[0][1]), num(pts[0][2]), num(pts[1][1]), num(pts[1][2])
		if x1 == x2 {
			if y1 == y2 {
				return l, false, fmt.Errorf("the two points are the same point")
			}
			*steps = append(*steps, Step{"Compute", "Same x-value", fmt.Sprintf("the run is %s - %s = 0", formatValue(x2), formatValue(x1))})
			return line{x1: x1, y1: y1}, true, nil
		}
		m := (y2 - y1) / (x2 - x1)
		*steps = append(*steps, Step{"Compute", "Slope formula", fmt.Sprintf("m = (%s - %s)/(%s - %s) = %s", formatValue(y2), formatValue(y1), formatValue(x2), formatValue(x1), formatValue(m))})
		return line{m, x1, y1, formatValue(m)}, false, nil
	case len(pts) == 1 && slopePattern.MatchString(ascii):
		txt := slopePattern.FindStringSubmatch(ascii)[1]
		m := num(txt)
		if n, d, ok := strings.Cut(txt, "/"); ok {
			m = num(n) / num(d)
		}
		return line{m, num(pts[0][1]), num(pts[0][2]), formatValue(m)}, false, nil
	}
	return l, false, fmt.Errorf(`expected two points like "(6,2) and (7,6)" or "slope 3 through (3,2)", got %q`, ascii)
}

// relativePattern reads a line parallel or perpendicular to y = px/q, with the
// spaces removed: "parallel to y = 3x through (2,-2)" becomes "paralleltoy=3x...".
var relativePattern = regexp.MustCompile(`(parallel|perpendicular)toy=(-?\d+(?:/\d+)?)x`)

// readRelativeLine reads a line through a point, parallel or perpendicular to
// the line y = px/q. A parallel line has the same slope. A perpendicular line
// has the negative reciprocal slope, -q/p, and a horizontal reference line gives
// a vertical line.
func readRelativeLine(compact string, steps *[]Step) (l line, vertical bool, err error) {
	m := relativePattern.FindStringSubmatch(compact)
	pt := pointPattern.FindStringSubmatch(compact)
	if pt == nil {
		return l, false, fmt.Errorf(`expected a point like "(2,-2)" after the reference line, got %q`, compact)
	}
	x1, _ := strconv.ParseFloat(pt[1], 64)
	y1, _ := strconv.ParseFloat(pt[2], 64)

	p, q := 0, 1
	if n, d, ok := strings.Cut(m[2], "/"); ok {
		p, _ = strconv.Atoi(n)
		q, _ = strconv.Atoi(d)
	} else {
		p, _ = strconv.Atoi(m[2])
	}
	if m[1] == "parallel" {
		*steps = append(*steps, Step{"Compute", "Parallel lines have the same slope", fraction(p, q)})
		return line{float64(p) / float64(q), x1, y1, fraction(p, q)}, false, nil
	}
	if p == 0 {
		*steps = append(*steps, Step{"Compute", "Perpendicular to a horizontal line", "the line is vertical"})
		return line{x1: x1, y1: y1}, true, nil
	}
	num, den := -q, p
	*steps = append(*steps, Step{"Compute", "Perpendicular slopes multiply to -1", fraction(num, den)})
	return line{float64(num) / float64(den), x1, y1, fraction(num, den)}, false, nil
}

// solveSlope answers "the slope is _" and says whether the line rises, falls,
// is horizontal, or is vertical.
func solveSlope(ascii string, steps *[]Step) (rep Report) {
	l, vertical, err := readLine(ascii, steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	switch {
	case vertical:
		rep.Result = "undefined (vertical)"
	case l.m > 0:
		rep.Result = l.mText + " (rises)"
	case l.m < 0:
		rep.Result = l.mText + " (falls)"
	default:
		rep.Result = "0 (horizontal)"
	}
	*steps = append(*steps, Step{"Result", "Slope", rep.Result})
	return rep
}

// signed writes "v" as " - |v|" or " + |v|" after the letter, as in "x + 4".
func signed(name, v string) string {
	if strings.HasPrefix(v, "-") {
		return name + " + " + v[1:]
	}
	return name + " - " + v
}

// pointSlopeForm writes y - y1 = m(x - x1) with the signs resolved.
func pointSlopeForm(l line) string {
	m := l.mText
	if strings.Contains(m, "/") || strings.HasPrefix(m, "-") && strings.Contains(m, "/") {
		m = "(" + m + ")"
	}
	return fmt.Sprintf("%s = %s(%s)", signed("y", formatValue(l.y1)), m, signed("x", formatValue(l.x1)))
}

// slopeInterceptForm writes y = mx + b.
func slopeInterceptForm(l line) string {
	b := l.y1 - l.m*l.x1
	var sb strings.Builder
	sb.WriteString("y = ")
	switch {
	case l.m == 0:
	case l.m == 1:
		sb.WriteString("x")
	case l.m == -1:
		sb.WriteString("-x")
	case strings.Contains(l.mText, "/"):
		sb.WriteString("(" + l.mText + ")x")
	default:
		sb.WriteString(l.mText + "x")
	}
	switch {
	case l.m == 0:
		sb.WriteString(formatValue(b))
	case math.Abs(b) < 1e-9:
	case b < 0:
		sb.WriteString(" - " + formatValue(-b))
	default:
		sb.WriteString(" + " + formatValue(b))
	}
	return sb.String()
}

// solveLineForm writes the equation of a line in point-slope form, or in
// slope-intercept form when slopeIntercept is set. It uses the first point.
func solveLineForm(ascii string, slopeIntercept bool, steps *[]Step) (rep Report) {
	l, vertical, err := readLine(ascii, steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	if vertical {
		rep.Result = "x = " + formatValue(l.x1)
		*steps = append(*steps, Step{"Result", "Vertical line", rep.Result})
		return rep
	}
	*steps = append(*steps, Step{"Compute", "Point-slope form", pointSlopeForm(l)})
	rep.Result = pointSlopeForm(l)
	if slopeIntercept {
		rep.Result = slopeInterceptForm(l)
	}
	*steps = append(*steps, Step{"Result", "Equation", rep.Result})
	return rep
}

var tablePattern = regexp.MustCompile(`^\s*y\s*=\s*(.+?)\s*(?:;\s*([a-z])\s*=\s*(.+))?$`)

// defaultXs are the x-values used when the input does not list any.
const defaultXs = "-3, -2, -1, 0, 1, 2, 3"

// readTable reads "y = x - 2; x = -3, -2, -1". The x-values are optional and
// default to -3 through 3.
func readTable(ascii string, steps *[]Step) (tree SExpression, name string, xs, ys []float64, err error) {
	m := tablePattern.FindStringSubmatch(ascii)
	if m == nil {
		return nil, "", nil, nil, fmt.Errorf(`needs "y = expression" or "y = expression; x = a, b, c", got %q`, ascii)
	}
	name, list := m[2], m[3]
	if name == "" {
		name, list = "x", defaultXs
	}
	tree, _, err = ParseWithEvents(m[1])
	if err != nil {
		return nil, "", nil, nil, err
	}
	*steps = append(*steps, Step{"Parse", "Syntax tree", tree.String()})
	for _, field := range strings.Split(list, ",") {
		x, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil {
			return nil, "", nil, nil, fmt.Errorf("%q is not a number", strings.TrimSpace(field))
		}
		y, err := tree.Eval(map[string]float64{name: x})
		if err != nil {
			return nil, "", nil, nil, err
		}
		xs, ys = append(xs, x), append(ys, y)
		*steps = append(*steps, Step{"Compute", "Substitute", fmt.Sprintf("%s = %s gives y = %s", name, formatValue(x), formatValue(y))})
	}
	return tree, name, xs, ys, nil
}

// solveTable finds y for each listed x, as in "y = x - 2; x = -3, -2, -1".
// The answer is the y-values in the order the x-values are given.
func solveTable(ascii string, steps *[]Step) (rep Report) {
	_, _, _, ys, err := readTable(ascii, steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	texts := make([]string, len(ys))
	for i, y := range ys {
		texts[i] = formatValue(y)
	}
	rep.Result = strings.Join(texts, ", ")
	*steps = append(*steps, Step{"Result", "y-values", rep.Result})
	return rep
}

// solveGraph lists the points of y = expression and draws them. The answer is
// the ordered pairs; the last step before it is the drawing.
func solveGraph(ascii string, steps *[]Step) (rep Report) {
	tree, name, xs, ys, err := readTable(ascii, steps)
	if err != nil {
		rep.Err = err
		return rep
	}
	pairs := make([]string, len(xs))
	for i := range xs {
		pairs[i] = fmt.Sprintf("(%s, %s)", formatValue(xs[i]), formatValue(ys[i]))
	}
	rep.Result = strings.Join(pairs, ", ")
	plot, err := plotGraph(tree, name, xs, ys)
	if err != nil {
		rep.Err = err
		return rep
	}
	*steps = append(*steps, Step{"Compute", "Graph (* are the listed points, . is the line)", plot})
	*steps = append(*steps, Step{"Result", "Points", rep.Result})
	return rep
}

// plotGraph draws the points on axes, three characters per x unit and one row
// per y unit. The axes always include the origin. The dots between the points
// follow the equation, so a line shows as a line.
func plotGraph(tree SExpression, name string, xs, ys []float64) (string, error) {
	const cell = 3
	span := func(vals []float64) (lo, hi int) {
		l, h := 0.0, 0.0
		for _, v := range vals {
			l, h = math.Min(l, v), math.Max(h, v)
		}
		return int(math.Floor(l)), int(math.Ceil(h))
	}
	xmin, xmax := span(xs)
	ymin, ymax := span(ys)
	if xmax-xmin > 40 || ymax-ymin > 40 {
		return "", fmt.Errorf("the graph would be larger than 40 units, x from %d to %d and y from %d to %d", xmin, xmax, ymin, ymax)
	}
	width, height := (xmax-xmin+1)*cell, ymax-ymin+1
	grid := make([][]rune, height)
	for r := range grid {
		grid[r] = []rune(strings.Repeat(" ", width))
	}
	col := func(x float64) int { return int(math.Round((x-float64(xmin))*cell)) + cell/2 }
	row := func(y float64) int { return ymax - int(math.Round(y)) }

	for r := range grid {
		grid[r][col(0)] = '|'
	}
	for c := range grid[row(0)] {
		grid[row(0)][c] = '-'
	}
	grid[row(0)][col(0)] = '+'

	for c := 0; c < width; c++ {
		x := float64(xmin) + float64(c-cell/2)/cell
		if y, err := tree.Eval(map[string]float64{name: x}); err == nil && !math.IsNaN(y) && !math.IsInf(y, 0) {
			if r := row(y); r >= 0 && r < height && grid[r][c] == ' ' {
				grid[r][c] = '.'
			}
		}
	}
	for i := range xs {
		if c, r := col(xs[i]), row(ys[i]); c >= 0 && c < width && r >= 0 && r < height {
			grid[r][c] = '*'
		}
	}

	var sb strings.Builder
	for r := range grid {
		fmt.Fprintf(&sb, "%3d %s\n", ymax-r, string(grid[r]))
	}
	sb.WriteString("    ")
	for x := xmin; x <= xmax; x++ {
		fmt.Fprintf(&sb, "%2d ", x)
	}
	fmt.Fprintf(&sb, "\n    y runs down the left, %s along the bottom", name)
	return sb.String(), nil
}

var solveForPattern = regexp.MustCompile(`^(.*?)\s+for\s+([a-zA-Z])\s*$`)

// solveFor isolates one variable of a formula that is linear in it, as in
// "r = ba for b", which gives b = r/a.
func solveFor(ascii string, steps *[]Step) (rep Report) {
	m := solveForPattern.FindStringSubmatch(ascii)
	sides := []string(nil)
	if m != nil {
		sides = strings.Split(m[1], "=")
	}
	if m == nil || len(sides) != 2 {
		rep.Err = fmt.Errorf(`solve-for needs "formula for v", such as "r = ba for b", got %q`, ascii)
		return rep
	}
	target := m[2]

	var polys [2]Poly
	for i, side := range sides {
		tree, _, err := ParseWithEvents(side)
		if err != nil {
			rep.Err = err
			return rep
		}
		if polys[i], err = toPoly(tree, steps); err != nil {
			rep.Err = err
			return rep
		}
	}
	diff := polys[0].Sub(polys[1])
	if err := diff.requireWhole(); err != nil {
		rep.Err = err
		return rep
	}

	// diff = coef*target + rest, which must equal 0.
	var coef, rest []Term
	for _, t := range diff {
		switch e := t.Vars[target]; e {
		case 0:
			rest = append(rest, t)
		case 1:
			vars := Monomial{}
			for name, exp := range t.Vars {
				if name != target {
					vars[name] = exp
				}
			}
			coef = append(coef, Term{Coeff: t.Coeff, Den: t.Den, Vars: vars})
		default:
			rep.Err = fmt.Errorf("%s appears to the power %d, which is not supported", target, e)
			return rep
		}
	}
	if len(coef) == 0 {
		rep.Err = fmt.Errorf("the formula has no %s to solve for", target)
		return rep
	}
	den, num := normalize(coef), normalize(rest).Neg()
	*steps = append(*steps, Step{"Compute", "Collect the variable", fmt.Sprintf("(%s)%s = %s", den, target, num)})
	text, err := simplifyRational(num, den, steps)
	if err != nil && len(den) == 1 {
		// Factoring cannot handle several variables, but a single term divides directly.
		inv, _ := den.inverse()
		text, err = num.Mul(inv).String(), nil
	}
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Result = target + " = " + text
	*steps = append(*steps, Step{"Result", "Solved for " + target, rep.Result})
	return rep
}
