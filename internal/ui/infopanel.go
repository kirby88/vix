package ui

import (
	"path/filepath"

	"charm.land/lipgloss/v2"
)

// renderInfoPanel renders the right-side info panel shown beside the chat
// scroll view. It displays the VIX logo, the working directory, and the
// active model name.
func renderInfoPanel(cwd, modelName string, width, height int, s Styles) string {
	logo := renderVixBanner()

	dirLabel := lipgloss.NewStyle().
		Foreground(colorDim).
		Width(width).
		Render(filepath.Base(cwd))

	modelLabel := lipgloss.NewStyle().
		Foreground(colorPrimary).
		Width(width).
		Render(modelName)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		logo,
		"",
		dirLabel,
		modelLabel,
	)

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(content)
}
