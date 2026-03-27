package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kirby88/vix/internal/protocol"
)

// QuestionPanelResult describes the outcome of a key event in the question panel.
type QuestionPanelResult int

const (
	QPNoop      QuestionPanelResult = iota // key consumed, no action
	QPSubmitted                            // user submitted answer(s)
	QPCancelled                            // user cancelled
)

// questionTab holds the state for a single question tab.
type questionTab struct {
	id          string
	category    string
	question    string
	options     []string                       // display labels; last is always "Type something." (simple mode)
	richOptions []protocol.EventQuestionOption // structured options (workflow tool steps)
	selected    int                            // cursor position within this tab's options
	answer      string                         // recorded answer
	answerText  string                         // text input value when has_user_input
	answered    bool
}

// QuestionPanel is a dedicated input panel for answering questions with selectable options.
type QuestionPanel struct {
	visible     bool
	tabs        []questionTab
	currentTab  int
	textInput   textarea.Model // shared inline textarea for "Type something." option
	width       int
	maxVisible  int // max visible options before scrolling
	offset      int // scroll offset for current tab's options
	confirmMode bool   // true when used for tool permission prompts
	preview     string // tool preview content shown in confirm mode
}

// Open initializes the panel from a question event.
func (qp *QuestionPanel) Open(event protocol.EventUserQuestion, width int) {
	qp.visible = true
	qp.width = width
	qp.currentTab = 0
	qp.offset = 0
	qp.maxVisible = 8

	// Initialize shared text input
	qp.textInput = textarea.New()
	qp.textInput.Placeholder = "Type your answer..."
	qp.textInput.Prompt = ""
	qp.textInput.ShowLineNumbers = false
	qp.textInput.SetHeight(1)
	qp.textInput.MaxHeight = 1
	qp.textInput.CharLimit = 0

	noStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	s := qp.textInput.Styles()
	s.Focused.Base = noStyle
	s.Focused.CursorLine = noStyle
	s.Focused.Placeholder = dimStyle
	s.Focused.Text = noStyle
	s.Focused.EndOfBuffer = noStyle
	s.Focused.Prompt = noStyle
	s.Blurred = s.Focused
	qp.textInput.SetStyles(s)

	// Build tabs from event
	if len(event.Questions) > 0 {
		// Multi-question batch mode
		qp.tabs = make([]questionTab, len(event.Questions))
		for i, q := range event.Questions {
			opts := make([]string, len(q.Options))
			copy(opts, q.Options)
			opts = append(opts, "Type something.")
			qp.tabs[i] = questionTab{
				id:       q.ID,
				category: q.Category,
				question: q.Question,
				options:  opts,
				selected: 0,
			}
		}
	} else if len(event.RichOptions) > 0 {
		// Rich options mode (workflow tool steps)
		cat := event.Category
		if cat == "" {
			cat = "Question"
		}
		qp.tabs = []questionTab{{
			id:          "single",
			category:    cat,
			question:    event.Question,
			richOptions: event.RichOptions,
			selected:    0,
		}}
	} else {
		// Single question mode (simple string options)
		opts := make([]string, len(event.Options))
		copy(opts, event.Options)
		opts = append(opts, "Type something.")
		cat := event.Category
		if cat == "" {
			cat = "Question"
		}
		qp.tabs = []questionTab{{
			id:       "single",
			category: cat,
			question: event.Question,
			options:  opts,
			selected: 0,
		}}
	}

	// Focus text input if first option is the text option
	qp.syncTextInputFocus()
}

// OpenConfirm initializes the panel for a tool permission prompt.
// It shows a preview of what the tool will do and offers only Accept/Deny options.
func (qp *QuestionPanel) OpenConfirm(toolName string, params map[string]any, width int) {
	qp.visible = true
	qp.width = width
	qp.currentTab = 0
	qp.offset = 0
	qp.maxVisible = 8
	qp.confirmMode = true
	qp.preview = buildConfirmPreview(toolName, params)

	qp.tabs = []questionTab{{
		id:       "confirm",
		category: "Permission",
		question: "Allow " + toolName + "?",
		options:  []string{"Yes, allow", "No, deny"},
		selected: 0,
	}}
}

