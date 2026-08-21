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
	out := v.render(120, 10)
	if strings.Contains(out, "[resolved]") {
		t.Errorf("a thread of unknown resolution was tagged resolved: %q", out)
	}
	if !strings.Contains(out, "resolution unknown") {
		t.Errorf("the row does not say the resolution is unknown: %q", out)
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
	summary := renderThreadSummary(restThread(), false)
	if strings.Contains(summary, "[resolved]") {
		t.Errorf("diff overlay tagged an unknown thread resolved: %q", summary)
	}
	if !strings.Contains(summary, "resolution unknown") {
		t.Errorf("diff overlay does not flag the unknown state: %q", summary)
	}
	known := renderThreadSummary(graphQLThread("T1", true), false)
	if !strings.Contains(known, "[resolved]") {
		t.Errorf("a genuinely resolved thread lost its tag: %q", known)
	}
	if strings.Contains(known, "unknown") {
		t.Errorf("a known thread was flagged unknown: %q", known)
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
