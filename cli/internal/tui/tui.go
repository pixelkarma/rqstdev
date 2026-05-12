package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

var ErrAborted = errors.New("cancelled")

type Option struct {
	Label       string
	Description string
}

type Field struct {
	Key         string
	Label       string
	Value       string
	Placeholder string
	Secret      bool
	Validate    func(string) error
}

func CanUse() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func Select(title string, lines []string, options []Option, initial int) (int, error) {
	if len(options) == 0 {
		return -1, errors.New("no options available")
	}
	if initial < 0 || initial >= len(options) {
		initial = 0
	}
	model := selectModel{
		title:   title,
		lines:   lines,
		options: options,
		cursor:  initial,
	}
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		return -1, err
	}
	done := final.(selectModel)
	if done.aborted {
		return -1, ErrAborted
	}
	return done.choice, nil
}

func Inputs(title string, lines []string, fields []Field) (map[string]string, error) {
	if len(fields) == 0 {
		return map[string]string{}, nil
	}
	model := inputModel{
		title:  title,
		lines:  lines,
		fields: fields,
		inputs: make([]textinput.Model, 0, len(fields)),
		values: map[string]string{},
	}
	for _, field := range fields {
		ti := textinput.New()
		ti.Placeholder = field.Placeholder
		ti.SetValue(field.Value)
		ti.Prompt = ""
		if field.Secret {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		model.inputs = append(model.inputs, ti)
		model.values[field.Key] = field.Value
	}
	model.inputs[0].Focus()

	final, err := tea.NewProgram(model).Run()
	if err != nil {
		return nil, err
	}
	done := final.(inputModel)
	if done.aborted {
		return nil, ErrAborted
	}
	return done.values, nil
}

type selectModel struct {
	title   string
	lines   []string
	options []Option
	cursor  int
	choice  int
	aborted bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.aborted = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter":
			m.choice = m.cursor
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	var b strings.Builder
	b.WriteString(m.title)
	b.WriteString("\n\n")
	for _, line := range m.lines {
		if strings.TrimSpace(line) != "" {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(m.lines) > 0 {
		b.WriteString("\n")
	}
	for i, option := range m.options {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		b.WriteString(cursor)
		b.WriteString(option.Label)
		if strings.TrimSpace(option.Description) != "" {
			b.WriteString("  ")
			b.WriteString(option.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n↑/↓ move  enter select  esc cancel\n")
	return b.String()
}

type inputModel struct {
	title   string
	lines   []string
	fields  []Field
	inputs  []textinput.Model
	index   int
	values  map[string]string
	errText string
	aborted bool
}

func (m inputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "up":
			m.prev()
			return m, nil
		case "tab", "down":
			m.next()
			return m, nil
		case "enter":
			value := strings.TrimSpace(m.inputs[m.index].Value())
			if err := m.validateCurrent(value); err != nil {
				m.errText = err.Error()
				return m, nil
			}
			m.values[m.fields[m.index].Key] = value
			m.errText = ""
			if m.index == len(m.inputs)-1 {
				return m, tea.Quit
			}
			m.next()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.inputs[m.index], cmd = m.inputs[m.index].Update(msg)
	m.values[m.fields[m.index].Key] = strings.TrimSpace(m.inputs[m.index].Value())
	return m, cmd
}

func (m inputModel) View() string {
	var b strings.Builder
	b.WriteString(m.title)
	b.WriteString("\n\n")
	for _, line := range m.lines {
		if strings.TrimSpace(line) != "" {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(m.lines) > 0 {
		b.WriteString("\n")
	}
	for i, field := range m.fields {
		cursor := "  "
		if i == m.index {
			cursor = "> "
		}
		b.WriteString(cursor)
		b.WriteString(field.Label)
		b.WriteString(": ")
		b.WriteString(m.inputs[i].View())
		b.WriteString("\n")
	}
	if m.errText != "" {
		b.WriteString("\n")
		b.WriteString("Error: ")
		b.WriteString(m.errText)
		b.WriteString("\n")
	}
	b.WriteString("\nenter next/submit  tab move  esc cancel\n")
	return b.String()
}

func (m *inputModel) validateCurrent(value string) error {
	if validator := m.fields[m.index].Validate; validator != nil {
		return validator(value)
	}
	return nil
}

func (m *inputModel) next() {
	m.inputs[m.index].Blur()
	if m.index < len(m.inputs)-1 {
		m.index++
	}
	m.inputs[m.index].Focus()
}

func (m *inputModel) prev() {
	m.inputs[m.index].Blur()
	if m.index > 0 {
		m.index--
	}
	m.inputs[m.index].Focus()
}

func Confirm(title string, lines []string, options ...string) (string, error) {
	menu := make([]Option, 0, len(options))
	for _, option := range options {
		menu = append(menu, Option{Label: option})
	}
	index, err := Select(title, lines, menu, 0)
	if err != nil {
		return "", err
	}
	return options[index], nil
}

func RequireText(title string, lines []string, label, expected string) error {
	values, err := Inputs(title, lines, []Field{{
		Key:   "value",
		Label: label,
		Validate: func(value string) error {
			if strings.TrimSpace(value) != expected {
				return fmt.Errorf("must match %q", expected)
			}
			return nil
		},
	}})
	if err != nil {
		return err
	}
	if strings.TrimSpace(values["value"]) != expected {
		return fmt.Errorf("must match %q", expected)
	}
	return nil
}
