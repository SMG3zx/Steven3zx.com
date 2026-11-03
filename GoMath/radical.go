package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	radicalPattern = regexp.MustCompile(`sqrt\(([^()]*)\)`)
	productPattern = regexp.MustCompile(`sqrt\(([^()]*)\)\s*\*?\s*sqrt\(([^()]*)\)`)
)

// radicalSubst rewrites each sqrt(R) in ascii as a stand-in variable so the
// polynomial engine can combine like radicals: 7sqrt(2) + 11sqrt(2) becomes
// 7a + 11a. First the product rule joins neighbours, so sqrt(6x)*sqrt(30x)
// becomes sqrt(6x*30x). R must then be a single term. Its largest square
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

	back = map[string]string{}
	letterOf := map[string]string{}
	// Late letters sort after most variables, so radicals print last: 6xsqrt(5).
	free := []rune("zyxwvutsrqponmlkjihgfedcba")
	isFree := func(r rune) bool { return !strings.ContainsRune(ascii, r) }

	out = radicalPattern.ReplaceAllStringFunc(out, func(m string) string {
		if err != nil {
			return m
		}
		inner := radicalPattern.FindStringSubmatch(m)[1]
		outside, inside, e := splitRadicand(inner)
		if e != nil {
			err = e
			return m
		}
		rep := outside
		if inside != "1" {
			l, ok := letterOf[inside]
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
				letterOf[inside] = l
				back[l] = "sqrt(" + inside + ")"
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

// wrapSum puts parentheses around s when it holds a sum or difference.
func wrapSum(s string) string {
	if strings.ContainsAny(s, "+-") {
		return "(" + s + ")"
	}
	return s
}

// splitRadicand writes the term under a root as outside times sqrt(inside),
// both as ASCII. A coefficient pulls out its largest square factor and each
// variable pulls out half of its exponent, rounded down. outside is "1" and
// inside is "1" when nothing is left on that side.
func splitRadicand(r string) (outside, inside string, err error) {
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
	for f := 2; f*f <= in; {
		if in%(f*f) == 0 {
			in /= f * f
			out *= f
		} else {
			f++
		}
	}
	outVars, inVars := Monomial{}, Monomial{}
	for name, e := range t.Vars {
		if e/2 > 0 {
			outVars[name] = e / 2
		}
		if e%2 == 1 {
			inVars[name] = 1
		}
	}

	outside = strconv.Itoa(out) + outVars.key()
	if outside == "1" || (out == 1 && len(outVars) > 0) {
		outside = strings.TrimPrefix(outside, "1")
		if outside == "" {
			outside = "1"
		}
	}
	inside = strconv.Itoa(in) + inVars.key()
	if in == 1 && len(inVars) > 0 {
		inside = inVars.key()
	}
	return outside, inside, nil
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
