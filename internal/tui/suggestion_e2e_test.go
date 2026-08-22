//go:build e2e

package tui

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// The apply-suggestion path is the one thing in ghx that writes to a branch,
// and every part of it — the thread-to-target mapping, the line arithmetic, the
// createCommitOnBranch call — only exists on the wire. A unit test can pin the
// arithmetic; it cannot tell you the mutation is even accepted, which is how
// the input variable shipped as a JSON string that the server rejects.
//
// So this drives the real App against a real PR:
//
//	GHX_E2E_REPO=keyolk/ghx GHX_E2E_PR=16 go test -tags e2e -run E2E ./internal/tui/
//
// It commits. Point it at a throwaway PR you own.

// e2eProbePath is the disposable file the fixtures anchor to, and staleOID is a
// commit that cannot be head, for the collision check.
const (
	e2eProbePath = "testdata/e2e_suggestion_probe.txt"
	staleOID     = "0000000000000000000000000000000000000001"
)

func e2eTarget(t *testing.T) (string, int) {
	t.Helper()
	repo := os.Getenv("GHX_E2E_REPO")
	num := os.Getenv("GHX_E2E_PR")
	if repo == "" || num == "" {
		t.Skip("set GHX_E2E_REPO and GHX_E2E_PR to run the apply-suggestion E2E")
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		t.Fatalf("GHX_E2E_PR=%q: %v", num, err)
	}
	return repo, n
}

// e2eApp opens the PR through the ordinary detail path and waits for the diff
// and the threads, so the cursor moves over the same rows the user sees.
func e2eApp(t *testing.T, repo string, number int) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	a := NewApp(cfg, DefaultKeymap(), gh.NewClient(60*time.Second))
	a.width, a.height = 200, 60
	a.detail = newPRDetailModel(cfg, a.client, a.km, number, repo)
	a.detail.resize(a.width, a.height-3)
	// Bypass the disk cache: an entry from an earlier run would hide the commit
	// this test just made.
	a.detail.cache = nil
	a.state = viewPRDetail

	drain(t, a, a.detail.fetch())
	if a.detail.detail == nil {
		t.Fatal("PR detail never arrived")
	}
	if len(a.detail.comments.threads) == 0 {
		t.Fatal("no review threads on the PR — post a suggestion comment first")
	}
	return a
}

// drain runs a tea.Cmd tree to completion, feeding every message back into the
// App the way the runtime would. Only the four fetches fan out here, so a plain
// recursive walk is enough.
func drain(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			drain(t, a, c)
		}
		return
	case nil:
		return
	}
	if e, ok := msg.(errMsg); ok {
		t.Fatalf("app reported: %v", e.err)
	}
	_, next := a.Update(msg)
	drain(t, a, next)
}

// cursorToSuggestion parks the diff cursor on the first comment row that both
// carries a suggestion and matches want, and reports the thread it found.
func cursorToSuggestion(t *testing.T, a *App, want func(pr.ReviewThread, pr.Suggestion) bool) pr.ReviewThread {
	t.Helper()
	a.detail.setTab(tabDiff)
	v := a.detail.diff
	for i := range v.rows {
		v.setCursor(i)
		th, sg, ok := v.suggestionUnderCursor()
		if ok && want(th, sg) {
			return th
		}
	}
	t.Fatal("no matching suggestion row found in the diff")
	return pr.ReviewThread{}
}

// applyUnderCursor presses A, confirms with y, and returns the outcome message.
func applyUnderCursor(t *testing.T, a *App) suggestionAppliedMsg {
	t.Helper()
	cmd := a.detail.requestApplySuggestion()
	if cmd == nil {
		t.Fatal("A produced no command")
	}
	switch m := cmd().(type) {
	case openSuggestionMsg:
		a.suggestion = m.prompt
	case errMsg:
		t.Fatalf("A refused: %v", m.err)
	default:
		t.Fatalf("A produced %T", m)
	}
	// The confirmation is not a formality here: it is the only thing between a
	// keypress and a commit, so the test goes through it rather than around it.
	confirm := a.handleSuggestionKey(keyMsg("y"))
	if confirm == nil {
		t.Fatal("y produced no command")
	}
	out, ok := confirm().(suggestionAppliedMsg)
	if !ok {
		t.Fatalf("apply produced %T", confirm())
	}
	return out
}

