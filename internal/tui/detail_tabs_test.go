package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// The reported problem: entering a PR made the tab strip disappear. The detail
// view's renderTabs truncated the joined string from the right, so on a narrow
// terminal tabs 4-6 — Comments, Commits, Checks — vanished entirely.
func TestDetailTabsStayVisibleOnNarrowTerminal(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.comments.setThreads([]pr.ReviewThread{{ID: "T1"}})

	for _, w := range []int{120, 80, 60, 40, 24} {
		strip := a.detail.renderTabs(w)
		got := leadingDigits(strip)
		if got != "123456" {
			t.Errorf("w=%d strip=%q indices=%q, want 123456", w, strip, got)
		}
	}
}

// On a wide terminal the full labels — including per-tab counts — survive.
func TestDetailTabsWideKeepsCounts(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.comments.setThreads([]pr.ReviewThread{{ID: "T1"}})

	strip := a.detail.renderTabs(200)
	if !strings.Contains(strip, "Comments(1)") {
		t.Errorf("wide strip lost the comment count: %q", strip)
	}
}

// The active tab keeps its brackets after truncation.
func TestDetailActiveTabStaysMarkedAfterTruncation(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.setTab(tabComments) // tab 4

	strip := a.detail.renderTabs(20)
	if !strings.Contains(strip, "[4") {
		t.Errorf("active tab lost its bracket: %q", strip)
	}
}

// leadingDigits pulls out tab indices (1-9) preceded by '[' or ' ', so counts
// inside (N) don't contaminate the result.
func leadingDigits(strip string) string {
	var out string
	for i, r := range strip {
		if r < '1' || r > '9' {
			continue
		}
		before := ""
		if i > 0 {
			before = string(strip[i-1])
		}
		if before == "[" || before == " " || i == 0 {
			out += string(r)
		}
	}
	return out
}
