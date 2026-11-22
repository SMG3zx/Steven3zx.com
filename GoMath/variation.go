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

// Variation input has three parts separated by semicolons:
//
//	y varies directly as x and inversely as z^2; y = 10 when x = 36 and z = 6; find y when x = 2 and z = 8
//
// The relation says which variables go in the numerator (directly, jointly)
// and which go in the denominator (inversely). The second part gives the known
// values that find the constant of variation. The third part names the unknown
// and gives the other values it needs. The unknown may be any variable of the
// relation, including the first one.
var (
	variationPattern = regexp.MustCompile(`^\s*([A-Za-z])\s+varies\s+(.+)$`)
	variableTerm     = regexp.MustCompile(`^([A-Za-z])(?:\^(\d+))?$`)
	assignPattern    = regexp.MustCompile(`([A-Za-z])\s*=\s*(-?\d+(?:\.\d+)?(?:/\d+)?)`)
	findPattern      = regexp.MustCompile(`find\s+([A-Za-z])\b`)
)

// solveVariation finds the value of one variable in a direct, inverse, or
// joint variation, after finding the constant of variation from the given values.
func solveVariation(ascii string, steps *[]Step) (rep Report) {
	parts := strings.Split(ascii, ";")
	if len(parts) != 3 {
		rep.Err = fmt.Errorf(`variation needs three parts separated by ";": the relation, the known values, and the question, got %q`, ascii)
		return rep
	}

	m := variationPattern.FindStringSubmatch(parts[0])
	if m == nil {
		rep.Err = fmt.Errorf(`the relation should read like "y varies directly as x", got %q`, strings.TrimSpace(parts[0]))
		return rep
	}
	out := m[1]
	exps, err := variationExponents(m[2])
	if err != nil {
		rep.Err = err
		return rep
	}
	*steps = append(*steps, Step{"Compute", "Relation", fmt.Sprintf("%s = k * %s", out, exponentText(exps))})

	given, err := assignments(parts[1])
	if err != nil {
		rep.Err = err
		return rep
	}
	k, err := constantOfVariation(out, exps, given)
	if err != nil {
		rep.Err = err
		return rep
	}
	*steps = append(*steps, Step{"Compute", "Constant of variation", "k = " + ratText(k)})
	*steps = append(*steps, Step{"Compute", "Equation", formulaText(out, k, exps)})

	target := findPattern.FindStringSubmatch(parts[2])
	if target == nil {
		rep.Err = fmt.Errorf(`the question should read like "find y when x = 18", got %q`, strings.TrimSpace(parts[2]))
		return rep
	}
	unknown := target[1]
	asked, err := assignments(parts[2])
	if err != nil {
		rep.Err = err
		return rep
	}

	var value *big.Rat
	if unknown == out {
		value, err = productOf(k, exps, asked)
	} else {
		value, err = unknownInput(k, out, unknown, exps, asked, steps)
	}
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Result = ratText(value)
	*steps = append(*steps, Step{"Result", "Value of " + unknown, rep.Result})
	return rep
}

// variationExponents reads the words after "varies". Directly and jointly put
// the variable on top, and inversely puts it on the bottom. "z^2" squares it.
func variationExponents(text string) (map[string]int, error) {
	exps := map[string]int{}
	sign := 0
	for _, w := range strings.Fields(strings.ReplaceAll(text, ",", " ")) {
		switch strings.ToLower(w) {
		case "directly", "jointly":
			sign = 1
		case "inversely":
			sign = -1
		case "as", "and":
		default:
			tm := variableTerm.FindStringSubmatch(w)
			if tm == nil {
				return nil, fmt.Errorf("the relation has an unexpected word %q; use directly, inversely, or jointly, then a variable such as x or z^2", w)
			}
			if sign == 0 {
				return nil, fmt.Errorf("say directly, inversely, or jointly before %q", w)
			}
			power := 1
			if tm[2] != "" {
				power, _ = strconv.Atoi(tm[2])
			}
			exps[tm[1]] += sign * power
		}
	}
	if len(exps) == 0 {
		return nil, fmt.Errorf("the relation names no variable")
	}
	return exps, nil
}

// assignments reads "x = 7 and z = 6" into a value for each variable.
func assignments(text string) (map[string]*big.Rat, error) {
	vals := map[string]*big.Rat{}
	for _, m := range assignPattern.FindAllStringSubmatch(text, -1) {
		r, ok := new(big.Rat).SetString(m[2])
		if !ok {
			return nil, fmt.Errorf("%q is not a number", m[2])
		}
		vals[m[1]] = r
	}
	return vals, nil
}

// constantOfVariation returns k from out = k * (variables), using the given values.
func constantOfVariation(out string, exps map[string]int, given map[string]*big.Rat) (*big.Rat, error) {
	y, ok := given[out]
	if !ok {
		return nil, fmt.Errorf("the known values need a value for %s", out)
	}
	prod, err := productOf(big.NewRat(1, 1), exps, given)
	if err != nil {
		return nil, err
	}
	if prod.Sign() == 0 {
		return nil, fmt.Errorf("a known value is zero, so the constant cannot be found")
	}
	return new(big.Rat).Quo(y, prod), nil
}

// productOf returns k times each variable raised to its exponent.
func productOf(k *big.Rat, exps map[string]int, vals map[string]*big.Rat) (*big.Rat, error) {
	result := new(big.Rat).Set(k)
	for _, v := range sortedNames(exps) {
		val, ok := vals[v]
		if !ok {
			return nil, fmt.Errorf("the question needs a value for %s", v)
		}
		p, err := ratPow(val, exps[v])
		if err != nil {
			return nil, err
		}
		result.Mul(result, p)
	}
	return result, nil
}

