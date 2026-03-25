package ui

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

var (
	codeBlockRe    = regexp.MustCompile("(?s)```(\\w*)\\n(.*?)```")
	ansiRe         = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	trailingPadRe  = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)
	// emptyTableRe matches markdown tables with only a header row and separator (no data rows).
	// It matches: | header | ... |\n| --- | ... |\n followed by a blank line or EOF.
	emptyTableRe = regexp.MustCompile(`(?m)(\|[^\n]+\|\n\|[\s:\-|]+\|\n)(\n|\z)`)
)

// MarkdownRenderer wraps Glamour for rendering markdown to styled terminal output.
type MarkdownRenderer struct {
	renderer       *glamour.TermRenderer
	width          int
	hasDarkBG      bool
	codeBoxBorder  lipgloss.Style
}

// NewMarkdownRenderer creates a new markdown renderer with the given width.
func NewMarkdownRenderer(width int, hasDarkBG bool) *MarkdownRenderer {
	if width < 20 {
		width = 80
	}

	glamStyle := styles.DarkStyle
	if !hasDarkBG {
		glamStyle = styles.LightStyle
	}

	dimGray := lipgloss.Color("8")
	if !hasDarkBG {
		dimGray = lipgloss.Color("7")
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamStyle),
		glamour.WithWordWrap(width-4),
	)
	if err != nil {
		return &MarkdownRenderer{width: width, hasDarkBG: hasDarkBG, codeBoxBorder: lipgloss.NewStyle().Foreground(dimGray)}
	}
	return &MarkdownRenderer{renderer: r, width: width, hasDarkBG: hasDarkBG, codeBoxBorder: lipgloss.NewStyle().Foreground(dimGray)}
}

type codeBlock struct {
	lang string
	code string
}

// Render renders markdown text to styled terminal output.
func (m *MarkdownRenderer) Render(md string) string {
	if m.renderer == nil {
		return md + "\n"
	}

	// Strip empty markdown tables (header + separator, no data rows) that
	// glamour renders without a bottom border.
	md = emptyTableRe.ReplaceAllString(md, "$2")

	// Extract fenced code blocks and replace with placeholders.
	// Use a plain alphanumeric marker to avoid markdown interpretation
	// (e.g. __ would be treated as bold).
	var blocks []codeBlock
	replaced := codeBlockRe.ReplaceAllStringFunc(md, func(match string) string {
		sub := codeBlockRe.FindStringSubmatch(match)
		lang := sub[1]
		code := sub[2]
		code = strings.TrimRight(code, "\n")
		idx := len(blocks)
		blocks = append(blocks, codeBlock{lang: lang, code: code})
		return fmt.Sprintf("CBLK%dMARKER", idx)
	})

	out, err := m.renderer.Render(replaced)
	if err != nil {
		return md + "\n"
	}

	// Replace placeholders with rendered code boxes.
	// Glamour wraps text in ANSI codes, so we can't do a simple string replace.
	// Instead, find lines whose stripped text contains the marker and replace them.
	for i, block := range blocks {
		marker := fmt.Sprintf("CBLK%dMARKER", i)
		highlighted := m.glamourHighlight(block.lang, block.code)
		box := renderCodeBox(block.lang, highlighted, m.width, m.codeBoxBorder)
		out = replaceMarkerLine(out, marker, box)
	}

	return out
}

// glamourHighlight renders a code block through glamour to get its native
// syntax highlighting, then strips glamour's padding/margins to return just
// the highlighted code lines.
func (m *MarkdownRenderer) glamourHighlight(lang, code string) []string {
	fence := "```"
	if lang != "" {
		fence += lang
	}
	md := fence + "\n" + code + "\n```\n"

	out, err := m.renderer.Render(md)
	if err != nil {
		return strings.Split(code, "\n")
	}

	// Glamour renders code blocks with 4-space indent (2 margin + 2 code indent)
	// and pads each line with ANSI-colored spaces. Extract code lines and strip
	// the leading indent and trailing padding.
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		stripped := ansiRe.ReplaceAllString(line, "")
		trimmed := strings.TrimRight(stripped, " ")
		if trimmed == "" {
			continue
		}
		// Code lines from glamour have 4-space indent
		if len(stripped) >= 4 && stripped[:4] == "    " {
			lines = append(lines, stripGlamourLine(line))
		}
	}

	if len(lines) == 0 {
		return strings.Split(code, "\n")
	}
	return lines
}

