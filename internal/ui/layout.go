package ui

import (
	"image"

	uv "github.com/charmbracelet/ultraviolet"
)

// sidebarWidth is the fixed column count reserved for the right info panel.
const sidebarWidth = 30

// minChatWidth is the minimum chat area width; below this, the sidebar is suppressed.
const minChatWidth = 20

// Layout holds the computed vertical and horizontal dimensions for the UI.
type Layout struct {
	Width           int
	ChatHeight      int
	InputHeight     int
	StatusBarHeight int
	PanelHeight     int
	SidebarWidth    int // fixed width reserved for the right info panel (0 = hidden)
	ChatWidth       int // Width - SidebarWidth; horizontal space for the chat box
}

// computeLayout calculates the vertical space allocation.
// panelHeights are optional heights for attachment panel, history panel, etc.
func computeLayout(width, height, inputLineCount int, panelHeights ...int) Layout {
	const statusBarHeight = 2

	// Input area: textarea lines + 2 for top/bottom border
	inputHeight := inputLineCount + 2
	if inputHeight < 3 {
		inputHeight = 3
	}

	// Sum panel heights (attachment, history, mode warning, etc.)
	panelHeight := 0
	for _, h := range panelHeights {
		panelHeight += h
	}

	chatHeight := height - inputHeight - statusBarHeight - panelHeight
	if chatHeight < 3 {
		chatHeight = 3
	}

	// Determine sidebar and chat widths.
	// Suppress the sidebar when the terminal is too narrow.
	sw := sidebarWidth
	cw := width - sw
	if cw < minChatWidth {
		sw = 0
		cw = width
	}

	return Layout{
		Width:           width,
		ChatHeight:      chatHeight,
		InputHeight:     inputHeight,
		StatusBarHeight: statusBarHeight,
		PanelHeight:     panelHeight,
		SidebarWidth:    sw,
		ChatWidth:       cw,
	}
}

// centerRect returns a rectangle centered within the given area.
func centerRect(area uv.Rectangle, width, height int) uv.Rectangle {
	cx := area.Min.X + area.Dx()/2
	cy := area.Min.Y + area.Dy()/2
	return image.Rect(cx-width/2, cy-height/2, cx-width/2+width, cy-height/2+height)
}
