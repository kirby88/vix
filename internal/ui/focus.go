package ui

// FocusState tracks which UI component currently has keyboard focus.
type FocusState int

const (
	FocusEditor FocusState = iota // textarea input has focus
	FocusChat                     // chat viewport has focus (scrollable)
)
