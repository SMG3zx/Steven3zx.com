package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	radicalPattern  = regexp.MustCompile(`sqrt(?:\[(\d+)\])?\(([^()]*)\)`)
	quotientPattern = regexp.MustCompile(`sqrt(?:\[(\d+)\])?\(([^()]*)\)\s*/\s*sqrt(?:\[(\d+)\])?\(([^()]*)\)`)
	productPattern  = regexp.MustCompile(`sqrt(?:\[(\d+)\])?\(([^()]*)\)\s*\*?\s*sqrt(?:\[(\d+)\])?\(([^()]*)\)`)
)

// radicalSubst rewrites each sqrt(R) in ascii as a stand-in variable so the
// polynomial engine can combine like radicals: 7sqrt(2) + 11sqrt(2) becomes
// 7a + 11a. First the product rule joins neighbours, so sqrt(6x)*sqrt(30x)
// becomes sqrt(6x*30x), and the quotient rule joins sqrt(A)/sqrt(B) into sqrt(A/B) when B divides A exactly. R must then be a single term. Its largest square
// factor comes out, with variables taken as nonnegative, so sqrt(180x^2)
// becomes 6x times a for sqrt(5). Perfect squares leave no radical.
// back maps each stand-in letter to the radical it replaced. When ascii has
// no sqrt(...), it returns ascii unchanged with a nil map.
func radicalSubst(ascii string) (out string, back map[string]string, notes []string, err error) {
	if !strings.Contains(ascii, "sqrt") {
		return ascii, nil, nil, nil
	}
	out = ascii
	// Neighbours join only when they share an index, so sqrt(2)sqrt[3](4)
	// stays as two radicals. Each pass must change something to go on.
	for changed := true; changed && productPattern.MatchString(out); {
		changed = false
		out = productPattern.ReplaceAllStringFunc(out, func(m string) string {
			g := productPattern.FindStringSubmatch(m)
			if g[1] != g[3] {
				return m
			}
			joined := rootText(g[1], wrapSum(g[2])+"*"+wrapSum(g[4]))
			notes = append(notes, fmt.Sprintf("product rule: %s = %s", m, joined))
			changed = true
			return joined
		})
	}

	out = quotientPattern.ReplaceAllStringFunc(out, func(m string) string {
		g := quotientPattern.FindStringSubmatch(m)
		if g[1] != g[3] {
			return m
		}
		q, ok := divideTerms(g[2], g[4])
		if !ok {
			return m
		}
		joined := rootText(g[1], q)
		notes = append(notes, fmt.Sprintf("quotient rule: %s = %s", m, joined))
		return joined
	})

	back = map[string]string{}
	letterOf := map[string]string{}
	// Late letters sort after most variables, so radicals print last: 6xsqrt(5).
	free := []rune("zyxwvutsrqponmlkjihgfedcba")
	isFree := func(r rune) bool { return !strings.ContainsRune(ascii, r) }

	out = radicalPattern.ReplaceAllStringFunc(out, func(m string) string {
		if err != nil {
			return m
		}
		g := radicalPattern.FindStringSubmatch(m)
		k := 2
		if g[1] != "" {
			k, _ = strconv.Atoi(g[1])
			if k < 2 {
				err = fmt.Errorf("the root index must be at least 2, got %d", k)
				return m
			}
		}
		outside, inside, e := splitRadicand(g[2], k)
		if e != nil {
			err = e
			return m
		}
		rep := outside
		if inside != "1" {
			sym := "sqrt(" + inside + ")"
			if k != 2 {
				sym = fmt.Sprintf("sqrt[%d](%s)", k, inside)
			}
			l, ok := letterOf[sym]
			if !ok {
				for len(free) > 0 && !isFree(free[0]) {
					free = free[1:]
				}
				if len(free) == 0 {
					err = fmt.Errorf("too many different radicals")
					return m
				}
				l = string(free[0])
				free = free[1:]
				letterOf[sym] = l
				back[l] = sym
			}
			if rep == "1" {
				rep = l
			} else {
				rep += l
			}
		}
		notes = append(notes, fmt.Sprintf("%s = %s", m, rep))
		return "(" + rep + ")"
	})
	if err == nil && strings.Contains(out, "sqrt") {
		err = fmt.Errorf("a radical here must hold a single term, such as sqrt(6x), got %q", ascii)
	}
	return out, back, notes, err
}

// rootText writes the root of index k (empty for a square root) of radicand.
func rootText(k, radicand string) string {
	if k == "" {
		return "sqrt(" + radicand + ")"
	}
	return "sqrt[" + k + "](" + radicand + ")"
}