// fileAtHead reads the file from the head *branch*, not from the OID the PR
// reports. Those disagree right after a commit: pullRequest.headRefOid lags the
// ref by a second or two, so reading at it would show the state before the
// write and read as "the commit did not land".
func fileAtHead(t *testing.T, a *App, repo, path string) string {
	t.Helper()
	owner, name, _ := strings.Cut(repo, "/")
	head, err := a.detail.client.PRHeadRef(t.Context(), a.detail.number)
	if err != nil {
		t.Fatalf("resolve head: %v", err)
	}
	body, err := a.detail.client.FileAtRef(t.Context(), owner, name, head.Branch, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

// TestE2EApplySingleLineSuggestion is the whole path: a real thread, the real
// mapping, a real commit, and a byte comparison of what landed.
func TestE2EApplySingleLineSuggestion(t *testing.T) {
	repo, number := e2eTarget(t)
	a := e2eApp(t, repo, number)

	th := cursorToSuggestion(t, a, func(th pr.ReviewThread, sg pr.Suggestion) bool {
		_, _, ranged := threadRange(th)
		return !ranged && !sg.Deletion
	})
	before := fileAtHead(t, a, repo, th.Path)

	out := applyUnderCursor(t, a)
	if out.err != nil {
		t.Fatalf("apply failed: %v", out.err)
	}

	after := fileAtHead(t, a, repo, th.Path)
	if after == before {
		t.Fatal("the commit reported success but the file at head is unchanged")
	}
	// The final newline is the byte that a naive split/join drops, and losing it
	// shows up as a whole-file diff on the next push rather than as an error.
	if strings.HasSuffix(before, "\n") && !strings.HasSuffix(after, "\n") {
		t.Error("the trailing newline was lost")
	}
	line := threadLine(th)
	got := strings.Split(after, "\n")
	if line-1 >= len(got) {
		t.Fatalf("file has %d lines, suggestion targeted %d", len(got), line)
	}
	t.Logf("line %d is now %q", line, got[line-1])
	if len(strings.Split(before, "\n")) != len(got) {
		t.Errorf("a single-line replacement changed the line count: %d → %d",
			len(strings.Split(before, "\n")), len(got))
	}
}

// TestE2EApplyRangeSuggestion covers the multi-line anchor, where an off-by-one
// in either bound silently eats or duplicates a line.
func TestE2EApplyRangeSuggestion(t *testing.T) {
	repo, number := e2eTarget(t)
	a := e2eApp(t, repo, number)

	var lo, hi int
	th := cursorToSuggestion(t, a, func(th pr.ReviewThread, sg pr.Suggestion) bool {
		l, h, ranged := threadRange(th)
		lo, hi = l, h
		return ranged && !sg.Deletion
	})
	before := strings.Split(fileAtHead(t, a, repo, th.Path), "\n")

	out := applyUnderCursor(t, a)
	if out.err != nil {
		t.Fatalf("apply failed: %v", out.err)
	}

	after := strings.Split(fileAtHead(t, a, repo, th.Path), "\n")
	// Everything outside [lo,hi] must survive untouched; that is the assertion a
	// unit test on ReplaceLines cannot make about the real anchor coordinates.
	for i := 0; i < lo-1; i++ {
		if before[i] != after[i] {
			t.Errorf("line %d changed but is above the range: %q → %q", i+1, before[i], after[i])
		}
	}
	tailBefore := before[hi:]
	tailAfter := after[len(after)-len(tailBefore):]
	for i := range tailBefore {
		if tailBefore[i] != tailAfter[i] {
			t.Errorf("a line below the range changed: %q → %q", tailBefore[i], tailAfter[i])
		}
	}
	t.Logf("range %d-%d became %q", lo, hi, strings.Join(after[lo-1:len(after)-len(tailBefore)], "\\n"))
}

// TestE2EApplyDeletionSuggestion covers the empty block, which removes lines.
func TestE2EApplyDeletionSuggestion(t *testing.T) {
	repo, number := e2eTarget(t)
	a := e2eApp(t, repo, number)

	th := cursorToSuggestion(t, a, func(_ pr.ReviewThread, sg pr.Suggestion) bool {
		return sg.Deletion
	})
	before := strings.Split(fileAtHead(t, a, repo, th.Path), "\n")

	out := applyUnderCursor(t, a)
	if out.err != nil {
		t.Fatalf("apply failed: %v", out.err)
	}

	after := strings.Split(fileAtHead(t, a, repo, th.Path), "\n")
	if len(after) >= len(before) {
		t.Errorf("a deletion did not shorten the file: %d → %d lines", len(before), len(after))
	}
}

// TestE2EStaleHeadIsRejected is the safety property the whole design rests on:
// expectedHeadOid must make a write against a moved branch fail rather than
// discard whatever arrived in between. It is asserted at the client, because
// staging a genuine mid-apply race would mean racing GitHub.
func TestE2EStaleHeadIsRejected(t *testing.T) {
	repo, number := e2eTarget(t)
	owner, name, _ := strings.Cut(repo, "/")
	client := gh.NewClient(60 * time.Second).WithRepo(repo)

	head, err := client.PRHeadRef(t.Context(), number)
	if err != nil {
		t.Fatalf("resolve head: %v", err)
	}
	body, err := client.FileAtRef(t.Context(), owner, name, head.OID, e2eProbePath)
	if err != nil {
		t.Fatalf("read probe file: %v", err)
	}
	_ = body

	if err := client.ApplySuggestionAtHead(t.Context(), number, staleOID,
		gh.SuggestionTarget{Path: e2eProbePath, Line: 1},
		"stale-write-should-not-land", false); err == nil {
		t.Fatal("a commit against a stale expectedHeadOid was accepted")
	} else {
		t.Logf("rejected as expected: %v", err)
	}

	after, err := client.FileAtRef(t.Context(), owner, name, head.OID, e2eProbePath)
	if err != nil {
		t.Fatalf("re-read probe file: %v", err)
	}
	if after != body {
		t.Error("the rejected write changed the file anyway")
	}
}

// TestE2EOutdatedSuggestionIsRefused pins the guard that matters most here.
// Applying a suggestion moves the branch, which makes its own thread outdated:
// GitHub drops `line` to null and only `originalLine` survives. Those old
// coordinates still resolve against the current file — and the row does not
// even render as an orphan, because originalLine happens to exist in the new
// diff — so nothing but isOutdated distinguishes it from a live suggestion.
func TestE2EOutdatedSuggestionIsRefused(t *testing.T) {
	repo, number := e2eTarget(t)
	a := e2eApp(t, repo, number)

	var found int
	for _, th := range a.detail.comments.threads {
		if !th.IsOutdated || len(th.Comments) == 0 {
			continue
		}
		if !pr.HasSuggestion(th.Comments[0].Body) {
			continue
		}
		found++
		// Park the cursor on this thread's row and press A the way a user would.
		a.detail.setTab(tabDiff)
		v := a.detail.diff
		placed := false
		for i := range v.rows {
			if v.rows[i].threadID != th.ID || v.rows[i].commentIdx != 0 {
				continue
			}
			v.setCursor(i)
			placed = true
			break
		}
		if !placed {
			t.Fatalf("outdated thread %s has no row in the diff", th.ID)
		}
		msg := a.detail.requestApplySuggestion()()
		e, ok := msg.(errMsg)
		if !ok {
			t.Fatalf("A on an outdated suggestion produced %T, want a refusal", msg)
		}
		if !strings.Contains(e.err.Error(), "outdated") {
			t.Errorf("refused for the wrong reason: %v", e.err)
		}
		t.Logf("refused: %v", e.err)
	}
	if found == 0 {
		t.Skip("no outdated suggestion thread on this PR — apply one first")
	}
}
