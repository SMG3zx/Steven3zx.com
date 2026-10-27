package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type mode int

const (
	modeInput mode = iota
	modeSteps
	modeBank
)

const (
	// frameInterval paces the decode animation. A key press still moves one
	// step at a time; the animation only plays out the current stage.
	frameInterval = time.Second / 30
	// charsPerFrame is how many characters of the stage resolve each frame.
	charsPerFrame = 1
	// defaultWidth is used until the terminal reports its size.
	defaultWidth = 80
	// chromeLines is the screen space around the stage text: the header, the
	// box border and padding, the caption, progress bar, result, and help.
	chromeLines = 13
)

type frameMsg struct{}

// scrambleGlyphs are shown in place of characters that have not resolved yet.
var scrambleGlyphs = []rune("▓▒░#%&@*?")

var (
	dimStyle   = lipgloss.NewStyle().Faint(true)
	boldStyle  = lipgloss.NewStyle().Bold(true)
	stageStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8")).
			Padding(1, 3).
			Bold(true)
	phaseColors = map[string]lipgloss.Style{
		"Input":   lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		"Tokens":  lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		"Parse":   lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"Compute": lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		"Result":  lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
	}
)

type model struct {
	mode   mode
	input  string
	op     Operation
	report Report
	step   int
	// revealed is how many characters of the current stage have resolved.
	revealed int
	// frame counts animation frames, so the scramble glyphs keep moving.
	frame  int
	width  int
	height int

	bankIdx     int
	checked     bool
	checkOK     bool
	checkDetail string
}

func newModel() model {
	return model{mode: modeInput, op: OpSimplify}
}

func (m model) Init() tea.Cmd {
	return nil
}

// stageText is the part of a step worth showing on its own: the result after
// the last " = " when the step has one, and the whole detail otherwise.
func stageText(s Step) string {
	if i := strings.LastIndex(s.Detail, " = "); i >= 0 {
		return s.Detail[i+len(" = "):]
	}
	return s.Detail
}

// currentStage is the stage text of the step on screen.
func (m model) currentStage() string {
	if m.step >= len(m.report.Steps) {
		return ""
	}
	return stageText(m.report.Steps[m.step])
}

// currentLen is the length, in runes, of the current stage. Parse steps are
// drawn as scenes rather than decoded, so they have no stage text to animate.
func (m model) currentLen() int {
	if _, ok := m.report.Scenes[m.step]; ok {
		return 0
	}
	return len([]rune(m.currentStage()))
}

// animating reports whether the current stage is still decoding.
func (m model) animating() bool {
	return m.mode == modeSteps && m.revealed < m.currentLen()
}

// nextFrame schedules the next animation frame if the current stage is still decoding.
func (m model) nextFrame() tea.Cmd {
	if !m.animating() {
		return nil
	}
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}

// decoded is the current stage with unresolved characters shown as scrambled glyphs.
// Spaces stay put so the layout does not jump while the text resolves.
func (m model) decoded() string {
	var sb strings.Builder
	for i, r := range []rune(m.currentStage()) {
		switch {
		case i < m.revealed || r == ' ':
			sb.WriteRune(r)
		default:
			sb.WriteRune(scrambleGlyphs[(m.frame+i*3)%len(scrambleGlyphs)])
		}
	}
	return sb.String()
}

// run solves input with op and switches to the stage view.
func (m model) run(op Operation, input string) (model, tea.Cmd) {
	m.op = op
	m.input = input
	m.report = Solve(input, op)
	m.step = 0
	m.revealed = 0
	m.mode = modeSteps
	m.checked = false
	return m, m.nextFrame()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case frameMsg:
		m.frame++
		if m.animating() {
			m.revealed += charsPerFrame
			if m.revealed > m.currentLen() {
				m.revealed = m.currentLen()
			}
		}
		return m, m.nextFrame()
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		switch m.mode {
		case modeInput:
			return m.updateInput(msg)
		case modeSteps:
			return m.updateSteps(msg)
		case modeBank:
			return m.updateBank(msg)
		}
	}
	return m, nil
}

func (m model) updateInput(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch km.Type {
	case tea.KeyEnter:
		return m.run(OpSimplify, m.input)
	case tea.KeyCtrlF:
		return m.run(OpFactor, m.input)
	case tea.KeyCtrlT:
		return m.run(OpTechnique, m.input)
	case tea.KeyCtrlP:
		return m.run(OpPrime, m.input)
	case tea.KeyCtrlB:
		m.mode = modeBank
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyBackspace:
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		m.input += " "
	case tea.KeyRunes:
		m.input += string(km.Runes)
	}
	return m, nil
}

// updateSteps treats each key as one turn. If the current stage is still
// decoding, the key finishes it and nothing else. Otherwise it moves a step.
func (m model) updateSteps(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.report.Steps) - 1
	switch km.Type {
	case tea.KeyRight:
		if m.animating() {
			m.revealed = m.currentLen()
			return m, nil
		}
		if m.step < last {
			m.step++
			m.revealed = 0
			return m, m.nextFrame()
		}
	case tea.KeyLeft:
		if m.animating() {
			m.revealed = m.currentLen()
			return m, nil
		}
		if m.step > 0 {
			m.step--
			m.revealed = m.currentLen()
		}
	case tea.KeyEsc:
		m.mode = modeInput
	}
	return m, nil
}

