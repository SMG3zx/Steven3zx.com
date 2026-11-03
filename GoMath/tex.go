package main

import "strings"

// normalizeTeX rewrites the LaTeX/TeX forms we accept into the ASCII syntax
// the parser reads. It returns the rewritten text and one note per kind of
// rewrite it made, in the order they were applied.
func normalizeTeX(s string) (string, []string) {
	var notes []string

	if strings.Contains(s, "−") {
		s = strings.ReplaceAll(s, "−", "-")
		notes = append(notes, "replace the Unicode minus sign − with -")
	}

	simple := []struct{ from, to, note string }{
		{"•", "*", "replace • with *"},
		{"·", "*", "replace · with *"},
		{`\left`, "", `drop \left`},
		{`\right`, "", `drop \right`},
		{`\cdot`, "*", `replace \cdot with *`},
		{`\times`, "*", `replace \times with *`},
		{`\,`, " ", `drop the thin space \,`},
	}
	for _, r := range simple {
		if strings.Contains(s, r.from) {
			s = strings.ReplaceAll(s, r.from, r.to)
			notes = append(notes, r.note)
		}
	}

	if strings.Contains(s, `\frac`) {
		var changed bool
		s, changed = replaceFractions(s)
		if changed {
			notes = append(notes, `rewrite \frac{a}{b} as (a)/(b)`)
		}
	}

	if strings.Contains(s, `\sqrt`) {
		s = strings.ReplaceAll(s, `\sqrt`, "sqrt")
		notes = append(notes, `replace \sqrt with sqrt`)
	}

	if strings.ContainsAny(s, "{}") {
		s = strings.NewReplacer("{", "(", "}", ")").Replace(s)
		notes = append(notes, "turn {...} groups into (...), so x^{2} becomes x^(2)")
	}
	return s, notes
}

// replaceFractions rewrites every \frac{a}{b} as (a)/(b). Numerators and
// denominators are normalized first, so they may contain other TeX.
func replaceFractions(s string) (string, bool) {
	changed := false
	for {
		i := strings.Index(s, `\frac`)
		if i < 0 {
			return s, changed
		}
		num, rest, ok := braceGroup(s[i+len(`\frac`):])
		if !ok {
			return s, changed
		}
		den, after, ok := braceGroup(rest)
		if !ok {
			return s, changed
		}
		numASCII, _ := normalizeTeX(num)
		denASCII, _ := normalizeTeX(den)
		s = s[:i] + "(" + numASCII + ")/(" + denASCII + ")" + after
		changed = true
	}
}

// braceGroup reads a {...} group at the start of s, allowing nested braces.
// It returns the text inside the braces and the text after them.
func braceGroup(s string) (inside, rest string, ok bool) {
	s = strings.TrimLeft(s, " ")
	if !strings.HasPrefix(s, "{") {
		return "", "", false
	}
	depth := 0
	for i, c := range s {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[1:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}