// divideTerms divides one single-term expression by another, such as
// 147x^3 by 3x giving 49x^2. ok is false unless the division is exact.
func divideTerms(a, b string) (string, bool) {
	parse := func(r string) (Term, bool) {
		tree, _, err := ParseWithEvents(r)
		if err != nil {
			return Term{}, false
		}
		p, err := toPoly(tree, &[]Step{})
		if err != nil || len(p) != 1 || !p.whole() || p.hasNegativeExponent() {
			return Term{}, false
		}
		return p[0], true
	}
	ta, ok := parse(a)
	if !ok {
		return "", false
	}
	tb, ok := parse(b)
	if !ok || tb.Coeff == 0 || ta.Coeff%tb.Coeff != 0 {
		return "", false
	}
	vars := Monomial{}
	for name, e := range ta.Vars {
		vars[name] = e
	}
	for name, e := range tb.Vars {
		if vars[name] < e {
			return "", false
		}
		vars[name] -= e
		if vars[name] == 0 {
			delete(vars, name)
		}
	}
	return Poly{{Coeff: ta.Coeff / tb.Coeff, Vars: vars}}.String(), true
}

// wrapSum puts parentheses around s when it holds a sum or difference.
func wrapSum(s string) string {
	if strings.ContainsAny(s, "+-") {
		return "(" + s + ")"
	}
	return s
}

// splitRadicand writes the term under a k-th root as outside times
// sqrt[k](inside), both as ASCII. A coefficient pulls out its largest k-th
// power factor and each variable pulls out its exponent divided by k, rounded
// down, leaving the remainder inside. outside is "1" and inside is "1" when
// nothing is left on that side.
func splitRadicand(r string, k int) (outside, inside string, err error) {
	tree, _, err := ParseWithEvents(r)
	if err != nil {
		return "", "", err
	}
	poly, err := toPoly(tree, &[]Step{})
	if err != nil {
		return "", "", err
	}
	if len(poly) != 1 || poly[0].Coeff < 0 || !poly.whole() || poly.hasNegativeExponent() {
		return "", "", fmt.Errorf("a radical here must hold a single term with a positive whole-number coefficient, got sqrt(%s)", r)
	}
	t := poly[0]

	out, in := 1, t.Coeff
	for f := 2; ipow(f, k) <= in; {
		if in%ipow(f, k) == 0 {
			in /= ipow(f, k)
			out *= f
		} else {
			f++
		}
	}
	outVars, inVars := Monomial{}, Monomial{}
	for name, e := range t.Vars {
		if e/k > 0 {
			outVars[name] = e / k
		}
		if e%k > 0 {
			inVars[name] = e % k
		}
	}

	outside = strconv.Itoa(out) + outVars.key()
	if out == 1 && len(outVars) > 0 {
		outside = outVars.key()
	}
	inside = strconv.Itoa(in) + inVars.key()
	if in == 1 && len(inVars) > 0 {
		inside = inVars.key()
	}
	return outside, inside, nil
}

// ipow returns b raised to the whole-number power n.
func ipow(b, n int) int {
	r := 1
	for ; n > 0; n-- {
		r *= b
	}
	return r
}

// radicalRestore puts each radical back in place of its stand-in letter.
func radicalRestore(s string, back map[string]string) string {
	for l, r := range back {
		s = strings.ReplaceAll(s, l, r)
	}
	return s
}

// errNotReal reports an even root of a negative number.
var errNotReal = fmt.Errorf("not a real number")