// buildConfirmPreview builds a plain-text preview of the tool operation for display.
func buildConfirmPreview(toolName string, params map[string]any) string {
	const maxLines = 12
	switch toolName {
	case "write_file":
		path, _ := params["path"].(string)
		content, _ := params["content"].(string)
		lines := strings.Split(content, "\n")
		truncated := false
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			truncated = true
		}
		preview := strings.Join(lines, "\n")
		if truncated {
			preview += "\n…"
		}
		return "📄 " + path + "\n" + preview

	case "edit_file":
		path, _ := params["path"].(string)
		old, _ := params["old_string"].(string)
		newStr, _ := params["new_string"].(string)
		oldLines := strings.Split(old, "\n")
		newLines := strings.Split(newStr, "\n")
		var sb strings.Builder
		sb.WriteString("✏️  " + path + "\n")
		for i, l := range oldLines {
			if i >= maxLines/2 {
				sb.WriteString("- …\n")
				break
			}
			sb.WriteString("- " + l + "\n")
		}
		for i, l := range newLines {
			if i >= maxLines/2 {
				sb.WriteString("+ …\n")
				break
			}
			sb.WriteString("+ " + l + "\n")
		}
		return strings.TrimRight(sb.String(), "\n")

	case "delete_file":
		path, _ := params["path"].(string)
		return "🗑️  " + path

	default:
		return ""
	}
}

// QAPair holds a question and its answer for rendering in chat history.
type QAPair struct {
	Category string
	Question string
	Answer   string
}

// GetAnsweredPairs returns the Q&A pairs from the current tabs (call before Close).
func (qp *QuestionPanel) GetAnsweredPairs() []QAPair {
	pairs := make([]QAPair, 0, len(qp.tabs))
	for _, tab := range qp.tabs {
		if tab.answered {
			pairs = append(pairs, QAPair{
				Category: tab.category,
				Question: tab.question,
				Answer:   tab.answer,
			})
		}
	}
	return pairs
}

// CurrentTab returns the current tab's category and question as a QAPair (without answer).
func (qp *QuestionPanel) CurrentTab() QAPair {
	if qp.currentTab < len(qp.tabs) {
		t := qp.tabs[qp.currentTab]
		return QAPair{Category: t.category, Question: t.question}
	}
	return QAPair{Category: "Question"}
}

// CurrentAnswerText returns the answerText of the current tab (for has_user_input options).
func (qp *QuestionPanel) CurrentAnswerText() string {
	if qp.currentTab < len(qp.tabs) {
		return qp.tabs[qp.currentTab].answerText
	}
	return ""
}

// optionCount returns the number of selectable options in the given tab.
func (tab *questionTab) optionCount() int {
	if len(tab.richOptions) > 0 {
		return len(tab.richOptions)
	}
	return len(tab.options)
}

// Close hides the panel and resets state.
func (qp *QuestionPanel) Close() {
	qp.visible = false
	qp.tabs = nil
	qp.currentTab = 0
	qp.confirmMode = false
	qp.preview = ""
}

// IsVisible returns whether the panel is showing.
func (qp *QuestionPanel) IsVisible() bool {
	return qp.visible
}

// SetWidth updates the panel width on terminal resize.
func (qp *QuestionPanel) SetWidth(width int) {
	qp.width = width
}

// isMultiTab returns true if there are multiple question tabs.
func (qp *QuestionPanel) isMultiTab() bool {
	return len(qp.tabs) > 1
}

// currentTabRef returns a pointer to the current tab.
func (qp *QuestionPanel) currentTabRef() *questionTab {
	if qp.currentTab >= 0 && qp.currentTab < len(qp.tabs) {
		return &qp.tabs[qp.currentTab]
	}
	return nil
}

