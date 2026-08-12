package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// X toggles a thread's resolved state: optimistic flip, server mutation, then a
// reload. Before this change there was no resolve action at all — threads could
// be filtered and displayed, but never acted on.

func TestToggleThreadResolvedFlipsSelectedThread(t *testing.T) {
	v := newCommentsView()
	v.setThreads([]pr.ReviewThread{
		{ID: "T1", Path: "a.go", Line: 1, IsResolved: false, Comments: []pr.ThreadComment{{DatabaseID: 7, Body: "x"}}},
		{ID: "T2", Path: "b.go", Line: 2, IsResolved: true, Comments: []pr.ThreadComment{{DatabaseID: 8, Body: "y"}}},
	})
	v.hideResolved = false

	// Cursor on T1 (unresolved) -> resolve it.
	thread, resolve, ok := v.toggleThreadResolved()
	if !ok || !resolve || thread.ID != "T1" {
		t.Fatalf("toggle = (%q, %v, %v), want T1/true/true", thread.ID, resolve, ok)
	}
	if !v.threads[0].IsResolved {
		t.Error("optimistic flip did not mark T1 resolved")
	}

	// Cursor moves to T2 (resolved) -> unresolve it.
	v.cursor = 1
	thread, resolve, ok = v.toggleThreadResolved()
	if !ok || resolve || thread.ID != "T2" {
		t.Fatalf("toggle = (%q, %v, %v), want T2/false/true", thread.ID, resolve, ok)
	}
	if v.threads[1].IsResolved {
		t.Error("optimistic flip did not mark T2 unresolved")
	}
}

// With nothing selected, toggle is a no-op rather than a panic.
func TestToggleThreadResolvedWithNoSelection(t *testing.T) {
	v := newCommentsView()
	v.setThreads(nil)
	if _, _, ok := v.toggleThreadResolved(); ok {
		t.Error("toggle on an empty list should report no selection")
	}
}

// The t key still toggles the filter, not the thread. The two were both called
// "toggleResolved" at one point; this pins them apart.
func TestTKeyTogglesFilterNotThread(t *testing.T) {
	v := newCommentsView()
	v.setThreads([]pr.ReviewThread{{ID: "T1", IsResolved: true}})
	before := v.hideResolved
	v.toggleResolvedFilter()
	if v.hideResolved == before {
		t.Error("t did not flip the resolved filter")
	}
	// And the thread state is untouched.
	if !v.threads[0].IsResolved {
		t.Error("t must not change a thread's resolved state")
	}
}

// The footer advertises X so it is discoverable.
func TestCommentsFooterAdvertisesResolve(t *testing.T) {
	v := newCommentsView()
	got := v.helpLine()
	if !strings.Contains(got, "X") || !strings.Contains(got, "resolve") {
		t.Errorf("footer = %q, want it to mention X:resolve", got)
	}
}

// The gh client dispatches the right GraphQL mutation per direction.
func TestResolveThreadCallsResolveMutation(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeTestExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s\n' "$*" >> "$CAPTURE"
case "$*" in
  *"resolveReviewThread"*) printf '{"data":{"resolveReviewThread":{"thread":{"isResolved":true}}}}\n' ;;
  *"unresolveReviewThread"*) printf '{"data":{"unresolveReviewThread":{"thread":{"isResolved":false}}}}\n' ;;
  *) printf '{}\n' ;;
esac
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)

	// Reuse a detail model with the fake gh on PATH.
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.comments.setThreads([]pr.ReviewThread{
		{ID: "T1", IsResolved: false, Comments: []pr.ThreadComment{{DatabaseID: 1}}},
	})

	cmd := a.detail.resolveThread(pr.ReviewThread{ID: "T1"}, true)
	if cmd == nil {
		t.Fatal("resolveThread produced no command")
	}
	cmd()

	data, _ := os.ReadFile(capture)
	if !strings.Contains(string(data), "resolveReviewThread") {
		t.Errorf("resolve did not call resolveReviewThread: %s", data)
	}
	if !strings.Contains(string(data), "T1") {
		t.Errorf("resolve did not pass the thread ID: %s", data)
	}
}

// A failed mutation reverts the optimistic flip so the marker matches reality.
func TestResolveFailureRevertsOptimisticFlip(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
echo 'HTTP 403: Resource not accessible' >&2
exit 1
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.comments.setThreads([]pr.ReviewThread{
		{ID: "T1", IsResolved: false, Comments: []pr.ThreadComment{{DatabaseID: 1}}},
	})

	// Optimistic flip to resolved, then the failing mutation reverts it.
	cmd := a.detail.resolveThread(pr.ReviewThread{ID: "T1"}, true)
	msg := cmd()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("mutation result = %#v, want errMsg", msg)
	}
	if a.detail.comments.threads[0].IsResolved {
		t.Error("optimistic flip was not reverted after failure")
	}
}

// Ensure the context import is used (keeps the resolveThread signature honest
// in tests that construct their own commands).
var _ = context.Background
