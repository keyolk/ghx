package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/split"
)

// Pane frames were painted with hand-built truecolor escapes, which ignore the
// colour profile and survive SetNoColor: with NO_COLOR every box and the split
// divider still came out coloured.
func TestPaneFramesHonourNoColor(t *testing.T) {
	saved := []*lipgloss.Style{
		&paneBorderStyle, &paneBorderFocusedStyle, &paneBorderErrorStyle, &paneLabelStyle,
		&selectedRowStyle, &diffCursorStyle,
	}
	snapshot := make([]lipgloss.Style, len(saved))
	for i, s := range saved {
		snapshot[i] = *s
	}
	savedProfile := lipgloss.ColorProfile()
	t.Cleanup(func() {
		for i, s := range saved {
			*s = snapshot[i]
		}
		lipgloss.SetColorProfile(savedProfile)
	})

	SetNoColor()

	renders := map[string]string{
		"focused pane": decoratedPane("label", "body", 20, 4, true),
		"dim pane":     decoratedPane("label", "body", 20, 4, false),
		"failure box":  borderedPane("merge failed", "body", 30, 4, paneBorderErrorStyle),
		"split":        split.Divider(split.Horizontal, mouse.Rect{W: 1, H: 2}, paneBorderFocusedStyle, false),
	}
	for name, out := range renders {
		if strings.Contains(out, "\x1b[38;") {
			t.Errorf("%s carries a foreground colour under NO_COLOR: %q", name, out)
		}
	}
}

// With colour available the focused frame must still be coloured: the fix is
// to route through the styles, not to drop the colour.
func TestPaneFramesAreColouredWhenColourIsOn(t *testing.T) {
	forceColor(t)
	out := decoratedPane("label", "body", 20, 4, true)
	if !strings.Contains(out, "\x1b[38;") {
		t.Errorf("focused pane lost its border colour: %q", out)
	}
	if !strings.Contains(out, "─") || !strings.Contains(out, "label") {
		t.Errorf("frame or label missing: %q", out)
	}
}
