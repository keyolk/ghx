package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

const suggestionBody = "Better the other way:\n\n```suggestion\nnew line\n```\n"

func suggestionApp(t *testing.T, threads []pr.ReviewThread) (*App, *diffView) {
	t.Helper()
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.detail = &pr.Detail{Number: 1, URL: "https://x/1"}
	a.detail.activeTab = tabDiff
	raw := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,3 +1,3 @@\n context\n+added\n+second\n"
	if err := a.detail.diff.setContent(raw, threads); err != nil {
		t.Fatal(err)
	}
	a.detail.comments.setThreads(threads)
	return a, a.detail.diff
}

func threadWithSuggestion(line int) pr.ReviewThread {
	return pr.ReviewThread{
		ID: "T1", Path: "a.txt", Line: line, DiffSide: "RIGHT",
		ResolutionKnown: true,
		Comments: []pr.ThreadComment{{
			ID: "C1", DatabaseID: 1, Body: suggestionBody,
			Author: pr.User{Login: "reviewer"},
		}},
	}
}

// cursorOnThread puts the cursor on the thread's opening row.
func cursorOnThread(t *testing.T, v *diffView) {
	t.Helper()
	for i, r := range v.rows {
		if r.kind == rowThread && r.commentIdx == 0 {
			v.cursor = i
			return
		}
	}
	t.Fatal("no thread row in the diff")
}

// A applies the suggestion under the cursor — but only after showing what it
// would write, since this is the one action in the diff view that commits to
// someone's branch.
func TestApplySuggestionOpensAConfirmation(t *testing.T) {
	a, v := suggestionApp(t, []pr.ReviewThread{threadWithSuggestion(2)})
	cursorOnThread(t, v)

	cmd := a.handleKey(keyMsg("A"))
	if cmd == nil {
		t.Fatal("A produced no command")
	}
	msg := cmd()
	open, ok := msg.(openSuggestionMsg)
	if !ok {
		if e, isErr := msg.(errMsg); isErr {
			t.Fatalf("A failed: %v", e.err)
		}
		t.Fatalf("A produced %T, want a confirmation", msg)
	}
	a.Update(open)
	if a.suggestion == nil {
		t.Fatal("the confirmation did not open")
	}
	if a.suggestion.suggestion.Replacement != "new line" {
		t.Errorf("prompt carries %q", a.suggestion.suggestion.Replacement)
	}
}

// The prompt must show the replacement before it is written; a suggestion
// anchored to a line that has since moved would otherwise be committed
// somewhere the reviewer never looked.
func TestSuggestionPromptShowsTheChange(t *testing.T) {
	a, v := suggestionApp(t, []pr.ReviewThread{threadWithSuggestion(2)})
	cursorOnThread(t, v)
	if cmd := a.handleKey(keyMsg("A")); cmd != nil {
		if open, ok := cmd().(openSuggestionMsg); ok {
			a.Update(open)
		}
	}
	if a.suggestion == nil {
		t.Fatal("no prompt")
	}
	out := a.renderSuggestionPrompt(100, 30)
	for _, want := range []string{"a.txt", "new line", "cannot be undone"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prompt does not mention %q:\n%s", want, out)
		}
	}
}

// n cancels without writing anything.
func TestSuggestionPromptCancels(t *testing.T) {
	a, v := suggestionApp(t, []pr.ReviewThread{threadWithSuggestion(2)})
	cursorOnThread(t, v)
	if cmd := a.handleKey(keyMsg("A")); cmd != nil {
		if open, ok := cmd().(openSuggestionMsg); ok {
			a.Update(open)
		}
	}
	if cmd := a.handleKey(keyMsg("n")); cmd != nil {
		t.Error("cancelling issued a command")
	}
	if a.suggestion != nil {
		t.Error("n left the prompt open")
	}
}

// While a commit is in flight, further keys must not start a second one.
func TestSuggestionPromptIgnoresKeysWhileBusy(t *testing.T) {
	a, v := suggestionApp(t, []pr.ReviewThread{threadWithSuggestion(2)})
	cursorOnThread(t, v)
	if cmd := a.handleKey(keyMsg("A")); cmd != nil {
		if open, ok := cmd().(openSuggestionMsg); ok {
			a.Update(open)
		}
	}
	a.suggestion.busy = true
	if cmd := a.handleKey(keyMsg("y")); cmd != nil {
		t.Error("a second y started another commit")
	}
	if a.suggestion == nil {
		t.Error("a keypress dismissed a prompt with a commit in flight")
	}
}

// A comment with no suggestion is not applicable, and A must say so rather than
// doing nothing.
func TestApplySuggestionRefusesAPlainComment(t *testing.T) {
	plain := threadWithSuggestion(2)
	plain.Comments[0].Body = "just a remark"
	a, v := suggestionApp(t, []pr.ReviewThread{plain})
	cursorOnThread(t, v)

	cmd := a.handleKey(keyMsg("A"))
	if cmd == nil {
		t.Fatal("A produced no command")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Error("A on a plain comment did not explain itself")
	}
}

// A suggestion on a deleted line has nothing in the new file to replace.
func TestApplySuggestionRefusesALeftSideAnchor(t *testing.T) {
	left := threadWithSuggestion(2)
	left.DiffSide = "LEFT"
	a, v := suggestionApp(t, []pr.ReviewThread{left})
	cursorOnThread(t, v)

	cmd := a.handleKey(keyMsg("A"))
	if cmd == nil {
		t.Fatal("A produced no command")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Error("a suggestion on a deleted line was accepted")
	}
}

// A resolved thread's suggestion has usually already been dealt with; applying
// it again would undo whatever replaced it.
func TestApplySuggestionRefusesAResolvedThread(t *testing.T) {
	done := threadWithSuggestion(2)
	done.IsResolved = true
	a, v := suggestionApp(t, []pr.ReviewThread{done})
	cursorOnThread(t, v)

	cmd := a.handleKey(keyMsg("A"))
	if cmd == nil {
		t.Fatal("A produced no command")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Error("a resolved thread's suggestion was accepted")
	}
}

// The collapsed row has to say a suggestion is there. Otherwise the only way to
// find one is to expand every thread and read it.
func TestSuggestionIsMarkedOnTheCollapsedRow(t *testing.T) {
	_, v := suggestionApp(t, []pr.ReviewThread{threadWithSuggestion(2)})
	var row string
	for i, r := range v.rows {
		if r.kind == rowThread && r.commentIdx == 0 {
			row = v.renderRow(i, 200)
			break
		}
	}
	if !strings.Contains(row, "suggestion") {
		t.Errorf("the thread row does not advertise its suggestion: %q", row)
	}
}

// A reply that quotes a suggestion is a counter-proposal, and the thread's
// anchor belongs to the opening comment — applying the reply's block there
// would write the wrong lines.
func TestApplySuggestionIgnoresARepliesBlock(t *testing.T) {
	thread := threadWithSuggestion(2)
	thread.Comments[0].Body = "what about this?"
	thread.Comments = append(thread.Comments, pr.ThreadComment{
		ID: "C2", DatabaseID: 2, Body: suggestionBody,
		Author: pr.User{Login: "other"},
	})
	a, v := suggestionApp(t, []pr.ReviewThread{thread})
	cursorOnThread(t, v)

	cmd := a.handleKey(keyMsg("A"))
	if cmd == nil {
		t.Fatal("A produced no command")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Error("a reply's suggestion was applied at the opening comment's anchor")
	}
}
