package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const parseCallTitle = "parse_expression()"

// renderParseScene draws one parser decision: a frame for each open parse call,
// the tokens as bubbles with their roles labelled, and the binding-power check
// that decided the step. width limits how many unread tokens are drawn.
func renderParseScene(e ParseEvent, width int) string {
	body := sceneBody(e, width)
	for level := 0; level <= e.Depth; level++ {
		body = frame(body, parseCallTitle)
	}
	return strings.Join(body, "\n")
}

// sceneBody draws the tokens, the comparison box above the operator, and nothing else.
func sceneBody(e ParseEvent, width int) []string {
	var pieces []piece
	opIndex := -1

	if e.Lhs != "" {
		pieces = append(pieces, newPiece(e.Lhs, "lhs"))
	}
	if e.Op != "" {
		opIndex = len(pieces)
		pieces = append(pieces, newPiece(e.Op, "op"))
	}

	// Each unread token is a bubble. The first one is the next token the parser reads.
	shown := e.Remaining
	if limit := tokenLimit(width); len(shown) > limit {
		shown = append(append([]string{}, shown[:limit-1]...), "…")
	}
	for i, tok := range shown {
		label := ""
		if i == 0 {
			label = "next"
		}
		pieces = append(pieces, newPiece(tok, label))
	}

	row, starts := joinPieces(pieces)
	cmp := comparisonText(e)
	if cmp == "" || opIndex < 0 {
		return row
	}

	// The comparison box sits above the operator it decided.
	box := comparisonBox(cmp)
	opCenter := starts[opIndex] + pieces[opIndex].width/2
	offset := opCenter - utf8.RuneCountInString(box[1])/2
	if offset < 0 {
		offset = 0
	}
	var out []string
	for _, line := range box {
		out = append(out, strings.Repeat(" ", offset)+line)
	}
	return append(out, row...)
}

// tokenLimit is how many unread tokens fit across the width.
func tokenLimit(width int) int {
	if width <= 0 {
		return 10
	}
	if n := width / 6; n >= 2 {
		return n
	}
	return 2
}

// comparisonText is the binding-power check behind a take, implicit, or stop decision.
func comparisonText(e ParseEvent) string {
	switch e.Kind {
	case "take", "implicit":
		return fmt.Sprintf("%d ≥ %d", e.LBP, e.MinBP)
	case "stop":
		return fmt.Sprintf("%d < %d", e.LBP, e.MinBP)
	}
	return ""
}

// comparisonBox draws text in a small rounded box.
func comparisonBox(text string) []string {
	n := utf8.RuneCountInString(text)
	top := "╭" + strings.Repeat("─", n+2) + "╮"
	mid := "│ " + text + " │"
	bottom := "╰" + strings.Repeat("─", n+2) + "╯"
	return []string{top, mid, bottom}
}

// piece is one token drawn as a bubble, with its role written under it.
type piece struct {
	lines []string
	width int
}

// newPiece draws text as a bubble with label centred underneath it.
func newPiece(text, label string) piece {
	n := utf8.RuneCountInString(text)
	bubble := []string{
		"╭" + strings.Repeat("─", n+2) + "╮",
		"│ " + text + " │",
		"╰" + strings.Repeat("─", n+2) + "╯",
	}
	width := n + 4
	if l := utf8.RuneCountInString(label); l > width {
		width = l
	}
	lines := make([]string, 0, 4)
	for _, b := range bubble {
		lines = append(lines, padCenter(b, width))
	}
	lines = append(lines, padCenter(label, width))
	return piece{lines: lines, width: width}
}

// joinPieces puts the pieces side by side and returns each piece's starting column.
func joinPieces(pieces []piece) ([]string, []int) {
	const gap = 2
	starts := make([]int, len(pieces))
	rows := make([]string, 4)
	col := 0
	for i, p := range pieces {
		g := gapFor(i, gap)
		for r := range rows {
			rows[r] += strings.Repeat(" ", g) + p.lines[r]
		}
		starts[i] = col + g
		col += g + p.width
	}
	return rows, starts
}

// gapFor leaves the gap before every piece but the first.
func gapFor(i, gap int) int {
	if i == 0 {
		return 0
	}
	return gap
}

// frame wraps body in a rounded box with title on its top edge.
func frame(body []string, title string) []string {
	inner := 0
	for _, line := range body {
		if n := utf8.RuneCountInString(line); n > inner {
			inner = n
		}
	}
	label := " " + title + " "

	// The frame is never narrower than its title, so the top border always fits.
	total := inner + 4
	if min := utf8.RuneCountInString(label) + 2; total < min {
		total = min
	}
	inner = total - 4

	dashes := total - 2 - utf8.RuneCountInString(label)
	left := dashes / 2
	out := []string{"╭" + strings.Repeat("─", left) + label + strings.Repeat("─", dashes-left) + "╮"}
	for _, line := range body {
		out = append(out, "│ "+padRight(line, inner)+" │")
	}
	out = append(out, "╰"+strings.Repeat("─", total-2)+"╯")
	return out
}

// padCenter pads s with spaces on both sides to width columns.
func padCenter(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	left := (width - n) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", width-n-left)
}

// padRight pads s with spaces on the right to width columns.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}
