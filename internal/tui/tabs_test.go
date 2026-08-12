package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// tabsApp builds a list with the given sources and a small cache so the tab
// strip has realistic decorations to render.
func tabsApp(t *testing.T, sources []config.SourceDef, detected map[string]bool) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sources = sources
	a := NewApp(cfg, DefaultKeymap(), gh.NewClient(0))
	a.width, a.height = 160, 40
	rows := []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}}
	caches := make([][]pr.Summary, len(sources))
	for i := range caches {
		caches[i] = rows
	}
	a.list.caches = caches
	a.list.detectedRepos = detected
	a.list.syncListItems()
	return a
}

func visibleTabDigits(t *testing.T, strip string) []string {
	t.Helper()
	// Each tab label starts with its 1-based index followed by a space. Pull
	// out only those leading digits, not the numbers inside (N) counts.
	var digits []string
	for i, r := range strip {
		if r >= '1' && r <= '9' {
			before := ""
			if i > 0 {
				before = string(strip[i-1])
			}
			// A tab index is preceded by '[' (active) or ' ' (inactive); a count
			// digit is preceded by '('.
			if before == "[" || before == " " || i == 0 {
				digits = append(digits, string(r))
			}
		}
	}
	return digits
}

// The reported problem: on a narrow terminal, tabs past the truncation point
// vanished entirely, taking their index hints with them. Every tab's index must
// stay visible so the 1-9 jump keys always have a target.
func TestEveryTabIndexStaysVisibleOnNarrowTerminal(t *testing.T) {
	sources := []config.SourceDef{
		{Name: "ghx", Repo: "keyolk/ghx"},
		{Name: "okx", Repo: "keyolk/okx"},
		{Name: "gcl", Repo: "keyolk/gcl"},
		{Name: "kmd", Repo: "keyolk/kmd"},
		{Name: "tweb", Repo: "keyolk/tweb"},
		{Name: "My PRs", Query: "author:@me state:open"},
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "Assigned", Query: "assignee:@me state:open"},
	}
	a := tabsApp(t, sources, map[string]bool{
		"keyolk/ghx": true, "keyolk/okx": true, "keyolk/gcl": true,
		"keyolk/kmd": true, "keyolk/tweb": true,
	})

	for _, w := range []int{240, 120, 80, 60, 40, 24} {
		strip := a.list.renderTabs(w)
		got := strings.Join(visibleTabDigits(t, strip), "")
		// Truncation must never drop a tab outright; the strip may shorten names
		// but all eight indices remain.
		if got != "12345678" {
			t.Errorf("w=%d strip=%q indices=%q, want 12345678", w, strip, got)
		}
	}
}

// On a wide terminal the full labels — including PR counts and the detected-repo
// marker — must survive. The fix is about narrowing, not about stripping
// decorations when there is room for them.
func TestWideTerminalKeepsFullTabLabels(t *testing.T) {
	sources := []config.SourceDef{
		{Name: "ghx", Repo: "keyolk/ghx"},
		{Name: "My PRs", Query: "author:@me state:open"},
	}
	a := tabsApp(t, sources, map[string]bool{"keyolk/ghx": true})
	strip := a.list.renderTabs(200)
	if !strings.Contains(strip, "ghx*(1)") {
		t.Errorf("wide strip lost the count/marker: %q", strip)
	}
	if !strings.Contains(strip, "My PRs") {
		t.Errorf("wide strip lost the second label: %q", strip)
	}
}

// Decorations drop before names do: a medium width should keep the names and
// indices but shed the (N) counts.
func TestMediumWidthDropsCountsBeforeNames(t *testing.T) {
	sources := []config.SourceDef{
		{Name: "platform-tools", Repo: "sendbird/platform-tools"},
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "Assigned", Query: "assignee:@me state:open"},
		{Name: "Mentioned", Query: "mentions:@me state:open"},
	}
	a := tabsApp(t, sources, map[string]bool{"sendbird/platform-tools": true})

	// Pick a width between the decorated and undecorated totals so decorations
	// are the first thing to go.
	decorated := lipglossWidth(a.list.renderTabs(400))
	// Force the count-bearing labels, then measure with counts stripped.
	withCounts := a.list.renderTabs(400)
	if !strings.Contains(withCounts, "(1)") {
		t.Fatalf("baseline strip has no count: %q (width %d)", withCounts, decorated)
	}

	// 60 cols: counts should be gone but names visible.
	strip := a.list.renderTabs(60)
	if strings.Contains(strip, "(1)") {
		t.Errorf("counts survived at w=60: %q", strip)
	}
	for _, want := range []string{"platform-tools", "My reviews", "Assigned", "Mentioned"} {
		if !strings.Contains(strip, want) {
			t.Errorf("w=60 dropped name %q: %s", want, strip)
		}
	}
}

// The active tab stays distinguishable (brackets / bold) even after truncation,
// so the user still knows which source they are looking at.
func TestActiveTabStaysMarkedAfterTruncation(t *testing.T) {
	sources := []config.SourceDef{
		{Name: "one", Repo: "o/one"},
		{Name: "two", Repo: "o/two"},
		{Name: "three", Repo: "o/three"},
	}
	a := tabsApp(t, sources, nil)
	a.list.curTab = 2 // "three"

	strip := a.list.renderTabs(20)
	// The active tab is bracketed; the inactive ones are not.
	if !strings.Contains(strip, "[3") {
		t.Errorf("active tab lost its bracket after truncation: %q", strip)
	}
}
