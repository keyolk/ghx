package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// gh's refusal as Client.exec wraps it: the "gh pr merge:" prefix, gh's failure
// icon, and gh's advice about flags ghx has no way to pass.
const conflictErr = "gh pr merge: X Pull request acme/one#2 is not mergeable: the merge commit cannot be cleanly created.\n" +
	"To have the pull request merged after all the requirements have been met, add the `--auto` flag.\n" +
	"Run the following to resolve the merge conflicts locally:\n" +
	"  gh pr checkout 2 && git fetch origin main && git merge origin/main"

func TestClassifyMergeError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New(conflictErr), "merge conflict"},
		{errors.New("gh pr merge: X Pull request o/n#1 is not mergeable: the base branch policy prohibits the merge."), "blocked by branch protection"},
		{errors.New("gh pr merge: X Pull request o/n#1 is not mergeable: the head branch is not up to date with the base branch."), "branch is behind its base"},
		{errors.New("gh pr merge: GraphQL: At least 1 approving review is required by reviewers with write access. (mergePullRequest)"), "approval required"},
		{errors.New("gh pr merge: GraphQL: Required status check \"build\" is expected. (mergePullRequest)"), "required checks not passing"},
		{errors.New("gh pr merge: GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)"), "branch changed during the merge"},
		{fmt.Errorf("gh pr merge: %w", context.DeadlineExceeded), "timed out"},
		{errors.New("gh pr merge: something new"), "merge failed"},
	}
	for _, c := range cases {
		if got, _ := classifyMergeError(c.err); got != c.want {
			t.Errorf("classify(%q) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestMergeErrorDetailDropsCLIOnlyAdvice(t *testing.T) {
	got := mergeErrorDetail(errors.New(conflictErr))
	if strings.HasPrefix(got, "gh pr merge") || strings.HasPrefix(got, "X ") {
		t.Errorf("detail kept the wrapper: %q", got)
	}
	if strings.Contains(got, "--auto") {
		t.Errorf("detail kept advice ghx cannot act on: %q", got)
	}
	if !strings.Contains(got, "git merge origin/main") {
		t.Errorf("detail lost gh's resolution command: %q", got)
	}
}

// The regression: a refused merge was a four-second footer toast whose reason
// was truncated away. It must open a box that stays until dismissed.
func TestFailedMergeOpensABoxThatStays(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	target := mergeTarget(2)
	confirmAndRun(t, a, confirmMerge, []actionTarget{target})

	item := newMergeFailureItem(target.label(), errors.New(conflictErr))
	a.Update(actionDoneMsg{label: "merged #2", err: errors.New(conflictErr), mergeFailed: &item})

	if a.mergeFailure == nil {
		t.Fatal("a refused merge did not open the failure box")
	}
	if a.pending != nil {
		t.Error("the in-flight marker survived the failure")
	}
	body := stripANSI(a.View())
	for _, want := range []string{"merge failed", "Not merged", "#2 in acme/one", "merge conflict"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered view lacks %q:\n%s", want, body)
		}
	}

	// Unrelated keys must not act on the queue hidden behind the box.
	a.handleKey(keyMsg("M"))
	if a.mergeFailure == nil || a.confirm != nil {
		t.Error("a key reached the view behind the failure box")
	}
	a.handleKey(keyMsg("enter"))
	if a.mergeFailure != nil {
		t.Error("enter did not dismiss the failure box")
	}
}

// A bulk merge that partly worked says both halves and names each failure.
func TestBulkMergeFailureListsEachPR(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	a.list.selected[selectionKey(pr.Summary{Repo: "acme/one", Number: 1})] = pr.Summary{}
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(1), mergeTarget(2), mergeTarget(3)})

	a.Update(bulkActionDoneMsg{
		label:     "merged 1 PR",
		completed: []string{mergeTarget(1).key()},
		err:       errors.New("two failed"),
		mergeFailures: []mergeFailureItem{
			newMergeFailureItem(mergeTarget(2).label(), errors.New(conflictErr)),
			newMergeFailureItem(mergeTarget(3).label(), errors.New("gh pr merge: X Pull request acme/one#3 is not mergeable: the base branch policy prohibits the merge.")),
		},
	})

	if a.mergeFailure == nil {
		t.Fatal("a partly failed bulk merge did not open the failure box")
	}
	body := stripANSI(a.View())
	for _, want := range []string{"2 pull requests were not merged (1 merged)",
		"#2 in acme/one", "merge conflict", "#3 in acme/one", "blocked by branch protection"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered view lacks %q:\n%s", want, body)
		}
	}
}

// The strategy prompt (`:merge`) reports through mergeResultMsg; it must reach
// the same box rather than the old toast.
func TestMergePromptFailureOpensTheBox(t *testing.T) {
	a := listApp(t, rowsFor(1))
	a.mergePrompt = &mergePrompt{strategy: "squash"}
	a.Update(mergeResultMsg{err: errors.New(conflictErr)})
	if a.mergePrompt != nil {
		t.Error("the strategy prompt stayed open")
	}
	if a.mergeFailure == nil || a.mergeFailure.items[0].reason != "merge conflict" {
		t.Errorf("mergeFailure = %+v, want a conflict entry", a.mergeFailure)
	}
}

// Non-merge failures keep the toast; the box is for merges only.
func TestOtherActionFailuresStayToasts(t *testing.T) {
	a := listApp(t, rowsFor(1))
	_, cmd := a.Update(actionDoneMsg{label: "closed #1", err: errors.New("nope")})
	if a.mergeFailure != nil {
		t.Error("a failed close opened the merge failure box")
	}
	if cmd == nil {
		t.Error("a failed close reported nothing")
	}
}
