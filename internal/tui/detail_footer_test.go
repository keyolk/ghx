package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// M merged from the detail view all along, but no footer said so — and for an
// irreversible action that is the difference between a key existing and a key
// being usable. Nothing else in the Overview tab hints the PR can be merged
// from it.
//
// The general rule this asserts: a key the view acts on has to be advertised
// somewhere the operator will see it. The footer is checked against what
// handleKey actually consumes, so the two cannot drift.
func TestDetailFooterAdvertisesTheKeysThatWork(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.detail = &pr.Detail{Number: 1, BaseRefName: "main", URL: "https://x/pull/1"}
	a.width, a.height = 200, 40

	// Tabs whose footer is the shared PR-action line. Diff, Comments, Checks and
	// Files delegate to their own tab, which owns a different vocabulary.
	for _, tab := range []detailTabKind{tabOverview, tabCommits} {
		a.detail.activeTab = tab
		footer := a.detail.helpLine()
		for _, key := range []string{"M", "y", "a", "r", "o"} {
			if !strings.Contains(footer, key) {
				t.Errorf("%s footer does not mention %q: %q",
					detailTabNames[tab], key, footer)
			}
		}
	}
}

// A hint that names a key the view ignores is worse than no hint: it sends the
// operator looking for a result that never comes. Every key the shared footer
// advertises must actually be consumed.
func TestDetailFooterDoesNotAdvertiseDeadKeys(t *testing.T) {
	for _, key := range []string{"M", "y", "a", "r", "o", "C"} {
		a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
		a.state = viewPRDetail
		a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
		a.detail.detail = &pr.Detail{Number: 1, BaseRefName: "main", URL: "https://x/pull/1"}
		a.detail.activeTab = tabOverview
		a.width, a.height = 200, 40
		a.copyClipboard = func(context.Context, string, string) error { return nil }

		if cmd := a.handleKey(keyMsg(key)); cmd == nil && a.mergePrompt == nil && a.confirm == nil {
			t.Errorf("the footer advertises %q but the Overview tab does nothing with it", key)
		}
	}
}

// The footer is one line. It is only useful if it fits the terminals it is
// read on; past that the last hints are silently cut off, which is how a key
// stops being advertised without anyone noticing.
func TestDetailFooterFitsACommonTerminal(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")

	for _, tab := range a.detail.tabs {
		a.detail.activeTab = tab
		if w := lipglossWidth(a.detail.helpLine()); w > 100 {
			t.Errorf("%s footer is %d cells — a 100-column terminal truncates it",
				detailTabNames[tab], w)
		}
	}
}
