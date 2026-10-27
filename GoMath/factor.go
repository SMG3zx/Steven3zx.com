package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PowerFactor is an irreducible factor raised to a power, such as (x - 3)^2.
// Base has a positive leading coefficient.
type PowerFactor struct {
	Base  Poly
	Power int
}

// Factorization is a polynomial written as Content * Monomial * Factors.
type Factorization struct {
	Content  int
	Monomial Monomial
	Factors  []PowerFactor
}

// String writes the factored form, such as "8(2x + 5)" or "2x(x - 4)(x + 4)".
func (f Factorization) String() string {
	var body strings.Builder
	if len(f.Monomial) > 0 {
		body.WriteString(f.Monomial.key())
	}
	for _, pf := range f.Factors {
		fmt.Fprintf(&body, "(%s)", pf.Base)
		if pf.Power > 1 {
			fmt.Fprintf(&body, "^%d", pf.Power)
		}
	}
	if body.Len() == 0 {
		return strconv.Itoa(f.Content)
	}

	switch f.Content {
	case 1:
		return body.String()
	case -1:
		return "-" + body.String()
	default:
		return strconv.Itoa(f.Content) + body.String()
	}
}

// IsPrime reports whether the polynomial is irreducible over the integers.
func (f Factorization) IsPrime() bool {
	return (f.Content == 1 || f.Content == -1) &&
		len(f.Monomial) == 0 &&
		len(f.Factors) == 1 &&
		f.Factors[0].Power == 1
}

// Factor splits p into a content, a monomial, and irreducible factors.
// Factoring works on one variable after the common monomial is removed.
// Rational roots are found with the rational root test, and each root
// divides out as a linear factor. Irreducible factors of degree two or
// more with no rational roots are left as they are. When steps is not nil,
// each technique used is recorded.
func Factor(p Poly, steps *[]Step) (Factorization, error) {
	if len(p) == 0 {
		return Factorization{}, fmt.Errorf("the zero polynomial cannot be factored")
	}

	// Greatest common factor: the integer content and the lowest power of each variable.
	content := 0
	for _, t := range p {
		content = gcd(content, abs(t.Coeff))
	}
	sign := 1
	if p[0].Coeff < 0 {
		sign = -1
	}
	mono := commonMonomial(p)
	fz := Factorization{Content: sign * content, Monomial: mono}

	if content > 1 || len(mono) > 0 {
		addStep(steps, Step{"Compute", "Greatest common factor",
			fmt.Sprintf("pull out %s", describeCommon(fz))})
	} else {
		addStep(steps, Step{"Compute", "Greatest common factor", "none: the only common factor is 1"})
	}

	// Divide the common factor out of every term.
	reduced := make([]Term, 0, len(p))
	for _, t := range p {
		vars := Monomial{}
		for name, exp := range t.Vars {
			if rest := exp - mono[name]; rest > 0 {
				vars[name] = rest
			}
		}
		reduced = append(reduced, Term{Coeff: t.Coeff / (sign * content), Vars: vars})
	}
	q := normalize(reduced)

	name, u, err := q.univariate()
	if err != nil {
		return Factorization{}, fmt.Errorf("cannot factor %s: %v once the common factor is removed", p, err)
	}

	// Pull out linear factors (bx - a) for each rational root a/b.
	var found []PowerFactor
	for len(u) > 1 {
		a, b, ok := findRationalRoot(u)
		if !ok {
			break
		}
		u, _ = divideLinear(u, b, a)
		linear := polyFromCoeffs(name, []int{-a, b})
		found = append(found, PowerFactor{Base: linear, Power: 1})
		addStep(steps, Step{"Compute", "Rational root",
			fmt.Sprintf("%s is a root, so (%s) is a factor", rationalText(a, b), linear)})
	}

	switch {
	case len(u) > 1:
		rest := polyFromCoeffs(name, u)
		found = append(found, PowerFactor{Base: rest, Power: 1})
		addStep(steps, Step{"Compute", "Irreducible factor",
			fmt.Sprintf("(%s) has no rational roots, so it does not factor further over the integers", rest)})
	case len(u) == 1:
		// A leftover constant is ±1 for a primitive polynomial, so it only fixes the sign.
		fz.Content *= u[0]
	}

	fz.Factors = groupFactors(found)
	return fz, nil
}

// findRationalRoot searches the rational root test candidates a/b, where b > 0
// divides the leading coefficient and a divides the constant term.
// u holds the coefficients from the constant term upward.
func findRationalRoot(u []int) (a, b int, ok bool) {
	n := len(u) - 1
	for _, p := range divisors(abs(u[0])) {
		for _, q := range divisors(abs(u[n])) {
			for _, s := range []int{1, -1} {
				cand, den := s*p, q
				if gcd(abs(cand), den) != 1 {
					continue
				}
				if scaledValue(u, cand, den) == 0 {
					return cand, den, true
				}
			}
		}
	}
	return 0, 0, false
}

// scaledValue computes b^n * u(a/b) with integers, so a root test is exact.
func scaledValue(u []int, a, b int) int {
	n := len(u) - 1
	total := 0
	for i, c := range u {
		term := c
		for k := 0; k < i; k++ {
			term *= a
		}
		for k := 0; k < n-i; k++ {
			term *= b
		}
		total += term
	}
	return total
}

