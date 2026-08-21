package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/pr"
)

// countBatch reports how many commands a tea.Cmd expands to. load() is a batch
// of four; a targeted refresh is one.
func countBatch(cmd tea.Cmd) int {
	if cmd == nil {
		return 0
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		return len(msg)
	default:
		return 1
	}
}

func detailForRefetch(t *testing.T) (*App, *prDetailModel) {
	t.Helper()
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	a := testApp(t, []pr.Summary{
		{Number: 42, Repo: "acme/one", State: "OPEN", UpdatedAt: stamp},
	})
	a.width, a.height = 160, 45
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 42, "acme/one")
	a.detail.updatedAt = stamp
	a.detail.detail = &pr.Detail{Number: 42, BaseRefName: "main"}
	return a, a.detail
}

// Resolving a thread re-fetched the diff, the commits, and the checks to learn
// that one thread had closed. Opening a PR is the most expensive thing ghx
// does, and every action inside it was paying that price in full.
func TestResolvingAThreadRefetchesOnlyThreads(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, _ := detailForRefetch(t)

	_, cmd := a.Update(threadResolvedMsg{})
	if n := countBatch(cmd); n != 1 {
		t.Errorf("resolving a thread issued %d requests, want 1 (threads only)", n)
	}
}

// A review changes the decision and the review list — both PR metadata. The
// diff cannot have changed.
func TestPostingAReviewRefetchesOnlyTheDetail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, _ := detailForRefetch(t)

	_, cmd := a.Update(reviewPostedMsg{action: "approve"})
	if n := countBatch(cmd); n != 1 {
		t.Errorf("approving issued %d requests, want 1 (detail only)", n)
	}
}

// ready/draft, close/reopen, and label edits are all metadata too.
func TestSimpleActionsRefetchOnlyTheDetail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, _ := detailForRefetch(t)

	_, cmd := a.Update(actionDoneMsg{label: "ready #42"})
	if n := countBatch(cmd); n != 1 {
		t.Errorf("a ready toggle issued %d requests, want 1 (detail only)", n)
	}
}

// R and :refresh mean "I do not trust what is on screen", which is the one case
// that has to re-read everything.
func TestExplicitRefreshStillFetchesEverything(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, _ := detailForRefetch(t)

	cmd, handled := a.detailActionKey(keyMsg("R"))
	if !handled {
		t.Fatal("R was not handled in the detail view")
	}
	if n := countBatch(cmd); n != 4 {
		t.Errorf("R issued %d requests, want all 4", n)
	}

	if n := countBatch(a.runPalette("refresh")); n != 4 {
		t.Errorf(":refresh issued %d requests, want all 4", n)
	}
}

// The narrowed refreshes must still invalidate the stored entry. The row's
// updatedAt does not move when the action is taken from inside ghx, so a cached
// copy would keep serving the state that just stopped being true — the comment
// posted, the thread resolved, the PR marked ready.
func TestTargetedRefreshesEvictTheCachedEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name    string
		refresh func(*prDetailModel) tea.Cmd
	}{
		{"threads", (*prDetailModel).refreshThreads},
		{"detail", (*prDetailModel).refreshDetail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed := detailForCache(t, stamp)
			seed.number = 42
			primeDetail(seed, t)
			seed.saveCache()

			acted := detailForCache(t, stamp)
			acted.number = 42
			tc.refresh(acted)

			next := detailForCache(t, stamp)
			next.number = 42
			if cmd := next.load(); cmd == nil {
				t.Error("the stale entry survived; the next visit would show the pre-action PR")
			}
		})
	}
}