// evalRoots replaces every sqrt(...) and sqrt[k](...) in ascii with its
// numeric value, innermost first, so the evaluator can finish the rest. env
// gives variable values for the radicand. It returns errNotReal when an even
// root of a negative number turns up.
func evalRoots(ascii string, env map[string]float64, notes *[]string) (string, error) {
	for {
		i := strings.LastIndex(ascii, "sqrt")
		if i < 0 {
			return ascii, nil
		}
		j := i + len("sqrt")
		k := 2.0
		if j < len(ascii) && ascii[j] == '[' {
			end := strings.IndexByte(ascii[j:], ']')
			if end < 0 {
				return "", fmt.Errorf("unclosed [ in %q", ascii)
			}
			idx, err := strconv.Atoi(strings.TrimSpace(ascii[j+1 : j+end]))
			if err != nil || idx < 2 {
				return "", fmt.Errorf("the root index must be a whole number of at least 2")
			}
			k = float64(idx)
			j += end + 1
		}
		if j >= len(ascii) || ascii[j] != '(' {
			return "", fmt.Errorf("sqrt needs parentheses, as in sqrt(9)")
		}
		depth, end := 0, -1
		for p := j; p < len(ascii) && end < 0; p++ {
			switch ascii[p] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = p
				}
			}
		}
		if end < 0 {
			return "", fmt.Errorf("unclosed ( in %q", ascii)
		}
		tree, _, err := ParseWithEvents(ascii[j+1 : end])
		if err != nil {
			return "", err
		}
		n, err := tree.Eval(env)
		if err != nil {
			return "", err
		}
		if n < 0 && math.Mod(k, 2) == 0 {
			*notes = append(*notes, fmt.Sprintf("%s = %s, which is negative under an even root", ascii[i:end+1], strconv.FormatFloat(n, 'f', -1, 64)))
			return "", errNotReal
		}
		r := math.Pow(math.Abs(n), 1/k)
		if rr := math.Round(r); math.Pow(rr, k) == math.Abs(n) {
			r = rr
		}
		if n < 0 {
			r = -r
		}
		val := strconv.FormatFloat(r, 'f', -1, 64)
		*notes = append(*notes, fmt.Sprintf("%s = %s", ascii[i:end+1], val))
		ascii = ascii[:i] + "(" + val + ")" + ascii[end+1:]
	}
}

// quotientRoot splits sqrt(A/B) at the top-level slash. ok is false when
// ascii is not a single root of a fraction.
func quotientRoot(ascii string) (num, den string, ok bool) {
	c := strings.ReplaceAll(ascii, " ", "")
	if !strings.HasPrefix(c, "sqrt(") || !strings.HasSuffix(c, ")") {
		return "", "", false
	}
	inner := c[len("sqrt(") : len(c)-1]
	depth := 0
	for i, r := range inner {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", "", false
			}
		case '/':
			if depth == 0 {
				return inner[:i], inner[i+1:], true
			}
		}
	}
	return "", "", false
}

// solveQuotientRoot applies the quotient rule, sqrt(A/B) = sqrt(A)/sqrt(B),
// to whole numbers A and B. Square factors come out of each root and the
// whole-number parts are reduced, so sqrt(1/144) gives 1/12 and sqrt(20/9)
// gives 2sqrt(5)/3. The same radical on top and bottom cancels. A denominator that stays under a root would need
// rationalizing, which is not supported.
func solveQuotientRoot(num, den string, steps *[]Step) (rep Report) {
	*steps = append(*steps, Step{"Compute", "Quotient rule", fmt.Sprintf("sqrt(%s/%s) = sqrt(%s)/sqrt(%s)", num, den, num, den)})

	parts := [2]struct{ out, in int }{}
	for i, side := range []string{num, den} {
		tree, _, err := ParseWithEvents(side)
		if err != nil {
			rep.Err = err
			return rep
		}
		v, err := tree.Eval(nil)
		if err != nil {
			rep.Err = err
			return rep
		}
		if v != math.Trunc(v) || v <= 0 {
			rep.Err = fmt.Errorf("the quotient rule here needs positive whole numbers, got %s", strconv.FormatFloat(v, 'f', -1, 64))
			return rep
		}
		outside, inside, err := splitRadicand(strconv.FormatFloat(v, 'f', -1, 64), 2)
		if err != nil {
			rep.Err = err
			return rep
		}
		parts[i].out, _ = strconv.Atoi(outside)
		parts[i].in = 1
		if inside != "1" {
			parts[i].in, _ = strconv.Atoi(inside)
		}
		*steps = append(*steps, Step{"Compute", "Simplify the root", fmt.Sprintf("sqrt(%s) = %s", strconv.FormatFloat(v, 'f', -1, 64), radicalText(parts[i].out, parts[i].in))})
	}
	if parts[1].in == parts[0].in {
		// The same radical on top and bottom cancels.
		parts[0].in, parts[1].in = 1, 1
	}
	if parts[1].in != 1 {
		rep.Err = fmt.Errorf("the denominator sqrt(%s) would need rationalizing, which is not supported", den)
		return rep
	}

	a, b := parts[0].out, parts[1].out
	for g := gcd(a, b); g > 1; g = gcd(a, b) {
		a, b = a/g, b/g
	}
	rep.Result = radicalText(a, parts[0].in)
	if b != 1 {
		rep.Result += "/" + strconv.Itoa(b)
	}
	*steps = append(*steps, Step{"Result", "Quotient", rep.Result})
	return rep
}