func (m model) updateBank(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch km.Type {
	case tea.KeyUp:
		if m.bankIdx > 0 {
			m.bankIdx--
		}
	case tea.KeyDown:
		if m.bankIdx < len(Worksheet)-1 {
			m.bankIdx++
		}
	case tea.KeyEnter:
		e := Worksheet[m.bankIdx]
		var cmd tea.Cmd
		m, cmd = m.run(e.Op, e.Input)
		m.checked = true
		m.checkOK, m.checkDetail = e.Check()
		return m, cmd
	case tea.KeyEsc:
		m.mode = modeInput
	}
	return m, nil
}

func (m model) View() string {
	var sb strings.Builder
	switch m.mode {
	case modeInput:
		sb.WriteString("GoMath: type ASCII (x^2 + 1) or TeX (\\frac{x}{2})\n\n")
		fmt.Fprintf(&sb, "> %s_\n\n", m.input)
		sb.WriteString("enter simplify   ctrl+f factor   ctrl+t technique   ctrl+p prime check\n")
		sb.WriteString("ctrl+b worksheet bank   esc quit\n")
	case modeSteps:
		m.viewSteps(&sb)
	case modeBank:
		m.viewBank(&sb)
	}
	return sb.String()
}

// viewSteps shows one stage at a time: a centred box holding the decoding
// result, a caption for the step, a progress bar, and the help line.
func (m model) viewSteps(sb *strings.Builder) {
	width := m.screenWidth()
	center := func(s string) string {
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, s) + "\n"
	}

	sb.WriteString(center(dimStyle.Render(fmt.Sprintf("%s of %q", m.op, m.input))))
	sb.WriteString("\n")
	if m.report.Err != nil {
		sb.WriteString(center(fmt.Sprintf("error: %v", m.report.Err)))
		return
	}
	if len(m.report.Steps) == 0 {
		sb.WriteString(center("nothing to show"))
		return
	}

	s := m.report.Steps[m.step]
	if ev, ok := m.report.Scenes[m.step]; ok {
		sb.WriteString(center(renderParseScene(ev, width)))
	} else {
		box := stageStyle.Width(m.boxWidth()).Render(strings.Join(m.stageLines(), "\n"))
		sb.WriteString(center(box))
	}
	sb.WriteString("\n")

	phase := s.Phase
	if style, ok := phaseColors[s.Phase]; ok {
		phase = style.Render(s.Phase)
	}
	sb.WriteString(center(fmt.Sprintf("%s  %s", phase, boldStyle.Render(s.Title))))
	sb.WriteString(center(m.progressBar()))
	sb.WriteString("\n")

	if m.step == len(m.report.Steps)-1 && !m.animating() {
		sb.WriteString(center(boldStyle.Render("result: " + m.report.Result)))
	}
	if m.checked && !m.animating() {
		if m.checkOK {
			sb.WriteString(center("matches the worksheet answer"))
		} else {
			sb.WriteString(center("does not match: " + m.checkDetail))
		}
	}
	sb.WriteString("\n")
	sb.WriteString(center(dimStyle.Render("left/right: next (or finish)   esc back")))
}

// screenWidth is the terminal width, or defaultWidth before the terminal reports it.
func (m model) screenWidth() int {
	if m.width <= 0 {
		return defaultWidth
	}
	return m.width
}

// boxWidth is the fixed width of the stage box, so it does not jump between steps.
func (m model) boxWidth() int {
	w := m.screenWidth() - 4
	if w > 70 {
		w = 70
	}
	if w < 30 {
		w = 30
	}
	return w
}

// stageLines wraps the decoded stage to fit the box, and clips it to the height left over.
func (m model) stageLines() []string {
	// The box border takes 2 columns and its padding takes 3 on each side.
	inner := m.boxWidth() - 8

	var lines []string
	for _, para := range strings.Split(m.decoded(), "\n") {
		runes := []rune(para)
		if len(runes) == 0 {
			lines = append(lines, "")
		}
		for len(runes) > inner {
			lines = append(lines, string(runes[:inner]))
			runes = runes[inner:]
		}
		lines = append(lines, string(runes))
	}

	if m.height > 0 {
		if room := m.height - chromeLines; room > 0 && len(lines) > room {
			lines = append(lines[:room-1], "…")
		}
	}
	return lines
}

// progressBar shows how far through the steps the walkthrough is.
func (m model) progressBar() string {
	const width = 30
	total := len(m.report.Steps)
	filled := (m.step + 1) * width / total
	return fmt.Sprintf("[%s%s] %d/%d",
		strings.Repeat("█", filled), strings.Repeat("░", width-filled), m.step+1, total)
}

func (m model) viewBank(sb *strings.Builder) {
	sb.WriteString("Worksheet bank: each answer is checked against the screenshot\n\n")
	for i, e := range Worksheet {
		cursor := "  "
		if i == m.bankIdx {
			cursor = "> "
		}
		fmt.Fprintf(sb, "%s%-14s %-10s %s\n", cursor, e.Source, e.Op, e.Input)
	}
	sb.WriteString("\n")
	if m.checked {
		if m.checkOK {
			fmt.Fprintf(sb, "matches the worksheet answer: %s\n", m.report.Result)
		} else {
			fmt.Fprintf(sb, "does not match: %s\n", m.checkDetail)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("up/down choose   enter run and check   esc back\n")
}