// stripGlamourLine removes glamour's leading 4-char indent and trailing
// ANSI-colored space padding from a highlighted code line.
func stripGlamourLine(line string) string {
	// Walk past leading ANSI codes and spaces, skipping exactly 4 visible spaces
	data := []byte(line)
	pos := 0
	spacesSkipped := 0

	for pos < len(data) && spacesSkipped < 4 {
		if pos+1 < len(data) && data[pos] == '\x1b' && data[pos+1] == '[' {
			j := pos + 2
			for j < len(data) && data[j] != 'm' {
				j++
			}
			if j < len(data) {
				j++
			}
			pos = j
		} else if data[pos] == ' ' {
			spacesSkipped++
			pos++
		} else {
			break
		}
	}

	content := string(data[pos:])

	// Strip trailing padding: glamour pads lines with ANSI-colored spaces.
	// Each padding space looks like \x1b[38;5;252m \x1b[0m.
	// Remove any trailing mix of ANSI codes and spaces.
	content = trailingPadRe.ReplaceAllString(content, "")

	return content
}

// replaceMarkerLine finds the line in text whose ANSI-stripped content contains
// marker, and replaces that entire line with replacement.
func replaceMarkerLine(text, marker, replacement string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		stripped := ansiRe.ReplaceAllString(line, "")
		if strings.Contains(stripped, marker) {
			lines[i] = replacement
			break
		}
	}
	return strings.Join(lines, "\n")
}

// UpdateWidth recreates the renderer with a new width.
func (m *MarkdownRenderer) UpdateWidth(width int) {
	if width < 20 {
		width = 80
	}
	m.width = width

	glamStyle := styles.DarkStyle
	if !m.hasDarkBG {
		glamStyle = styles.LightStyle
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamStyle),
		glamour.WithWordWrap(width-4),
	)
	if err != nil {
		return
	}
	m.renderer = r
}

// renderCodeBox renders a code block inside a rounded box with optional language label.
// highlightedLines are pre-highlighted code lines (with ANSI codes).
func renderCodeBox(lang string, highlightedLines []string, width int, borderStyle lipgloss.Style) string {
	// Inner width: total width minus 2 indent, 2 border chars, 2 padding spaces
	innerWidth := width - 6
	if innerWidth < 10 {
		innerWidth = 10
	}

	// Build top border
	var topBorder string
	totalBorderWidth := innerWidth + 2 // +2 for padding spaces inside box

	if lang != "" {
		label := " " + lang + " "
		labelWidth := lipgloss.Width(label)
		remainingDashes := totalBorderWidth - 1 - labelWidth
		if remainingDashes < 0 {
			remainingDashes = 0
		}
		topBorder = "  " + borderStyle.Render("╭─"+label+strings.Repeat("─", remainingDashes)+"╮")
	} else {
		topBorder = "  " + borderStyle.Render("╭"+strings.Repeat("─", totalBorderWidth)+"╮")
	}

	// Build content lines
	var contentLines []string
	for _, line := range highlightedLines {
		visualWidth := lipgloss.Width(line)
		padding := innerWidth - visualWidth
		if padding < 0 {
			padding = 0
		}
		padded := line + strings.Repeat(" ", padding)
		contentLines = append(contentLines,
			"  "+borderStyle.Render("│")+" "+padded+" "+borderStyle.Render("│"))
	}

	// Build bottom border
	bottomBorder := "  " + borderStyle.Render("╰"+strings.Repeat("─", totalBorderWidth)+"╯")

	result := topBorder + "\n"
	for _, line := range contentLines {
		result += line + "\n"
	}
	result += bottomBorder

	return result
}