// radicalText writes out*sqrt(in) in ASCII, such as 2sqrt(5), sqrt(5), or 4.
func radicalText(out, in int) string {
	switch {
	case in == 1:
		return strconv.Itoa(out)
	case out == 1:
		return fmt.Sprintf("sqrt(%d)", in)
	}
	return fmt.Sprintf("%dsqrt(%d)", out, in)
}

// fractionalExponent finds a power with a whole-number fraction as exponent.
var fractionalExponent = regexp.MustCompile(`\^\(\(?-?\d+\)?/\(?\d+\)?\)`)

// radicalFormPattern reads base^(m/n), allowing TeX's extra parentheses
// around the numerator and denominator.
var groupPattern = regexp.MustCompile(`^\([^()]*\)$`)

var wordPattern = regexp.MustCompile(`^\w+$`)

var radicalFormPattern = regexp.MustCompile(`^(.+)\^\(\(?(-?\d+)\)?/\(?(\d+)\)?\)$`)

// solveRadicalForm rewrites base^(m/n) as the n-th root of base^m, without
// evaluating it: 169^(1/2) is sqrt(169) and 8^(2/3) is sqrt[3](8^2).
func solveRadicalForm(ascii string, steps *[]Step) (rep Report) {
	c := strings.ReplaceAll(ascii, " ", "")
	g := radicalFormPattern.FindStringSubmatch(c)
	if g == nil {
		rep.Err = fmt.Errorf("radical form needs base^(m/n), got %q", ascii)
		return rep
	}
	m, _ := strconv.Atoi(g[2])
	n, _ := strconv.Atoi(g[3])
	if n < 2 || m < 1 {
		rep.Err = fmt.Errorf("radical form needs a positive fraction with a denominator of at least 2, got %d/%d", m, n)
		return rep
	}

	radicand := g[1]
	if m > 1 {
		base := g[1]
		if !wordPattern.MatchString(base) && !groupPattern.MatchString(base) {
			base = "(" + base + ")"
		}
		radicand = base + "^" + strconv.Itoa(m)
	}
	*steps = append(*steps, Step{"Compute", "Read the exponent", fmt.Sprintf("a^(m/n) means the n-th root of a^m, with m = %d and n = %d", m, n)})
	index := ""
	if n != 2 {
		index = strconv.Itoa(n)
	}
	rep.Result = rootText(index, radicand)
	*steps = append(*steps, Step{"Result", "Radical form", rep.Result})
	return rep
}

// powerOfPowerPattern reads (base^(p/q))^r, where r is a whole number or a
// fraction c/d, with TeX's extra parentheses allowed around any exponent.
var powerOfPowerPattern = regexp.MustCompile(`^\((\w+)\^\(\(?(-?\d+)\)?/\(?(\d+)\)?\)\)\^\(?\(?(-?\d+)\)?(?:/\(?(\d+)\)?)?\)?$`)

// solvePowerOfPower applies (a^m)^n = a^(mn) when m is a fraction, which the
// polynomial form cannot hold: (x^(6/7))^7 is x^6. ok is false when ascii
// is not that shape.
func solvePowerOfPower(ascii string, steps *[]Step) (rep Report, ok bool) {
	g := powerOfPowerPattern.FindStringSubmatch(strings.ReplaceAll(ascii, " ", ""))
	if g == nil {
		return rep, false
	}
	p, _ := strconv.Atoi(g[2])
	q, _ := strconv.Atoi(g[3])
	c, _ := strconv.Atoi(g[4])
	d := 1
	if g[5] != "" {
		d, _ = strconv.Atoi(g[5])
	}
	if q == 0 || d == 0 {
		rep.Err = fmt.Errorf("a fraction cannot have a denominator of 0")
		return rep, true
	}

	num, den := p*c, q*d
	if gg := gcd(abs(num), den); gg > 1 {
		num, den = num/gg, den/gg
	}
	*steps = append(*steps, Step{"Compute", "Multiply the exponents", fmt.Sprintf("(%d/%d) * (%d/%d) = %d/%d", p, q, c, d, num, den)})

	base := g[1]
	switch {
	case num == 0:
		rep.Result = "1"
	case den == 1 && num == 1:
		rep.Result = base
	case den == 1 && num > 0:
		rep.Result = fmt.Sprintf("%s^%d", base, num)
	case den == 1:
		rep.Result = fmt.Sprintf("1/%s^%d", base, -num)
		if num == -1 {
			rep.Result = "1/" + base
		}
	default:
		rep.Result = fmt.Sprintf("%s^(%d/%d)", base, num, den)
	}
	*steps = append(*steps, Step{"Result", "Simplified", rep.Result})
	return rep, true
}