// isOnTextOption returns true if the cursor is on a text-input option.
// For rich options: checks has_user_input on the selected option.
// For simple options: checks if cursor is on the last option ("Type something.").
func (qp *QuestionPanel) isOnTextOption() bool {
	if qp.confirmMode {
		return false
	}
	tab := qp.currentTabRef()
	if tab == nil {
		return false
	}
	if len(tab.richOptions) > 0 {
		return tab.selected < len(tab.richOptions) && tab.richOptions[tab.selected].HasUserInput
	}
	return tab.selected == len(tab.options)-1
}

// syncTextInputFocus focuses or blurs the text input based on cursor position.
func (qp *QuestionPanel) syncTextInputFocus() {
	if qp.confirmMode {
		return
	}
	if qp.isOnTextOption() {
		qp.textInput.Focus()
	} else {
		qp.textInput.Blur()
	}
}

// allAnswered returns true if every tab has been answered.
func (qp *QuestionPanel) allAnswered() bool {
	for _, tab := range qp.tabs {
		if !tab.answered {
			return false
		}
	}
	return true
}

// HandleKey processes a key event and returns (result, singleAnswer, batchAnswers).
func (qp *QuestionPanel) HandleKey(msg tea.KeyPressMsg) (QuestionPanelResult, string, map[string]string) {
	tab := qp.currentTabRef()
	if tab == nil {
		return QPNoop, "", nil
	}

	switch msg.String() {
	case "esc":
		return QPCancelled, "", nil

	case "up":
		if tab.selected > 0 {
			tab.selected--
			// Adjust scroll offset
			if tab.selected < qp.offset {
				qp.offset = tab.selected
			}
			qp.syncTextInputFocus()
		}
		return QPNoop, "", nil

	case "down":
		if tab.selected < tab.optionCount()-1 {
			tab.selected++
			// Adjust scroll offset
			if tab.selected >= qp.offset+qp.maxVisible {
				qp.offset = tab.selected - qp.maxVisible + 1
			}
			qp.syncTextInputFocus()
		}
		return QPNoop, "", nil

	case "left", "ctrl+h":
		if qp.isMultiTab() && qp.currentTab > 0 {
			qp.currentTab--
			qp.offset = 0
			qp.textInput.Reset()
			qp.syncTextInputFocus()
		}
		return QPNoop, "", nil

	case "right", "ctrl+l":
		if qp.isMultiTab() && qp.currentTab < len(qp.tabs)-1 {
			qp.currentTab++
			qp.offset = 0
			qp.textInput.Reset()
			qp.syncTextInputFocus()
		}
		return QPNoop, "", nil

	case "enter":
		return qp.handleEnter()

	}

	// Forward to text input when on text option
	if qp.isOnTextOption() {
		var cmd tea.Cmd
		qp.textInput, cmd = qp.textInput.Update(msg)
		_ = cmd
		return QPNoop, "", nil
	}

	return QPNoop, "", nil
}

