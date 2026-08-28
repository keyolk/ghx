package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// When the thread list came from REST there is no resolution bit anywhere in
// the response. "Unknown" must therefore not collapse into either answer: shown
// as resolved it hides feedback the author still owes a reply to, and shown as
// outstanding it invents work on a thread that was closed weeks ago.

func graphQLThread(id string, resolved bool) pr.ReviewThread {
	return pr.ReviewThread{
		ID: id, Path: "a.go", Line: 1, IsResolved: resolved, ResolutionKnown: true,
		Comments: []pr.ThreadComment{{DatabaseID: 1, Body: "finding", Author: pr.User{Login: "alice"}}},
	}
}

func restThread() pr.ReviewThread {
	// No ID and no ResolutionKnown — exactly what ReviewThreadsREST produces.
	return pr.ReviewThread{
		Path: "a.go", Line: 2,
		Comments: []pr.ThreadComment{{DatabaseID: 2, Body: "finding", Author: pr.User{Login: "bob"}}},
	}
}

func TestUnknownResolutionIsNotShownAsResolved(t *testing.T) {
	v := newCommentsView()
	v.setThreads([]pr.ReviewThread{restThread()})
	out := stripANSISeqs(v.render(120, 10))
	// The state leads the row as a glyph now; the unknown one must not be the
	// resolved one, which is the collapse this whole file exists to prevent.
	if strings.HasPrefix(out, iconThreadResolved) {
		t.Errorf("a thread of unknown resolution renders as resolved: %q", out)
	}
	if !strings.HasPrefix(out, iconThreadUnknown) {
		t.Errorf("the row does not mark the resolution as unknown: %q", out)
	}
	// And it must not read as open either: showing it as outstanding invents
	// work on a thread that may have been closed weeks ago.
	if strings.HasPrefix(out, iconThreadOpen) {
		t.Errorf("a thread of unknown resolution renders as open: %q", out)
	}
}

// The glyph column only means something if the reader can decode it, and an
// unknown-resolution thread survives the default filter — so `?` can appear
// without `t` ever being pressed.
func TestLegendNamesUnknownWithoutPressingT(t *testing.T) {
	v := newCommentsView()
	v.setThreads([]pr.ReviewThread{graphQLThread("T1", false), restThread()})
	if v.hideResolved != true {
		t.Fatal("the default should still hide resolved threads")
	}
	got := stripANSISeqs(v.helpLine())
	if !strings.Contains(got, iconThreadUnknown+" unknown") {
		t.Errorf("the footer does not decode %q while it is on screen: %q",
			iconThreadUnknown, got)
	}
}

// With one state on screen there is nothing to tell apart, and the legend is
// just noise in a footer that is already near the width limit.
func TestLegendIsAbsentWithASingleState(t *testing.T) {
	v := newCommentsView()
	v.setThreads([]pr.ReviewThread{graphQLThread("T1", false), graphQLThread("T2", false)})
	if got := stripANSISeqs(v.helpLine()); strings.Contains(got, "unknown") ||
		strings.Contains(got, iconThreadResolved+" resolved") {
		t.Errorf("the legend shows with only one state on screen: %q", got)
	}
}

// The `t` filter hides resolved threads. Hiding one whose state is unknown
// would be asserting it is resolved.
func TestHideResolvedKeepsUnknownThreadsVisible(t *testing.T) {
	v := newCommentsView()
	v.setThreads([]pr.ReviewThread{
		graphQLThread("T1", true),
		restThread(),
	})
	v.hideResolved = true
	visible := v.visible()
	if len(visible) != 1 {
		t.Fatalf("%d threads visible, want only the unknown one", len(visible))
	}
	if visible[0].ResolutionKnown {
		t.Error("the hidden thread was the wrong one")
	}
}

// The diff overlay renders its own thread summary, so it needs the same rule.
func TestDiffOverlayMarksUnknownResolution(t *testing.T) {
	unknown := renderThreadSummary(restThread(), false)
	if !strings.HasPrefix(unknown, iconThreadUnknown) {
		t.Errorf("diff overlay does not flag the unknown state: %q", unknown)
	}

	resolved := renderThreadSummary(graphQLThread("T1", true), false)
	if !strings.HasPrefix(resolved, iconThreadResolved) {
		t.Errorf("a genuinely resolved thread is not marked resolved: %q", resolved)
	}

	open := renderThreadSummary(graphQLThread("T2", false), false)
	if !strings.HasPrefix(open, iconThreadOpen) {
		t.Errorf("an open thread is not marked open: %q", open)
	}

	// All three must be distinguishable from each other, which is the point.
	if unknown[:len(iconThreadUnknown)] == resolved[:len(iconThreadResolved)] {
		t.Error("unknown and resolved render with the same glyph")
	}
}

// resolveReviewThread is GraphQL-only and needs a thread node id, which a REST
// thread does not have. Pressing X must say so rather than flipping a marker
// that nothing will persist.
func TestResolveRefusesAThreadWithoutGraphQL(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.activeTab = tabComments
	a.detail.comments.setThreads([]pr.ReviewThread{restThread()})

	cmd, handled := a.detail.update(keyMsg("X"))
	if !handled {
		t.Fatal("X was not consumed")
	}
	if cmd == nil {
		t.Fatal("X silently did nothing; it must explain why it cannot resolve")
	}
	msg := cmd()
	err, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("X produced %T, want an error explaining the limitation", msg)
	}
	if !strings.Contains(err.err.Error(), "GraphQL") {
		t.Errorf("the error does not name the cause: %v", err.err)
	}
	// And the local marker must not have moved.
	if a.detail.comments.threads[0].IsResolved {
		t.Error("X flipped a marker that no request will persist")
	}
}

// A GraphQL-sourced thread still resolves normally.
func TestResolveStillWorksWithGraphQLThreads(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.activeTab = tabComments
	a.detail.comments.setThreads([]pr.ReviewThread{graphQLThread("T1", false)})

	if cmd, handled := a.detail.update(keyMsg("X")); !handled || cmd == nil {
		t.Fatal("X should resolve a GraphQL thread")
	}
	if !a.detail.comments.threads[0].IsResolved {
		t.Error("the optimistic flip did not happen")
	}
}
