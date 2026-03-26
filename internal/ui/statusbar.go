package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// renderStatusBar renders the single bottom status line with shortcuts and connection.
func renderStatusBar(
	width int,
	connected bool,
	reconnecting bool,
	warning string,
	s Styles,
) string {
	warningStyle := lipgloss.NewStyle().Foreground(colorWarning).Italic(true)

	// Center: shortcuts or warning overlay
	var center string
	if warning != "" {
		center = warningStyle.Render("⚠ " + warning)
	} else {
		center = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Tab chat/input · Shift+Tab cycle workflows · Ctrl+P commands · Esc cancel · Ctrl+C quit")
	}

	// Right: connection status with label
	var right string
	if connected {
		right = statusConnectedStyle.Render("● Connected")
	} else if reconnecting {
		right = statusReconnectingStyle.Render("● Reconnecting")
	} else {
		right = statusDisconnectedStyle.Render("● Disconnected")
	}

	centerLen := lipgloss.Width(center)
	rightLen := lipgloss.Width(right)

	// Distribute spacing: center the shortcuts in the middle of the row
	totalContent := centerLen + rightLen
	remaining := width - totalContent - 2 // -2 for outer padding
	if remaining < 2 {
		remaining = 2
	}
	leftPad := remaining / 2
	rightPad := remaining - leftPad

	bar := strings.Repeat(" ", leftPad) + center + strings.Repeat(" ", rightPad) + right
	return s.StatusBarStyle.Width(width).Render(bar)
}

func formatTokenCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%dk", n/1000)
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