// handleEnter processes the Enter key.
func (qp *QuestionPanel) handleEnter() (QuestionPanelResult, string, map[string]string) {
	tab := qp.currentTabRef()
	if tab == nil {
		return QPNoop, "", nil
	}

	// Determine the answer for the current selection
	var answer string
	if len(tab.richOptions) > 0 {
		// Rich options mode
		if tab.selected >= len(tab.richOptions) {
			return QPNoop, "", nil
		}
		opt := tab.richOptions[tab.selected]
		answer = opt.Title
		if opt.HasUserInput {
			text := strings.TrimSpace(qp.textInput.Value())
			if text == "" {
				return QPNoop, "", nil // don't submit empty text on has_user_input
			}
			tab.answerText = text
		}
	} else if qp.isOnTextOption() {
		text := strings.TrimSpace(qp.textInput.Value())
		if text == "" {
			return QPNoop, "", nil // don't submit empty text
		}
		answer = text
	} else {
		answer = tab.options[tab.selected]
	}

	if qp.isMultiTab() {
		// Record answer for current tab
		tab.answer = answer
		tab.answered = true
		qp.textInput.Reset()

		// If all answered, submit
		if qp.allAnswered() {
			answers := make(map[string]string, len(qp.tabs))
			for _, t := range qp.tabs {
				answers[t.id] = t.answer
			}
			return QPSubmitted, "", answers
		}

		// Auto-advance to next unanswered tab
		for i := 0; i < len(qp.tabs); i++ {
			next := (qp.currentTab + 1 + i) % len(qp.tabs)
			if !qp.tabs[next].answered {
				qp.currentTab = next
				qp.offset = 0
				qp.syncTextInputFocus()
				break
			}
		}
		return QPNoop, "", nil
	}

	// Single tab mode — submit immediately
	return QPSubmitted, answer, nil
}

// Height returns the total rendered height of the panel.
func (qp *QuestionPanel) Height() int {
	if !qp.visible || len(qp.tabs) == 0 {
		return 4 // default input height
	}

	tab := qp.currentTabRef()
	if tab == nil {
		return 4
	}

	h := 0
	// Tab bar (multi-tab)
	if qp.isMultiTab() {
		h++ // tab bar line
	}
	h++ // category/blank line
	h++ // question text
	h++ // blank line before options

	// Options (capped by maxVisible)
	visible := tab.optionCount()
	if visible > qp.maxVisible {
		visible = qp.maxVisible
	}
	h += visible

	// Text input line if on text option
	if qp.isOnTextOption() {
		h++ // the text input itself
	}

	h++ // divider
	h++ // help text

	// Preview lines in confirm mode
	if qp.confirmMode && qp.preview != "" {
		h += strings.Count(qp.preview, "\n") + 1 // lines in preview
		h++                                       // blank line after preview
	}

	return h
}

