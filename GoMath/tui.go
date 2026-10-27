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
	// frameInterval paces the typing animation. A key press still moves one
	// step at a time; the animation only plays out the text of the current step.
	frameInterval = time.Second / 30
	// runesPerFrame is how much of the current step's detail appears each frame.
	runesPerFrame = 3
	// chromeLines is the space the header, progress bar, and help take up.
	chromeLines = 9
)

type frameMsg struct{}

var (
	dimStyle    = lipgloss.NewStyle().Faint(true)
	boldStyle   = lipgloss.NewStyle().Bold(true)
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
	// revealed is how many runes of the current step's detail are showing.
	revealed int
	height   int

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

// currentLen is the length, in runes, of the current step's detail.
func (m model) currentLen() int {
	if m.step >= len(m.report.Steps) {
		return 0
	}
	return len([]rune(m.report.Steps[m.step].Detail))
}

// animating reports whether the current step is still typing out.
func (m model) animating() bool {
	return m.mode == modeSteps && m.revealed < m.currentLen()
}

// nextFrame schedules the next animation frame if the current step is still typing out.
func (m model) nextFrame() tea.Cmd {
	if !m.animating() {
		return nil
	}
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}

// run solves input with op and switches to the step view.
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
		m.height = msg.Height
		return m, nil
	case frameMsg:
		if m.animating() {
			m.revealed += runesPerFrame
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

// updateSteps treats each key as one turn. If the current step is still typing
// out, the key finishes the typing and nothing else. Otherwise it moves a step.
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

// viewSteps shows every step so far, earlier ones dimmed, the current one
// typing out, and the newest lines kept in view when the window is short.
func (m model) viewSteps(sb *strings.Builder) {
	fmt.Fprintf(sb, "%s of %q\n\n", m.op, m.input)
	if m.report.Err != nil {
		fmt.Fprintf(sb, "error: %v\n\n", m.report.Err)
	}
	if len(m.report.Steps) == 0 {
		sb.WriteString("nothing to show\n")
		return
	}

	for _, line := range m.transcript(m.budget()) {
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\n")
	fmt.Fprintf(sb, "%s\n", m.progressBar())

	if m.step == len(m.report.Steps)-1 && m.report.Err == nil && !m.animating() {
		fmt.Fprintf(sb, "result: %s\n", m.report.Result)
	}
	if m.checked && !m.animating() {
		if m.checkOK {
			fmt.Fprintf(sb, "matches the worksheet answer: %s\n", m.report.Result)
		} else {
			fmt.Fprintf(sb, "does not match: %s\n", m.checkDetail)
		}
	}
	sb.WriteString("\nleft/right: step (or finish typing)   esc back\n")
}

// budget is how many transcript lines fit beside the chrome around them.
func (m model) budget() int {
	if m.height <= 0 {
		return 60
	}
	if n := m.height - chromeLines; n > 4 {
		return n
	}
	return 4
}

// transcript renders steps 0..m.step as lines, keeping only the last max lines.
func (m model) transcript(limit int) []string {
	var lines []string
	for i := 0; i <= m.step; i++ {
		s := m.report.Steps[i]
		phase := s.Phase
		if style, ok := phaseColors[s.Phase]; ok {
			phase = style.Render(s.Phase)
		}

		detail := s.Detail
		if i == m.step {
			runes := []rune(s.Detail)
			detail = string(runes[:m.revealed])
			if m.animating() {
				detail += "▌"
			}
			lines = append(lines, boldStyle.Render(fmt.Sprintf("[%s] %s", phase, s.Title)))
		} else {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("[%s] %s", s.Phase, s.Title)))
		}
		for _, d := range strings.Split(detail, "\n") {
			if i < m.step {
				lines = append(lines, dimStyle.Render("    "+d))
			} else {
				lines = append(lines, "    "+d)
			}
		}
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
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
