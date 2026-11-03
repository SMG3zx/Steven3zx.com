package main

import (
	"fmt"
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
