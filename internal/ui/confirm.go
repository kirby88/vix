package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderConfirmPrompt renders the confirmation prompt for a tool execution.
func renderConfirmPrompt(toolName string, width int) string {
	prompt := fmt.Sprintf("Allow %s? [Y/n] ", toolName)
	return confirmStyle.Render(prompt)
}

// renderQuitDialog renders the quit confirmation as a centered overlay box,
// styled like the command palette. width/height are the terminal dimensions.
// selected: 0 = Yes, 1 = No.
func renderQuitDialog(width, height int, s Styles, selected int) string {
	dialogWidth := 44
	if dialogWidth > width-4 {
		dialogWidth = width - 4
	}
	innerWidth := dialogWidth - 4 // account for border + padding

	title := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).
		Width(innerWidth).Align(lipgloss.Center).
		Render("Quit vix?")

	sep := s.CommandPaletteSepStyle.Width(innerWidth).Render(strings.Repeat("─", innerWidth))

	msg := lipgloss.NewStyle().Foreground(s.ColorDimGray).
		Width(innerWidth).Align(lipgloss.Center).
		Render("Any running agent will be cancelled.")

	yesStyle := lipgloss.NewStyle().Bold(true).Foreground(s.ColorDimGray)
	noStyle := lipgloss.NewStyle().Bold(true).Foreground(s.ColorDimGray)
	if selected == 0 {
		yesStyle = yesStyle.Foreground(colorSecondary)
	} else {
		noStyle = noStyle.Foreground(colorSecondary)
	}

	yesBtn := yesStyle.Render("Yes")
	noBtn := noStyle.Render("No")
	buttons := lipgloss.NewStyle().Width(innerWidth).Align(lipgloss.Center).
		Render(yesBtn + "    " + noBtn)

	content := title + "\n" + sep + "\n" + msg + "\n\n" + buttons

	return s.CommandPaletteStyle.Width(dialogWidth).Render(content)
}
