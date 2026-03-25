package ui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

// newInput creates a configured text area component.
func newInput() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Ask the agent anything... (Enter to send, Shift+Enter or Alt+Enter for new line)"
	ta.Focus()
	ta.CharLimit = 0 // no limit
	ta.ShowLineNumbers = false
	ta.SetHeight(1) // Start with 1 line
	ta.MaxHeight = 10 // Maximum 10 lines before scrolling

	// Show prompt arrow only on the first line, blank indent on continuation lines
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})

	// Configure keybindings - Shift+Enter inserts newlines, Enter submits
	// ctrl+j is what iTerm2 sends for shift+enter; alt+enter is a universal fallback
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
		key.WithHelp("shift+enter", "new line"),
	)

	// Clear all background styles so textarea matches terminal background
	noStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))

	s := ta.Styles()
	s.Focused.Base = noStyle
	s.Focused.CursorLine = textStyle
	s.Focused.Placeholder = dimStyle
	s.Focused.Text = textStyle
	s.Focused.EndOfBuffer = noStyle
	s.Focused.Prompt = noStyle

	s.Blurred.Base = noStyle
	s.Blurred.CursorLine = noStyle
	s.Blurred.Placeholder = dimStyle
	s.Blurred.Text = dimStyle
	s.Blurred.EndOfBuffer = noStyle
	s.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorDim)

	s.Cursor.Blink = true
	ta.SetStyles(s)

	return ta
}

// renderInputBox wraps the textarea in a rounded border box with mode title embedded in top border.
// When focused is false, the border uses a dim grey color instead of the mode color.
func renderInputBox(modeName string, isWorkflow bool, textareaView string, width int, focused bool, dimColor color.Color) string {
	var titleStyle lipgloss.Style
	var borderColor color.Color

	title := " " + modeName + " "
	if isWorkflow {
		titleStyle = planBarStyle
		borderColor = colorSecondary
	} else {
		titleStyle = chatBarStyle
		borderColor = colorPrimary
	}

	if !focused {
		borderColor = dimColor
		titleStyle = lipgloss.NewStyle().Foreground(dimColor)
	}

	// 1. Build custom top border with embedded title: "╭─ Title ──...──╮"
	borderCharStyle := lipgloss.NewStyle().Foreground(borderColor)

	// Total top border = width chars: "╭" + "─" + title + dashes + "╮"
	titleRendered := titleStyle.Render(title)
	titleLen := lipgloss.Width(titleRendered)

	// Fill remaining space with dashes: width - 2(╭╮) - 1(leading ─) - titleLen
	remainingDashes := width - 3 - titleLen
	if remainingDashes < 0 {
		remainingDashes = 0
	}
	dashes := strings.Repeat("─", remainingDashes)

	topBorder := borderCharStyle.Render("╭─") + titleRendered + borderCharStyle.Render(dashes) + borderCharStyle.Render("╮")

	// 2. Use lipgloss for sides + bottom (no top border)
	// Width is total visual width (includes border + padding)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderTop(false).
		BorderForeground(borderColor).
		Width(width).
		Padding(0, 1)

	body := boxStyle.Render(textareaView)

	return topBorder + "\n" + body
}
