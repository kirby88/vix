package ui

import (
	"image/color"
	"math/rand/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

const (
	animFPS            = 20
	animInitialChar    = '.'
	animMaxBirthOffset = time.Second
	animNumChars       = 12
	animPreFrames      = 10
)

var animRunes = []rune("0123456789abcdefABCDEF~!@#$%^&*()+=_")

// Animation gradient endpoints — derived from brand colors in styles.go.
var (
	animColorA = lipgloss.Color(primaryHex)
	animColorB = lipgloss.Color(secondaryHex)
)

// animStepMsg triggers the next animation frame.
type animStepMsg struct{}

// ThinkingAnim renders random cycling characters in a purple-to-pink gradient.
type ThinkingAnim struct {
	startTime     time.Time
	birthOffsets  []time.Duration
	initialFrames [][]string // pre-rendered '.' frames
	cyclingFrames [][]string // pre-rendered random char frames
	step          int
	active        bool
}

// NewThinkingAnim creates a new animation.
func NewThinkingAnim() ThinkingAnim {
	ramp := makeGradient(animNumChars, animColorA, animColorB)

	initial := make([][]string, animPreFrames)
	for i := range initial {
		initial[i] = make([]string, animNumChars)
		for j := range initial[i] {
			initial[i][j] = lipgloss.NewStyle().
				Foreground(ramp[j]).
				Render(string(animInitialChar))
		}
	}

	cycling := make([][]string, animPreFrames)
	for i := range cycling {
		cycling[i] = make([]string, animNumChars)
		for j := range cycling[i] {
			r := animRunes[rand.IntN(len(animRunes))]
			cycling[i][j] = lipgloss.NewStyle().
				Foreground(ramp[j]).
				Render(string(r))
		}
	}

	offsets := make([]time.Duration, animNumChars)
	for i := range offsets {
		offsets[i] = time.Duration(rand.N(int64(animMaxBirthOffset))) * time.Nanosecond
	}

	return ThinkingAnim{
		initialFrames: initial,
		cyclingFrames: cycling,
		birthOffsets:  offsets,
	}
}

// Start activates the animation and resets timing.
func (a *ThinkingAnim) Start() tea.Cmd {
	a.active = true
	a.step = 0
	a.startTime = time.Now()
	// Regenerate birth offsets for fresh stagger each time
	for i := range a.birthOffsets {
		a.birthOffsets[i] = time.Duration(rand.N(int64(animMaxBirthOffset))) * time.Nanosecond
	}
	return a.tick()
}

// Stop deactivates the animation.
func (a *ThinkingAnim) Stop() {
	a.active = false
}

// Advance moves to the next frame and returns a tick command.
func (a *ThinkingAnim) Advance() tea.Cmd {
	if !a.active {
		return nil
	}
	a.step = (a.step + 1) % animPreFrames
	return a.tick()
}

// View renders the current animation frame with left padding.
func (a *ThinkingAnim) View() string {
	if !a.active {
		return ""
	}
	var b strings.Builder
	b.WriteString("  ") // indent to align with chat content
	elapsed := time.Since(a.startTime)
	step := a.step
	for i := range animNumChars {
		if elapsed < a.birthOffsets[i] {
			b.WriteString(a.initialFrames[step][i])
		} else {
			b.WriteString(a.cyclingFrames[step][i])
		}
	}
	return b.String()
}

func (a *ThinkingAnim) tick() tea.Cmd {
	return tea.Tick(time.Second/animFPS, func(time.Time) tea.Msg {
		return animStepMsg{}
	})
}

// makeGradient blends two colors into a ramp of the given size.
func makeGradient(size int, a, b color.Color) []color.Color {
	ca, _ := colorful.MakeColor(a)
	cb, _ := colorful.MakeColor(b)
	ramp := make([]color.Color, size)
	for i := range ramp {
		t := float64(i) / float64(size-1)
		ramp[i] = ca.BlendHcl(cb, t)
	}
	return ramp
}