// unknownInput solves out = k * (variables) for the input variable unknown.
// It raises unknown to its exponent e, so unknown^e = out / (k * the others),
// then takes the |e|-th root, which must be exact.
func unknownInput(k *big.Rat, out, unknown string, exps map[string]int, asked map[string]*big.Rat, steps *[]Step) (*big.Rat, error) {
	e, ok := exps[unknown]
	if !ok {
		return nil, fmt.Errorf("%s is not in the relation", unknown)
	}
	y, ok := asked[out]
	if !ok {
		return nil, fmt.Errorf("finding %s needs the value of %s", unknown, out)
	}
	others := map[string]int{}
	for v, ev := range exps {
		if v != unknown {
			others[v] = ev
		}
	}
	rest, err := productOf(k, others, asked)
	if err != nil {
		return nil, err
	}
	if rest.Sign() == 0 {
		return nil, fmt.Errorf("the other values make the right side zero")
	}
	// unknown^e = y / rest. Turn a negative exponent into a positive one first.
	target := new(big.Rat).Quo(y, rest)
	n := e
	if e < 0 {
		target.Inv(target)
		n = -e
	}
	*steps = append(*steps, Step{"Compute", "Solve for " + unknown, fmt.Sprintf("%s^%d = %s", unknown, n, ratText(target))})
	value, ok := exactRoot(target, n)
	if !ok {
		return nil, fmt.Errorf("%s is not an exact %s root of %s, so the answer is not a rational number", unknown, ordinal(n), ratText(target))
	}
	return value, nil
}

// ratPow raises r to the power e, which may be negative.
func ratPow(r *big.Rat, e int) (*big.Rat, error) {
	base := new(big.Rat).Set(r)
	if e < 0 {
		if base.Sign() == 0 {
			return nil, fmt.Errorf("a value of zero cannot be raised to a negative power")
		}
		base.Inv(base)
		e = -e
	}
	result := big.NewRat(1, 1)
	for i := 0; i < e; i++ {
		result.Mul(result, base)
	}
	return result, nil
}

// exactRoot returns the n-th root of r when both its numerator and denominator
// have exact integer roots.
func exactRoot(r *big.Rat, n int) (*big.Rat, bool) {
	if r.Sign() < 0 && n%2 == 0 {
		return nil, false
	}
	num, ok1 := intRoot(new(big.Int).Abs(r.Num()), n)
	den, ok2 := intRoot(new(big.Int).Set(r.Denom()), n)
	if !ok1 || !ok2 {
		return nil, false
	}
	result := new(big.Rat).SetFrac(num, den)
	if r.Sign() < 0 {
		result.Neg(result)
	}
	return result, true
}

// intRoot returns the exact n-th root of x, if there is one.
func intRoot(x *big.Int, n int) (*big.Int, bool) {
	f, _ := new(big.Float).SetInt(x).Float64()
	guess := int64(math.Round(math.Pow(f, 1/float64(n))))
	for g := guess - 1; g <= guess+1; g++ {
		if g < 0 {
			continue
		}
		cand := big.NewInt(g)
		if new(big.Int).Exp(cand, big.NewInt(int64(n)), nil).Cmp(x) == 0 {
			return cand, true
		}
	}
	return nil, false
}

func sortedNames(exps map[string]int) []string {
	names := make([]string, 0, len(exps))
	for v := range exps {
		names = append(names, v)
	}
	sort.Strings(names)
	return names
}

// exponentText writes the variables of a relation, such as "x z^2 / ..." with
// the inverse ones after a slash.
func exponentText(exps map[string]int) string {
	num, den := factorsText(exps)
	if den == "" {
		return num
	}
	return num + " / " + den
}

// formulaText writes the equation with the constant, such as "y = 10x/z^2".
func formulaText(out string, k *big.Rat, exps map[string]int) string {
	num, den := factorsText(exps)
	var kText string
	switch {
	case k.IsInt() && k.Num().Int64() == 1:
	case k.IsInt():
		kText = k.Num().String()
	default:
		kText = "(" + k.RatString() + ")"
	}
	body := kText + num
	if body == "" {
		body = "1"
	}
	if den != "" {
		if strings.Contains(den, " ") {
			den = "(" + den + ")"
		}
		body += "/" + den
	}
	return out + " = " + body
}

// factorsText writes the variables with positive exponents and those with
// negative exponents, each as a run of powers such as "xz^2".
func factorsText(exps map[string]int) (num, den string) {
	var nb, db strings.Builder
	for _, v := range sortedNames(exps) {
		e := exps[v]
		switch {
		case e > 0:
			nb.WriteString(powerText(v, e))
		case e < 0:
			db.WriteString(powerText(v, -e))
		}
	}
	return nb.String(), db.String()
}

func powerText(v string, e int) string {
	if e == 1 {
		return v
	}
	return v + "^" + strconv.Itoa(e)
}

// ratText writes a whole number as itself and a fraction as p/q.
func ratText(r *big.Rat) string {
	return r.RatString()
}

func ordinal(n int) string {
	switch n {
	case 1:
		return "first"
	case 2:
		return "second"
	case 3:
		return "third"
	}
	return strconv.Itoa(n) + "th"
}
