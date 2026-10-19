package main

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	fillerWordRE = regexp.MustCompile(`(?i)(?:,\s*)?\b(?:um+|uh+|erm+|er+)\b[,;:]?\s*`)
	youKnowFillerRE = regexp.MustCompile(`(?i)(?:,\s*)?\byou know\b,?\s*`)
	duplicatePunctuationRE = regexp.MustCompile(`([,;:])\s*[,;:]+`)
	spaceBeforePunctuationRE = regexp.MustCompile(`\s+([,.;:!?])`)
	spaceAfterPunctuationRE = regexp.MustCompile(`([,;:])([^\s])`)
	transcriptWordRE = regexp.MustCompile(`[\p{L}\p{N}]+(?:['’][\p{L}\p{N}]+)*`)
	listMarkerRE = regexp.MustCompile(`(?i)(^|[.!?;:]\s+|,\s*)((?:step|key point|point)\s+(?:one|two|three|four|five|six|seven|eight|nine|ten|\d+)|first|firstly|second|secondly|third|thirdly|fourth|fourthly|fifth|fifthly|sixth|seventh|eighth|ninth|tenth|finally|lastly|next)\b\s*[,.:]?\s+`)
	listIntroRE  = regexp.MustCompile(`(?i)\b(?:the\s+)?(?:key points|main points|points|steps|items|things to do)\s*(?:are|include|is)?\s*:?\s+`)
	listSplitRE  = regexp.MustCompile(`\s*[,;]\s*`)
)

// polishTranscript cleans common speech disfluencies and structures clearly
// signaled lists without changing the speech recognition model or sending text
// outside the machine.
func polishTranscript(input string) string {
	text := strings.TrimSpace(input)
	text = fillerWordRE.ReplaceAllString(text, "")
	text = youKnowFillerRE.ReplaceAllString(text, "")
	text = removeRepeatedPhrases(text)
	text = normalizeTranscriptSpacing(text)
	text = formatOrdinalList(text)
	text = formatIntroducedList(text)
	return strings.TrimSpace(text)
}

func removeRepeatedPhrases(text string) string {
	for {
		words := transcriptWordRE.FindAllStringIndex(text, -1)
		removed := false
		for start := 0; start < len(words) && !removed; start++ {
			maxWords := (len(words) - start) / 2
			if maxWords > 3 {
				maxWords = 3
			}
			for size := maxWords; size > 0; size-- {
				second := start + size
				matches := true
				for offset := 0; offset < size; offset++ {
					firstSpan := words[start+offset]
					secondSpan := words[second+offset]
					if !strings.EqualFold(text[firstSpan[0]:firstSpan[1]], text[secondSpan[0]:secondSpan[1]]) {
						matches = false
						break
					}
					if offset > 0 && !onlyWhitespace(text[words[start+offset-1][1]:firstSpan[0]]) {
						matches = false
						break
					}
					if offset > 0 && !onlyWhitespace(text[words[second+offset-1][1]:secondSpan[0]]) {
						matches = false
						break
					}
				}
				if !matches {
					continue
				}
				firstEnd := words[start+size-1][1]
				secondStart := words[second][0]
				separator := text[firstEnd:secondStart]
				if !onlyListSeparator(separator) {
					continue
				}
				secondEnd := words[second+size-1][1]
				// Drop punctuation between a stuttered phrase as well, so
				// "I, I think" becomes "I think" instead of "I, think".
				text = text[:firstEnd] + text[secondEnd:]
				removed = true
				break
			}
		}
		if !removed {
			return text
		}
	}
}

func onlyWhitespace(text string) bool {
	return strings.TrimSpace(text) == ""
}

func onlyListSeparator(text string) bool {
	for _, char := range text {
		if char != ' ' && char != '\t' && char != '\r' && char != '\n' && char != ',' {
			return false
		}
	}
	return true
}

func normalizeTranscriptSpacing(text string) string {
	text = duplicatePunctuationRE.ReplaceAllString(text, "$1 ")
	text = spaceBeforePunctuationRE.ReplaceAllString(text, "$1")
	text = spaceAfterPunctuationRE.ReplaceAllString(text, "$1 $2")
	return strings.Join(strings.Fields(text), " ")
}

func formatOrdinalList(text string) string {
	matches := listMarkerRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) < 2 {
		return text
	}
	firstLabel := strings.ToLower(text[matches[0][4]:matches[0][5]])
	if !strings.HasPrefix(firstLabel, "step ") && !strings.HasPrefix(firstLabel, "point ") && firstLabel != "first" && firstLabel != "firstly" {
		return text
	}

	intro := strings.TrimRight(strings.TrimSpace(text[:matches[0][2]]), ",;:-")
	items := make([]string, 0, len(matches))
	for i, match := range matches {
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][2]
		}
		item := strings.TrimSpace(strings.Trim(text[match[1]:end], " \t\r\n,;:-"))
		if item != "" {
			items = append(items, item)
		}
	}
	if len(items) < 2 {
		return text
	}
	return renderList(intro, items, true)
}

func formatIntroducedList(text string) string {
	match := listIntroRE.FindStringIndex(text)
	if match == nil {
		return text
	}
	intro := strings.TrimSpace(text[:match[1]])
	body := strings.TrimSpace(text[match[1]:])
	parts := listSplitRE.Split(body, -1)
	if len(parts) == 1 {
		lower := strings.ToLower(body)
		if and := strings.LastIndex(lower, " and "); and >= 0 {
			parts = []string{body[:and], body[and+5:]}
		}
	} else if last := len(parts) - 1; strings.HasPrefix(strings.ToLower(strings.TrimSpace(parts[last])), "and ") {
		parts[last] = strings.TrimSpace(parts[last])[4:]
	} else if and := strings.LastIndex(strings.ToLower(parts[len(parts)-1]), " and "); and >= 0 {
		last := parts[len(parts)-1]
		parts = append(parts[:len(parts)-1], last[:and], last[and+5:])
	}
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, " \t\r\n,;:-"))
		if part != "" {
			items = append(items, part)
		}
	}
	if len(items) < 2 {
		return text
	}
	return renderList(intro, items, false)
}

func renderList(intro string, items []string, numbered bool) string {
	var result strings.Builder
	if intro != "" {
		result.WriteString(intro)
		result.WriteString("\n\n")
	}
	for i, item := range items {
		if i > 0 {
			result.WriteByte('\n')
		}
		if numbered {
			result.WriteString(strconv.Itoa(i + 1))
			result.WriteString(". ")
		} else {
			result.WriteString("• ")
		}
		result.WriteString(item)
	}
	return result.String()
}
