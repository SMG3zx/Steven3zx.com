package main

import "fmt"

// simplifyRational reduces num/den by cancelling the factors they share. Both
// sides are factored, then the common content, common variable powers and
// common irreducible factors are divided out. The answer is the reduced
// fraction in factored form, such as 3/(x - 2).
func simplifyRational(num, den Poly, steps *[]Step) (string, error) {
	if len(den) == 0 {
		return "", fmt.Errorf("cannot divide by zero")
	}
	if len(num) == 0 {
		return "0", nil
	}
	for _, p := range []Poly{num, den} {
		if err := p.requireWhole(); err != nil {
			return "", err
		}
		if p.hasNegativeExponent() {
			return "", fmt.Errorf("a rational expression here must have polynomial parts, got %s", p)
		}
	}

	nf, err := Factor(num, steps)
	if err != nil {
		return "", err
	}
	df, err := Factor(den, steps)
	if err != nil {
		return "", err
	}
	*steps = append(*steps, Step{"Compute", "Factor the numerator and denominator", fmt.Sprintf("(%s) / (%s)", nf, df)})

	// Content: reduce the two whole numbers, keeping the sign on top.
	nc, dc := nf.Content, df.Content
	if dc < 0 {
		nc, dc = -nc, -dc
	}
	if g := gcd(abs(nc), dc); g > 1 {
		nc, dc = nc/g, dc/g
	}

	// Variable powers: cancel the smaller exponent of each variable.
	nm, dm := Monomial{}, Monomial{}
	for name, e := range nf.Monomial {
		nm[name] = e
	}
	for name, e := range df.Monomial {
		dm[name] = e
	}
	for name := range nm {
		if m := min(nm[name], dm[name]); m > 0 {
			nm[name] -= m
			dm[name] -= m
		}
	}
	for _, m := range []Monomial{nm, dm} {
		for name, e := range m {
			if e == 0 {
				delete(m, name)
			}
		}
	}

	// Irreducible factors: cancel the smaller power of each shared base.
	denPower := map[string]int{}
	for _, f := range df.Factors {
		denPower[f.Base.String()] = f.Power
	}
	var nfs, dfs []PowerFactor
	for _, f := range nf.Factors {
		key := f.Base.String()
		m := min(f.Power, denPower[key])
		if m > 0 {
			denPower[key] -= m
		}
		if f.Power > m {
			nfs = append(nfs, PowerFactor{f.Base, f.Power - m})
		}
	}
	for _, f := range df.Factors {
		if p := denPower[f.Base.String()]; p > 0 {
			dfs = append(dfs, PowerFactor{f.Base, p})
		}
	}

	top := Factorization{Content: nc, Monomial: nm, Factors: nfs}
	bottom := Factorization{Content: dc, Monomial: dm, Factors: dfs}
	if dc == 1 && len(dm) == 0 && len(dfs) == 0 {
		// A lone factor such as (x + 1) needs no parentheses by itself.
		if nc == 1 && len(nm) == 0 && len(nfs) == 1 && nfs[0].Power == 1 {
			return nfs[0].Base.String(), nil
		}
		return top.String(), nil
	}
	return top.String() + "/" + wrapDenominator(bottom), nil
}

// wrapDenominator writes f, in parentheses unless it is a single number or a
// single variable power, so y/(2x) and 3/(x - 2) are not misread.
func wrapDenominator(f Factorization) string {
	text := f.String()
	if f.Content == 1 && len(f.Factors) == 0 && len(f.Monomial) == 1 {
		return text
	}
	// A single factor already carries its own parentheses.
	if f.Content == 1 && len(f.Monomial) == 0 && len(f.Factors) == 1 {
		return text
	}
	if len(f.Monomial) == 0 && len(f.Factors) == 0 {
		return text
	}
	return "(" + text + ")"
}

// rationalParts reads e as numerator/denominator polynomials. Products,
// quotients and whole-number powers of fractions combine; a sum of fractions
// does not, and reports ok false.
func rationalParts(e SExpression) (num, den Poly, ok bool) {
	c, isCons := e.(Cons)
	if !isCons || len(c.Rest) != 2 {
		if p, err := toPoly(e, nil); err == nil {
			return p, constPoly(1), true
		}
		return nil, nil, false
	}
	switch c.Head {
	case '*', '/':
		an, ad, ok1 := rationalParts(c.Rest[0])
		bn, bd, ok2 := rationalParts(c.Rest[1])
		if !ok1 || !ok2 {
			return nil, nil, false
		}
		if c.Head == '*' {
			return an.Mul(bn), ad.Mul(bd), true
		}
		return an.Mul(bd), ad.Mul(bn), true
	case '^':
		exp, err := toPoly(c.Rest[1], nil)
		n, isConst := exp.Constant()
		if err != nil || !isConst || n < 0 {
			return nil, nil, false
		}
		bn, bd, ok1 := rationalParts(c.Rest[0])
		if !ok1 {
			return nil, nil, false
		}
		return bn.Pow(n), bd.Pow(n), true
	}
	if p, err := toPoly(e, nil); err == nil {
		return p, constPoly(1), true
	}
	return nil, nil, false
}
