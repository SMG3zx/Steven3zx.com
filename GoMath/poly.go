package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Monomial maps each variable to a whole-number exponent, e.g. {"x": 2, "y": 1} for x^2 y.
// Exponents may be negative, as in x^-6 y, until String writes them with positive exponents.
type Monomial map[string]int

// key is the canonical text of the monomial, with variables sorted by name: "x^2y".
// It is also used to combine like terms.
func (m Monomial) key() string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		sb.WriteString(name)
		if m[name] != 1 {
			fmt.Fprintf(&sb, "^%d", m[name])
		}
	}
	return sb.String()
}

func (m Monomial) degree() int {
	total := 0
	for _, exp := range m {
		total += exp
	}
	return total
}

// Term is a coefficient times a monomial, such as -3x^2y.
type Term struct {
	Coeff int
	Vars  Monomial
}

// Poly is a sum of terms with integer coefficients. Values are kept normalized:
// like terms are combined, zero terms are dropped, and terms are sorted by
// degree (highest first), then by variable text.
type Poly []Term

// normalize combines like terms, drops zeros, and sorts the result.
func normalize(terms []Term) Poly {
	coeffs := map[string]int{}
	vars := map[string]Monomial{}
	for _, t := range terms {
		k := t.Vars.key()
		coeffs[k] += t.Coeff
		vars[k] = t.Vars
	}

	var out Poly
	for k, c := range coeffs {
		if c != 0 {
			out = append(out, Term{Coeff: c, Vars: vars[k]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].Vars.degree(), out[j].Vars.degree()
		if di != dj {
			return di > dj
		}
		return out[i].Vars.key() < out[j].Vars.key()
	})
	return out
}

func constPoly(c int) Poly {
	return normalize([]Term{{Coeff: c, Vars: Monomial{}}})
}

func varPoly(name string) Poly {
	return Poly{{Coeff: 1, Vars: Monomial{name: 1}}}
}

func (p Poly) Add(q Poly) Poly {
	return normalize(append(append([]Term{}, p...), q...))
}

func (p Poly) Neg() Poly {
	terms := make([]Term, len(p))
	for i, t := range p {
		terms[i] = Term{Coeff: -t.Coeff, Vars: t.Vars}
	}
	return normalize(terms)
}

func (p Poly) Sub(q Poly) Poly {
	return p.Add(q.Neg())
}

func (p Poly) Mul(q Poly) Poly {
	var terms []Term
	for _, a := range p {
		for _, b := range q {
			vars := Monomial{}
			for name, exp := range a.Vars {
				vars[name] += exp
			}
			for name, exp := range b.Vars {
				vars[name] += exp
			}
			// x times x^-1 cancels, so drop the zero exponent.
			for name, exp := range vars {
				if exp == 0 {
					delete(vars, name)
				}
			}
			terms = append(terms, Term{Coeff: a.Coeff * b.Coeff, Vars: vars})
		}
	}
	return normalize(terms)
}

// Pow raises p to a whole-number power. Anything to the 0 is 1.
func (p Poly) Pow(n int) Poly {
	result := constPoly(1)
	for i := 0; i < n; i++ {
		result = result.Mul(p)
	}
	return result
}

// Degree is the highest total degree of any term. The zero polynomial has degree 0.
func (p Poly) Degree() int {
	max := 0
	for _, t := range p {
		if d := t.Vars.degree(); d > max {
			max = d
		}
	}
	return max
}

// Constant returns the value when p has no variables.
func (p Poly) Constant() (int, bool) {
	switch len(p) {
	case 0:
		return 0, true
	case 1:
		if len(p[0].Vars) == 0 {
			return p[0].Coeff, true
		}
	}
	return 0, false
}

// Vars returns the variable names in p, sorted.
func (p Poly) Vars() []string {
	seen := map[string]bool{}
	for _, t := range p {
		for name := range t.Vars {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// univariate returns the single variable name in p and its coefficients from
// the constant term upward, so coeffs[i] is the coefficient of name^i.
// It fails when p contains more than one variable.
func (p Poly) univariate() (string, []int, error) {
	vars := p.Vars()
	if len(vars) > 1 {
		return "", nil, fmt.Errorf("expected one variable, found %s", strings.Join(vars, ", "))
	}
	name := ""
	if len(vars) == 1 {
		name = vars[0]
	}
	if len(p) == 0 {
		return name, nil, nil
	}

	coeffs := make([]int, p.Degree()+1)
	for _, t := range p {
		coeffs[t.Vars[name]] += t.Coeff
	}
	return name, coeffs, nil
}

// polyFromCoeffs builds a univariate polynomial from coefficients in the
// same order univariate returns them.
func polyFromCoeffs(name string, coeffs []int) Poly {
	var terms []Term
	for exp, c := range coeffs {
		if c == 0 {
			continue
		}
		vars := Monomial{}
		if exp > 0 {
			vars[name] = exp
		}
		terms = append(terms, Term{Coeff: c, Vars: vars})
	}
	return normalize(terms)
}

// hasNegativeExponent reports whether any variable in p has a negative exponent.
func (p Poly) hasNegativeExponent() bool {
	for _, t := range p {
		for _, exp := range t.Vars {
			if exp < 0 {
				return true
			}
		}
	}
	return false
}

// positive rewrites p so every exponent is positive: p = num / den. The
// denominator takes the largest negative power of each variable, and the
// numerator is multiplied up to match. Without negative exponents, num is p
// and den is empty.
func (p Poly) positive() (Poly, Monomial) {
	den := Monomial{}
	for _, t := range p {
		for name, exp := range t.Vars {
			if exp < 0 && -exp > den[name] {
				den[name] = -exp
			}
		}
	}
	if len(den) == 0 {
		return p, den
	}

	terms := make([]Term, len(p))
	for i, t := range p {
		vars := Monomial{}
		for name, exp := range t.Vars {
			vars[name] = exp
		}
		for name, d := range den {
			vars[name] += d
			if vars[name] == 0 {
				delete(vars, name)
			}
		}
		terms[i] = Term{Coeff: t.Coeff, Vars: vars}
	}
	return normalize(terms), den
}

// inverse returns 1/p. Only a single term with coefficient 1 or -1 has a
// polynomial inverse, such as x^3 or -y, so anything else is an error.
func (p Poly) inverse() (Poly, error) {
	if len(p) != 1 || (p[0].Coeff != 1 && p[0].Coeff != -1) {
		return nil, fmt.Errorf("can only divide by a single variable term, got %s", p)
	}
	vars := Monomial{}
	for name, exp := range p[0].Vars {
		vars[name] = -exp
	}
	return Poly{{Coeff: p[0].Coeff, Vars: vars}}, nil
}

// String writes p in standard form, such as "3x^3 + 8x^2 - 10x". When p has
// negative exponents it writes a fraction with positive exponents, such as
// "y/x^6" or "(2x + 1)/x".
func (p Poly) String() string {
	num, den := p.positive()
	if len(den) == 0 {
		return num.terms()
	}
	text := num.terms()
	if len(num) > 1 {
		text = "(" + text + ")"
	}
	return text + "/" + den.key()
}

// terms writes each term in order, with signs, such as "3x^3 + 8x^2 - 10x".
func (p Poly) terms() string {
	if len(p) == 0 {
		return "0"
	}

	var sb strings.Builder
	for i, t := range p {
		c := t.Coeff
		switch {
		case i == 0 && c < 0:
			sb.WriteString("-")
		case i > 0 && c < 0:
			sb.WriteString(" - ")
		case i > 0:
			sb.WriteString(" + ")
		}

		abs := c
		if abs < 0 {
			abs = -abs
		}
		mono := t.Vars.key()
		if mono == "" || abs != 1 {
			sb.WriteString(strconv.Itoa(abs))
		}
		sb.WriteString(mono)
	}
	return sb.String()
}

// ToPoly converts a parsed expression into a polynomial. It fails for
// anything that is not a polynomial, such as a variable exponent or a divisor
// that is not a single variable term.
func ToPoly(e SExpression) (Poly, error) {
	return toPoly(e, nil)
}

// toPoly evaluates e bottom-up. When steps is not nil, each operation is recorded.
func toPoly(e SExpression, steps *[]Step) (Poly, error) {
	switch n := e.(type) {
	case SAtom:
		if unicode.IsDigit(rune(n.Value[0])) {
			v, err := strconv.Atoi(n.Value)
			if err != nil {
				return nil, fmt.Errorf("%q is not a whole-number coefficient", n.Value)
			}
			return constPoly(v), nil
		}
		return varPoly(n.Value), nil

	case Cons:
		if len(n.Rest) == 1 {
			x, err := toPoly(n.Rest[0], steps)
			if err != nil {
				return nil, err
			}
			res := x.Neg()
			addStep(steps, Step{"Compute", "Negate", fmt.Sprintf("-(%s) = %s", x, res)})
			return res, nil
		}

		left, err := toPoly(n.Rest[0], steps)
		if err != nil {
			return nil, err
		}
		right, err := toPoly(n.Rest[1], steps)
		if err != nil {
			return nil, err
		}

		switch n.Head {
		case '+':
			res := left.Add(right)
			addStep(steps, Step{"Compute", "Add", fmt.Sprintf("(%s) + (%s) = %s", left, right, res)})
			return res, nil
		case '-':
			res := left.Sub(right)
			addStep(steps, Step{"Compute", "Subtract", fmt.Sprintf("(%s) - (%s) = %s", left, right, res)})
			return res, nil
		case '*':
			res := left.Mul(right)
			addStep(steps, Step{"Compute", "Multiply", fmt.Sprintf("(%s)(%s) = %s", left, right, res)})
			return res, nil
		case '/':
			inv, err := right.inverse()
			if err != nil {
				return nil, err
			}
			res := left.Mul(inv)
			addStep(steps, Step{"Compute", "Divide", fmt.Sprintf("(%s) / (%s) = %s", left, right, res)})
			return res, nil
		case '^':
			exp, ok := right.Constant()
			if !ok {
				return nil, fmt.Errorf("exponent must be a whole number, got %s", right)
			}
			base := left
			if exp < 0 {
				inv, err := left.inverse()
				if err != nil {
					return nil, err
				}
				base, exp = inv, -exp
			}
			res := base.Pow(exp)
			addStep(steps, Step{"Compute", "Raise to a power", fmt.Sprintf("(%s)^%d = %s", left, exp, res)})
			return res, nil
		}
		return nil, fmt.Errorf("%c is not a polynomial operation", n.Head)
	}
	return nil, fmt.Errorf("unsupported expression %s", e)
}

func addStep(steps *[]Step, s Step) {
	if steps != nil {
		*steps = append(*steps, s)
	}
}
