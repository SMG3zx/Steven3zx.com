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
	quotientPattern = regexp.MustCompile(`sqrt\(([^()]*)\)\s*/\s*sqrt\(([^()]*)\)`)
	productPattern  = regexp.MustCompile(`sqrt\(([^()]*)\)\s*\*?\s*sqrt\(([^()]*)\)`)
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
	for productPattern.MatchString(out) {
		out = productPattern.ReplaceAllStringFunc(out, func(m string) string {
			g := productPattern.FindStringSubmatch(m)
			joined := "sqrt(" + wrapSum(g[1]) + "*" + wrapSum(g[2]) + ")"
			notes = append(notes, fmt.Sprintf("product rule: %s = %s", m, joined))
			return joined
		})
	}

	out = quotientPattern.ReplaceAllStringFunc(out, func(m string) string {
		g := quotientPattern.FindStringSubmatch(m)
		q, ok := divideTerms(g[1], g[2])
		if !ok {
			return m
		}
		joined := "sqrt(" + q + ")"
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

// divideTerms divides one single-term expression by another, such as
// 147x^3 by 3x giving 49x^2. ok is false unless the division is exact.
func divideTerms(a, b string) (string, bool) {
	parse := func(r string) (Term, bool) {
		tree, _, err := ParseWithEvents(r)
		if err != nil {
			return Term{}, false
		}
		p, err := toPoly(tree, &[]Step{})
		if err != nil || len(p) != 1 || p.hasNegativeExponent() {
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
	if len(poly) != 1 || poly[0].Coeff < 0 || poly.hasNegativeExponent() {
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
