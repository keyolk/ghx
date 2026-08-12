package tui

import "testing"

// The shared helper backs every tab strip. The subcommands (admin, actions) had
// the same right-truncation bug as the list and detail views — the highest-
// numbered tabs vanished on a narrow terminal. This exercises the helper
// directly so a regression in any of the three callers surfaces here.

func TestRenderTabStripKeepsEveryIndexOnNarrowTerminal(t *testing.T) {
	tabs := []TabLabel{
		{Name: "Collaborators"},
		{Name: "Branch Protection"},
		{Name: "Releases"},
		{Name: "Branches"},
		{Name: "Tags"},
		{Name: "Webhooks"},
	}
	for _, w := range []int{200, 120, 80, 60, 40, 24} {
		strip := RenderTabStrip(tabs, w)
		got := leadingDigits(strip)
		if got != "123456" {
			t.Errorf("w=%d strip=%q indices=%q, want 123456", w, strip, got)
		}
	}
}

func TestRenderTabStripMarksActiveTab(t *testing.T) {
	tabs := []TabLabel{{Name: "Runs"}, {Name: "Workflows", Active: true}}
	strip := RenderTabStrip(tabs, 200)
	if !contains(strip, "[2") {
		t.Errorf("active tab not bracketed: %q", strip)
	}
}

func TestRenderTabStripEmpty(t *testing.T) {
	if got := RenderTabStrip(nil, 80); got != "" {
		t.Errorf("nil tabs = %q, want empty", got)
	}
	if got := RenderTabStrip([]TabLabel{{Name: "x"}}, 0); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}
}