// divideLinear divides u by (bx - a). It reports false when the division is not exact.
func divideLinear(u []int, b, a int) ([]int, bool) {
	n := len(u) - 1
	rem := append([]int(nil), u...)
	quot := make([]int, n)
	for k := n; k >= 1; k-- {
		if rem[k]%b != 0 {
			return nil, false
		}
		t := rem[k] / b
		quot[k-1] = t
		rem[k] -= t * b
		rem[k-1] += t * a
	}
	if rem[0] != 0 {
		return nil, false
	}
	return quot, true
}

// commonMonomial returns the lowest power of each variable that appears in every term.
func commonMonomial(p Poly) Monomial {
	mono := Monomial{}
	for _, name := range p.Vars() {
		lowest := -1
		for _, t := range p {
			if exp := t.Vars[name]; lowest < 0 || exp < lowest {
				lowest = exp
			}
		}
		if lowest > 0 {
			mono[name] = lowest
		}
	}
	return mono
}

// groupFactors merges equal factors into powers and sorts them by degree,
// then by constant term, so output is stable.
func groupFactors(found []PowerFactor) []PowerFactor {
	var out []PowerFactor
	index := map[string]int{}
	for _, pf := range found {
		k := pf.Base.String()
		if i, ok := index[k]; ok {
			out[i].Power++
			continue
		}
		index[k] = len(out)
		out = append(out, PowerFactor{Base: pf.Base, Power: 1})
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].Base.Degree(), out[j].Base.Degree()
		if di != dj {
			return di < dj
		}
		return lessByConstant(out[i].Base, out[j].Base)
	})
	return out
}

// lessByConstant orders univariate polynomials by their coefficients from the constant term up.
func lessByConstant(a, b Poly) bool {
	_, ca, _ := a.univariate()
	_, cb, _ := b.univariate()
	for i := 0; i < len(ca) && i < len(cb); i++ {
		if ca[i] != cb[i] {
			return ca[i] < cb[i]
		}
	}
	return len(ca) < len(cb)
}

// Technique names the factoring method that fits the shape of p.
func Technique(p Poly) string {
	if len(p) == 0 {
		return "none"
	}
	content := 0
	for _, t := range p {
		content = gcd(content, abs(t.Coeff))
	}
	if content > 1 || len(commonMonomial(p)) > 0 {
		return "greatest common factor"
	}

	switch len(p) {
	case 2:
		if isTwoSquares(p) {
			return "difference of two squares"
		}
		if isTwoCubes(p) {
			if p[0].Coeff*p[1].Coeff < 0 {
				return "difference of cubes"
			}
			return "sum of two cubes"
		}
	case 3:
		if isPerfectSquareTrinomial(p) {
			return "perfect square trinomial"
		}
		if _, u, err := p.univariate(); err == nil && len(u) == 3 {
			return "trinomial"
		}
	case 4:
		return "factoring by grouping"
	}
	return "rational roots"
}

// isTwoSquares matches a^2 - b^2, where both coefficients and exponents are squares.
func isTwoSquares(p Poly) bool {
	return signsDiffer(p) && allPowers(p, 2)
}

// isTwoCubes matches a^3 + b^3 or a^3 - b^3, where both coefficients and exponents are cubes.
func isTwoCubes(p Poly) bool {
	return allPowers(p, 3)
}

func signsDiffer(p Poly) bool {
	return p[0].Coeff*p[1].Coeff < 0
}

// allPowers reports whether each term is a perfect k-th power: its coefficient
// has an integer k-th root, and every exponent is divisible by k.
func allPowers(p Poly, k int) bool {
	for _, t := range p {
		if _, ok := integerRoot(abs(t.Coeff), k); !ok {
			return false
		}
		for _, exp := range t.Vars {
			if exp%k != 0 {
				return false
			}
		}
	}
	return true
}

// isPerfectSquareTrinomial matches A x^2 + B x + C with A = a^2, C = c^2, and B = ±2ac.
func isPerfectSquareTrinomial(p Poly) bool {
	_, u, err := p.univariate()
	if err != nil || len(u) != 3 {
		return false
	}
	c, b, a := u[0], u[1], u[2]
	if c <= 0 || a <= 0 {
		return false
	}
	ra, okA := integerRoot(a, 2)
	rc, okC := integerRoot(c, 2)
	return okA && okC && (b == 2*ra*rc || b == -2*ra*rc)
}

// integerRoot returns r >= 0 with r^k == n, if one exists.
func integerRoot(n, k int) (int, bool) {
	for r := 0; ; r++ {
		p := 1
		for i := 0; i < k; i++ {
			p *= r
		}
		if p == n {
			return r, true
		}
		if p > n {
			return 0, false
		}
	}
}

// rationalText writes a/b as a whole number when b is 1.
func rationalText(a, b int) string {
	if b == 1 {
		return strconv.Itoa(a)
	}
	return fmt.Sprintf("%d/%d", a, b)
}

func describeCommon(fz Factorization) string {
	var parts []string
	if fz.Content > 1 {
		parts = append(parts, strconv.Itoa(fz.Content))
	}
	if len(fz.Monomial) > 0 {
		parts = append(parts, fz.Monomial.key())
	}
	return strings.Join(parts, " and ")
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func divisors(n int) []int {
	var out []int
	for d := 1; d <= n; d++ {
		if n%d == 0 {
			out = append(out, d)
		}
	}
	return out
}
