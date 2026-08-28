package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// A gh failure can carry a multi-line GraphQL document. The footer is one line;
// ansi truncation counts a newline as zero cells, so without flattening the
// message the extra lines are drawn below the frame and push the body off
// screen.
func TestFooterFlattensMultilineToast(t *testing.T) {
	msg := "PR list warning: gh api graphql: signal: killed\nquery($ids:[ID!]!){\n  rateLimit{remaining}\n}"
	got := truncateFooter(msg, 80)
	if strings.Contains(got, "\n") {
		t.Fatalf("footer kept newlines: %q", got)
	}
	if !strings.HasPrefix(got, "PR list warning: gh api graphql") {
		t.Errorf("footer lost its leading text: %q", got)
	}
}

// Width zero is the pre-resize case; it must still not return extra lines.
func TestFooterFlattensBeforeFirstResize(t *testing.T) {
	if got := truncateFooter("a\nb", 0); strings.Contains(got, "\n") {
		t.Fatalf("footer kept newlines at width 0: %q", got)
	}
}

// The whole app frame must stay exactly height rows even when an error toast
// arrives carrying a multi-line message.
func TestViewStaysWithinHeightWithMultilineToast(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Title: "a pull request", Repo: "o/n", State: "OPEN"}})
	a.width, a.height = 100, 24
	a.list.resize(100, 24)
	a.setToast(errorStyle.Render("error: ") +
		"gh api graphql: signal: killed\nquery($ids:[ID!]!){\n  rateLimit\n}")
	if n := strings.Count(a.View(), "\n") + 1; n != a.height {
		t.Errorf("View() rendered %d rows, want %d", n, a.height)
	}
}
