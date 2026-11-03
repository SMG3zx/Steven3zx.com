package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var radicalPattern = regexp.MustCompile(`sqrt\((\d+)\)`)

// radicalSubst rewrites each sqrt(n) in ascii as a stand-in variable so the
// polynomial engine can combine like radicals: 7sqrt(2) + 11sqrt(2) becomes
// 7a + 11a. The largest square factor is pulled out first, so sqrt(8) becomes
// 2a with a standing for sqrt(2). Perfect squares become whole numbers.
// back maps each stand-in letter to the radical it replaced. When ascii has
// no sqrt(n), it returns ascii unchanged with a nil map.
func radicalSubst(ascii string) (out string, back map[string]string, notes []string, err error) {
	if !radicalPattern.MatchString(ascii) {
		return ascii, nil, nil, nil
	}
	back = map[string]string{}
	letterOf := map[int]string{}
	free := []rune("abcdefghijklmnopqrstuvwxyz")
	var isFree = func(r rune) bool { return !strings.ContainsRune(ascii, r) }

	out = radicalPattern.ReplaceAllStringFunc(ascii, func(m string) string {
		if err != nil {
			return m
		}
		n, _ := strconv.Atoi(radicalPattern.FindStringSubmatch(m)[1])
		outside, inside := 1, n
		for f := 2; f*f <= inside; {
			if inside%(f*f) == 0 {
				inside /= f * f
				outside *= f
			} else {
				f++
			}
		}
		var rep string
		if inside == 1 {
			rep = strconv.Itoa(outside)
		} else {
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
				back[l] = fmt.Sprintf("sqrt(%d)", inside)
			}
			rep = l
			if outside > 1 {
				rep = strconv.Itoa(outside) + l
			}
		}
		notes = append(notes, fmt.Sprintf("%s = %s", m, rep))
		return "(" + rep + ")"
	})
	return out, back, notes, err
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
