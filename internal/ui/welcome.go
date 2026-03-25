package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// renderVixBanner returns the VIX ASCII art with a brand gradient.
func renderVixBanner() string {
	lines := []string{
		" ██╗   ██╗ ██╗ ██╗  ██╗",
		" ██║   ██║ ██║ ╚██╗██╔╝",
		" ██║   ██║ ██║  ╚███╔╝ ",
		" ╚██╗ ██╔╝ ██║  ██╔██╗ ",
		"  ╚████╔╝  ██║ ██╔╝ ██╗",
		"   ╚═══╝   ╚═╝ ╚═╝  ╚═╝",
	}

	// Compute vertical gradient from primary → secondary
	ca, _ := colorful.Hex(primaryHex)
	cb, _ := colorful.Hex(secondaryHex)

	var result strings.Builder
	for i, line := range lines {
		t := float64(i) / float64(len(lines)-1)
		c := ca.BlendHcl(cb, t)
		hex := fmt.Sprintf("#%02x%02x%02x", int(c.R*255), int(c.G*255), int(c.B*255))
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
		result.WriteString(style.Render(line))
		result.WriteRune('\n')
	}
	return result.String()
}

// renderWelcomeInline renders a centered welcome message for inline mode.
func renderWelcomeInline(width, height int, s Styles) string {
	// Build the welcome block (uncentered)
	var block strings.Builder

	block.WriteString(renderVixBanner())
	block.WriteString("\n")

	subtitle := lipgloss.NewStyle().
		Foreground(s.ColorWhite).
		Italic(true).
		Render("AI coding assistant")

	block.WriteString(subtitle + "\n\n")

	shortcutStyle := lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)
	descStyle := lipgloss.NewStyle().
		Foreground(s.ColorWhite)

	shortcuts := []struct {
		key  string
		desc string
	}{
		{"Tab", "Switch focus (input/chat)"},
		{"Shift+Tab", "Cycle mode"},
		{"Ctrl+P", "Command palette"},
		{"Ctrl+R", "Search history"},
		{"Ctrl+C", "Quit"},
		{"Esc", "Cancel current operation"},
	}

	// Find the longest key and longest desc to build fixed-width rows
	maxKeyWidth := 0
	maxDescWidth := 0
	for _, sc := range shortcuts {
		if len(sc.key) > maxKeyWidth {
			maxKeyWidth = len(sc.key)
		}
		if len(sc.desc) > maxDescWidth {
			maxDescWidth = len(sc.desc)
		}
	}
	rowWidth := maxKeyWidth + 2 + maxDescWidth // key + gap + desc

	for _, sc := range shortcuts {
		key := shortcutStyle.
			Width(maxKeyWidth).
			AlignHorizontal(lipgloss.Right).
			Render(sc.key)
		desc := descStyle.
			Width(maxDescWidth).
			Render(sc.desc)
		row := lipgloss.NewStyle().Width(rowWidth).Render(key + "  " + desc)
		block.WriteString(row + "\n")
	}

	// Center horizontally and vertically
	centered := lipgloss.NewStyle().
		Width(width).
		Height(height).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Render(block.String())

	return centered
}
