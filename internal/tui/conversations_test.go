package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

// The report: sendbird/delight-ops-nest#205 carries an Atlantis plan failure,
// two bot scans and an approval, and ghx said there were no comments. Every one
// of them is a PR-level remark; reviewThreads on that PR is genuinely empty.
func sampleConversations() []pr.Conversation {
	base := time.Date(2026, 8, 30, 16, 37, 0, 0, time.UTC)
	return []pr.Conversation{
		{ID: "IC_1", Kind: "comment", Body: "Ran Plan for dir: `terraform/aws`\n\n**Plan Failed**",
			Author: pr.User{Login: "sendbird-codelight"}, CreatedAt: base},
		{ID: "PRR_1", Kind: "APPROVED", Body: "LGTM!",
			Author: pr.User{Login: "jinsekim"}, CreatedAt: base.Add(3 * time.Minute)},
	}
}

func commentsApp(t *testing.T, threads []pr.ReviewThread, convs []pr.Conversation) *commentsView {
	t.Helper()
	c := newCommentsView()
	c.setThreads(threads)
	c.setConversations(convs)
	return c
}

func TestCommentsTabShowsPRLevelConversation(t *testing.T) {
	c := commentsApp(t, nil, sampleConversations())

	got := c.render(120, 20)
	if strings.Contains(got, "No comments") || strings.Contains(got, "No review threads") {
		t.Fatalf("a PR with 2 conversations rendered as empty:\n%s", got)
	}
	for _, want := range []string{"sendbird-codelight", "jinsekim", "approved"} {
		if !strings.Contains(stripANSI(got), want) {
			t.Errorf("rendered comments tab is missing %q:\n%s", want, stripANSI(got))
		}
	}
}

// A PR with genuinely nothing said about it must still say so.
func TestCommentsTabStillReportsAnEmptyPR(t *testing.T) {
	c := commentsApp(t, nil, nil)
	if got := stripANSI(c.render(120, 20)); !strings.Contains(got, "No comments on this PR") {
		t.Errorf("empty PR rendered as %q", got)
	}
}

// The resolved filter hides finished threads. It must not hide conversations,
// which have no resolution to filter on — hiding them would be asserting they
// were resolved.
func TestResolvedFilterDoesNotHideConversations(t *testing.T) {
	threads := []pr.ReviewThread{{
		ID: "T1", IsResolved: true, ResolutionKnown: true, Path: "a.go", Line: 1,
		Comments: []pr.ThreadComment{{Body: "done", Author: pr.User{Login: "alice"}}},
	}}
	c := commentsApp(t, threads, sampleConversations())

	if c.hideResolved != true {
		t.Fatal("setup: the resolved filter should be on by default")
	}
	if got := c.rowCount(); got != 2 {
		t.Errorf("rowCount = %d with 1 resolved thread hidden and 2 conversations, want 2", got)
	}
	body := stripANSI(c.render(120, 20))
	if !strings.Contains(body, "jinsekim") {
		t.Errorf("the resolved filter hid a conversation:\n%s", body)
	}
	if strings.Contains(body, "All 1 threads are resolved") {
		t.Error("the tab claimed everything was resolved while conversations were listed")
	}
}

// One cursor walks both lists. Past the threads it selects a conversation, and
// the thread-only accessor must decline rather than answer with a thread.
func TestCursorWalksThreadsThenConversations(t *testing.T) {
	threads := []pr.ReviewThread{{
		ID: "T1", ResolutionKnown: true, Path: "a.go", Line: 1,
		Comments: []pr.ThreadComment{{Body: "look", Author: pr.User{Login: "alice"}}},
	}}
	c := commentsApp(t, threads, sampleConversations())

	if _, ok := c.selected(); !ok {
		t.Fatal("cursor at 0 should be on the thread")
	}
	if _, ok := c.selectedConversation(); ok {
		t.Error("cursor at 0 answered as a conversation")
	}

	c.moveCursor(1)
	if _, ok := c.selected(); ok {
		t.Error("cursor on a conversation still answered with a thread — the " +
			"thread-only actions would act on the wrong row")
	}
	cv, ok := c.selectedConversation()
	if !ok {
		t.Fatal("cursor at 1 is not on a conversation")
	}
	if cv.Author.Login != "sendbird-codelight" {
		t.Errorf("selected conversation = %q, want the first one", cv.Author.Login)
	}

	// And it must not walk past the end.
	c.moveCursor(50)
	if c.cursor != c.rowCount()-1 {
		t.Errorf("cursor ran to %d, want it clamped to %d", c.cursor, c.rowCount()-1)
	}
}

// Expanding is per row, and a conversation id must never collide with a thread
// id in the shared map.
//
// The tail of a long body is what distinguishes expanded from collapsed. The
// preview folds newlines into one line, so an early phrase shows either way —
// what only the expansion produces is the text past the preview's width.
func TestExpandingAConversationShowsItsBody(t *testing.T) {
	tail := "the-line-only-the-expansion-shows"
	convs := []pr.Conversation{{
		ID: "IC_1", Kind: "comment",
		Body:   "Ran Plan\n" + strings.Repeat("filler filler filler\n", 12) + tail,
		Author: pr.User{Login: "sendbird-codelight"},
	}}
	c := commentsApp(t, nil, convs)

	if strings.Contains(stripANSI(c.render(120, 40)), tail) {
		t.Fatal("setup: a collapsed row already showed the whole body")
	}

	c.toggleExpand()
	if got := stripANSI(c.render(120, 40)); !strings.Contains(got, tail) {
		t.Errorf("expanding a conversation did not show its body:\n%s", got)
	}
}

// The expansion map is shared with threads, so the prefix is what stops a
// conversation from expanding a thread that happens to share its id.
func TestConversationAndThreadIdsDoNotCollide(t *testing.T) {
	shared := "SAME_ID"
	threads := []pr.ReviewThread{{
		ID: shared, ResolutionKnown: true, Path: "a.go", Line: 1,
		Comments: []pr.ThreadComment{{Body: "thread body", Author: pr.User{Login: "alice"}}},
	}}
	convs := []pr.Conversation{{
		ID: shared, Kind: "comment", Body: "conversation body",
		Author: pr.User{Login: "bot"},
	}}
	c := commentsApp(t, threads, convs)

	// Expand the conversation; the thread must stay collapsed.
	c.moveCursor(1)
	c.toggleExpand()
	if c.expanded[threadIdentity(threads[0])] {
		t.Error("expanding a conversation also expanded the thread with the same id")
	}
	if !c.expanded[conversationIdentity(convs[0])] {
		t.Error("the conversation did not expand")
	}
}

// A review with no message is a state the Overview tab reports. Listing it here
// would put a blank row under every approver's name.
func TestBodilessApprovalsAreNotListed(t *testing.T) {
	c := commentsApp(t, nil, []pr.Conversation{
		{ID: "PRR_1", Kind: "APPROVED", Body: "LGTM!", Author: pr.User{Login: "jinsekim"}},
	})
	if got := c.rowCount(); got != 1 {
		t.Errorf("rowCount = %d, want the one review that had something to say", got)
	}
}