// Render produces the styled panel content.
func (qp *QuestionPanel) Render(s Styles, focused bool) string {
	if !qp.visible || len(qp.tabs) == 0 {
		return ""
	}

	tab := qp.currentTabRef()
	if tab == nil {
		return ""
	}

	var sb strings.Builder
	innerWidth := qp.width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	borderColor := s.ColorBlurBorder
	if focused {
		borderColor = colorSecondary
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)

	// Top border with category or tab bar
	if qp.isMultiTab() {
		tabBar := qp.renderTabBar(s)
		tabLen := lipgloss.Width(tabBar)
		remaining := innerWidth + 2 - tabLen
		if remaining < 0 {
			remaining = 0
		}
		topBorder := borderStyle.Render("╭─") + tabBar + borderStyle.Render(strings.Repeat("─", remaining)+"╮")
		sb.WriteString(topBorder + "\n")
	} else {
		catLabel := " " + tab.category + " "
		catRendered := questionPanelCategoryStyle.Render(catLabel)
		catLen := lipgloss.Width(catRendered)
		remaining := innerWidth + 2 - catLen - 1
		if remaining < 0 {
			remaining = 0
		}
		topBorder := borderStyle.Render("╭─") + catRendered + borderStyle.Render(strings.Repeat("─", remaining)+"╮")
		sb.WriteString(topBorder + "\n")
	}

	// Content lines helper
	writeLine := func(line string) {
		padded := lipgloss.NewStyle().Width(innerWidth).Render(line)
		sb.WriteString(borderStyle.Render("│") + " " + padded + " " + borderStyle.Render("│") + "\n")
	}

	// Preview (confirm mode only)
	if qp.confirmMode && qp.preview != "" {
		previewStyle := lipgloss.NewStyle().Foreground(colorDim)
		for _, line := range strings.Split(qp.preview, "\n") {
			writeLine(previewStyle.Render(line))
		}
		writeLine("")
	}

	// Question text
	writeLine("")
	writeLine(s.QuestionTextStyle.Render(tab.question))
	writeLine("")

	// Options with scrolling
	optCount := tab.optionCount()
	visStart := qp.offset
	visEnd := visStart + qp.maxVisible
	if visEnd > optCount {
		visEnd = optCount
	}

	answeredStyle := lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)

	if len(tab.richOptions) > 0 {
		// Rich options: title in bold, description in dim
		for i := visStart; i < visEnd; i++ {
			opt := tab.richOptions[i]
			num := fmt.Sprintf("%d. ", i+1)

			if tab.answered && i == tab.selected {
				writeLine("  " + answeredStyle.Render("✓ "+num+opt.Title) + "  " + s.QuestionPanelDescStyle.Render(opt.Description))
			} else if i == tab.selected {
				cursor := questionPanelCursorStyle.Render("› ")
				writeLine(cursor + s.QuestionPanelSelectedStyle.Render(num+opt.Title) + "  " + s.QuestionPanelDescStyle.Render(opt.Description))
			} else {
				writeLine("  " + s.QuestionPanelUnselectedStyle.Render(num+opt.Title) + "  " + s.QuestionPanelDescStyle.Render(opt.Description))
			}
		}
	} else {
		// Simple string options
		for i := visStart; i < visEnd; i++ {
			opt := tab.options[i]
			num := fmt.Sprintf("%d. ", i+1)

			if tab.answered && i == tab.selected {
				writeLine("  " + answeredStyle.Render("✓ "+num+opt))
			} else if i == tab.selected {
				cursor := questionPanelCursorStyle.Render("› ")
				writeLine(cursor + s.QuestionPanelSelectedStyle.Render(num+opt))
			} else {
				writeLine("  " + s.QuestionPanelUnselectedStyle.Render(num+opt))
			}
		}
	}

	// Text input area (shown when cursor is on text option)
	if qp.isOnTextOption() {
		inputView := qp.textInput.View()
		writeLine("     " + inputView)
	}

	// Divider
	divider := s.QuestionPanelDividerStyle.Render(strings.Repeat("─", innerWidth))
	sb.WriteString(borderStyle.Render("│") + " " + divider + " " + borderStyle.Render("│") + "\n")

	// Help text
	var help string
	if qp.isMultiTab() {
		help = "Enter to select · ↑/↓ to navigate · ←/→ for tabs · Esc to cancel"
	} else {
		help = "Enter to select · ↑/↓ to navigate · Esc to cancel"
	}
	helpRendered := s.QuestionPanelHelpStyle.Render(help)
	helpPadded := lipgloss.NewStyle().Width(innerWidth).Render(helpRendered)
	sb.WriteString(borderStyle.Render("│") + " " + helpPadded + " " + borderStyle.Render("│") + "\n")

	// Bottom border
	bottomDashes := strings.Repeat("─", innerWidth+2)
	sb.WriteString(borderStyle.Render("╰" + bottomDashes + "╯"))

	return sb.String()
}

// renderTabBar builds the tab bar for multi-question mode.
func (qp *QuestionPanel) renderTabBar(s Styles) string {
	var parts []string
	parts = append(parts, " ")

	for i, tab := range qp.tabs {
		var indicator string
		if tab.answered {
			indicator = questionPanelTabAnsweredStyle.Render("✓")
		} else if i == qp.currentTab {
			indicator = "■"
		} else {
			indicator = "□"
		}

		label := fmt.Sprintf(" %s %s ", indicator, tab.category)
		if i == qp.currentTab {
			parts = append(parts, questionPanelTabActiveStyle.Render(label))
		} else if tab.answered {
			parts = append(parts, questionPanelTabAnsweredStyle.Render(label))
		} else {
			parts = append(parts, s.QuestionPanelTabStyle.Render(label))
		}
	}

	parts = append(parts, " ")
	return strings.Join(parts, "")
}
