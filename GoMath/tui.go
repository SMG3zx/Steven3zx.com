package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type mode int

const (
	modeInput mode = iota
	modeSteps
	modeBank
)

type model struct {
	mode   mode
	input  string
	op     Operation
	report Report
	step   int

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

// run solves input with op and switches to the step view.
func (m model) run(op Operation, input string) model {
	m.op = op
	m.input = input
	m.report = Solve(input, op)
	m.step = 0
	m.mode = modeSteps
	m.checked = false
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if km.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	switch m.mode {
	case modeInput:
		return m.updateInput(km)
	case modeSteps:
		return m.updateSteps(km)
	case modeBank:
		return m.updateBank(km)
	}
	return m, nil
}

func (m model) updateInput(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch km.Type {
	case tea.KeyEnter:
		return m.run(OpSimplify, m.input), nil
	case tea.KeyCtrlF:
		return m.run(OpFactor, m.input), nil
	case tea.KeyCtrlT:
		return m.run(OpTechnique, m.input), nil
	case tea.KeyCtrlP:
		return m.run(OpPrime, m.input), nil
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

func (m model) updateSteps(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.report.Steps) - 1
	switch km.Type {
	case tea.KeyRight:
		if m.step < last {
			m.step++
		}
	case tea.KeyLeft:
		if m.step > 0 {
			m.step--
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
		m = m.run(e.Op, e.Input)
		m.checked = true
		m.checkOK, m.checkDetail = e.Check()
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

func (m model) viewSteps(sb *strings.Builder) {
	fmt.Fprintf(sb, "%s of %q\n\n", m.op, m.input)
	if m.report.Err != nil {
		fmt.Fprintf(sb, "error: %v\n\n", m.report.Err)
	}
	if len(m.report.Steps) == 0 {
		sb.WriteString("nothing to show\n")
		return
	}

	s := m.report.Steps[m.step]
	fmt.Fprintf(sb, "step %d/%d  [%s] %s\n\n", m.step+1, len(m.report.Steps), s.Phase, s.Title)
	fmt.Fprintf(sb, "%s\n\n", s.Detail)
	if m.step == len(m.report.Steps)-1 && m.report.Err == nil {
		fmt.Fprintf(sb, "result: %s\n\n", m.report.Result)
	}
	if m.checked {
		if m.checkOK {
			fmt.Fprintf(sb, "matches the worksheet answer: %s\n\n", m.report.Result)
		} else {
			fmt.Fprintf(sb, "does not match: %s\n\n", m.checkDetail)
		}
	}
	sb.WriteString("left/right step   esc back\n")
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
