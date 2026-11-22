package main

import (
	"fmt"
	"sort"
)

// solveByRationalRoots solves a polynomial of degree 3 or more. coeffs[i] is
// the coefficient of name^i. It takes out zero roots, then finds rational roots
// one at a time with the rational root theorem, dividing each one out. When a
// quadratic or lower is left, solvePoly finishes it. A leftover quadratic with
// no real roots adds nothing to the set. A leftover of degree 3 or more with no
// rational root is reported as unsupported.
func solveByRationalRoots(name string, coeffs []int, steps *[]Step) ([]root, string, error) {
	c := append([]int(nil), coeffs...)
	var found []root

	for len(c) > 1 && c[0] == 0 {
		found = append(found, makeRoot(0, 1))
		*steps = append(*steps, Step{"Compute", "Zero root", name + " = 0"})
		c = c[1:]
	}

	for len(c) > 3 {
		p, q, ok := rationalRootOf(c)
		if !ok {
			return nil, "", fmt.Errorf("solve found no rational root of the degree %d polynomial, which is not supported", len(c)-1)
		}
		found = append(found, makeRoot(p, q))
		*steps = append(*steps, Step{"Compute", "Rational root", fmt.Sprintf("%s = %s", name, fraction(p, q))})
		c = deflate(c, p, q)
	}

	rest, special, err := solvePoly(polyFromCoeffs(name, c), steps)
	if err != nil {
		return nil, "", err
	}
	if special == "no solution" || special == "no real solution" {
		// What is left has no real roots, so the solution set is just found.
		rest, special = nil, ""
	}
	if special != "" {
		return nil, special, nil
	}

	all := append(found, rest...)
	sort.Slice(all, func(i, j int) bool { return all[i].val < all[j].val })
	var set []root
	seen := map[string]bool{}
	for _, r := range all {
		if !seen[r.text] {
			seen[r.text] = true
			set = append(set, r)
		}
	}
	return set, "", nil
}

// makeRoot is the root p/q, with its exact text and its value.
func makeRoot(p, q int) root {
	return root{fraction(p, q), float64(p) / float64(q)}
}

// rationalRootOf finds a rational root p/q of the integer polynomial c, where
// c[0] is not zero. p divides the constant term and q divides the leading
// coefficient, so only those pairs are tried.
func rationalRootOf(c []int) (p, q int, ok bool) {
	n := len(c) - 1
	for _, qd := range divisors(abs(c[n])) {
		for _, pd := range divisors(abs(c[0])) {
			for _, pv := range []int{pd, -pd} {
				if gcd(abs(pv), qd) != 1 {
					continue
				}
				if homogeneousValue(c, pv, qd) == 0 {
					return pv, qd, true
				}
			}
		}
	}
	return 0, 0, false
}

// homogeneousValue is q^n times c(p/q), which is zero exactly when p/q is a root.
func homogeneousValue(c []int, p, q int) int {
	n := len(c) - 1
	total := 0
	for i, coeff := range c {
		term := coeff
		for k := 0; k < i; k++ {
			term *= p
		}
		for k := 0; k < n-i; k++ {
			term *= q
		}
		total += term
	}
	return total
}

// deflate divides c by (q x - p), which is exact because p/q is a root. The
// quotient keeps whole-number coefficients by the rational root theorem.
func deflate(c []int, p, q int) []int {
	n := len(c) - 1
	rem := append([]int(nil), c...)
	quot := make([]int, n)
	for i := n; i >= 1; i-- {
		quot[i-1] = rem[i] / q
		rem[i] = 0
		rem[i-1] += p * quot[i-1]
	}
	return quot
}
