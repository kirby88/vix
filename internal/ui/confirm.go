package ui

import "fmt"

// renderConfirmPrompt renders the confirmation prompt for a tool execution.
func renderConfirmPrompt(toolName string, width int) string {
	prompt := fmt.Sprintf("Allow %s? [Y/n] ", toolName)
	return confirmStyle.Render(prompt)
}
