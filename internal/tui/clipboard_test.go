package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// recordCopy captures what a copy would have written. The real clipboard is off
// limits in a test: it would overwrite whatever the developer had on it, and CI
// has no pbcopy for the command to reach.
func recordCopy(a *App, got *string) {
	a.copyClipboard = func(_ context.Context, _, text string) error {
		*got = text
		return nil
	}
}

func TestCopyURLFromListRow(t *testing.T) {
	a := testApp(t, []pr.Summary{
		{Number: 7, Repo: "acme/one", State: "OPEN", URL: "https://github.com/acme/one/pull/7"},
	})
	var copied string
	recordCopy(a, &copied)

	cmd := a.handleKey(keyMsg("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	msg := cmd()
	if err, ok := msg.(errMsg); ok {
		t.Fatalf("y failed: %v", err.err)
	}
	if copied != "https://github.com/acme/one/pull/7" {
		t.Errorf("copied %q", copied)
	}
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("expected a toast, got %T", msg)
	}
	if !strings.Contains(toast.text, "acme/one/pull/7") {
		t.Errorf("the toast does not name what was copied: %q", toast.text)
	}
}

// The URL you want is the URL of the PR you are looking at, so `y` has to work
// from the detail view too — the detail tabs get the key first and must not
// claim it.
func TestCopyURLFromDetailView(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 7, Repo: "acme/one", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 7, "acme/one")
	a.detail.detail = &pr.Detail{Number: 7, URL: "https://github.com/acme/one/pull/7"}
	var copied string
	recordCopy(a, &copied)

	for _, tab := range a.detail.tabs {
		copied = ""
		a.detail.activeTab = tab
		cmd := a.handleKey(keyMsg("y"))
		if cmd == nil {
			t.Fatalf("%s tab swallowed y", detailTabNames[tab])
		}
		if msg := cmd(); msg != nil {
			if err, ok := msg.(errMsg); ok {
				t.Fatalf("%s tab: %v", detailTabNames[tab], err.err)
			}
		}
		if copied != "https://github.com/acme/one/pull/7" {
			t.Errorf("%s tab copied %q", detailTabNames[tab], copied)
		}
	}
}

// A multi-selection copies every marked PR, one URL per line — the form that
// pastes usefully into a message or a ticket.
func TestCopyURLCoversTheWholeSelection(t *testing.T) {
	a := testApp(t, []pr.Summary{
		{Number: 1, Repo: "acme/one", State: "OPEN", URL: "https://github.com/acme/one/pull/1"},
		{Number: 2, Repo: "acme/two", State: "OPEN", URL: "https://github.com/acme/two/pull/2"},
	})
	a.handleKey(keyMsg("space"))
	a.list.list.Select(1)
	a.handleKey(keyMsg("space"))

	var copied string
	recordCopy(a, &copied)
	cmd := a.handleKey(keyMsg("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	if msg := cmd(); msg != nil {
		if err, ok := msg.(errMsg); ok {
			t.Fatalf("y failed: %v", err.err)
		}
	}
	lines := strings.Split(copied, "\n")
	if len(lines) != 2 {
		t.Fatalf("copied %d lines, want 2: %q", len(lines), copied)
	}
	for _, want := range []string{"one/pull/1", "two/pull/2"} {
		if !strings.Contains(copied, want) {
			t.Errorf("the selection's URLs are missing %s: %q", want, copied)
		}
	}
}

// Copying is read-only, so it must not consume the marks that chose the PRs —
// the same reasoning that leaves `o` non-destructive.
func TestCopyURLKeepsTheSelection(t *testing.T) {
	a := testApp(t, []pr.Summary{
		{Number: 1, Repo: "acme/one", State: "OPEN", URL: "https://github.com/acme/one/pull/1"},
	})
	a.handleKey(keyMsg("space"))
	var copied string
	recordCopy(a, &copied)
	if cmd := a.handleKey(keyMsg("y")); cmd != nil {
		cmd()
	}
	if len(a.list.selected) != 1 {
		t.Errorf("the selection has %d rows after copying, want 1", len(a.list.selected))
	}
}

// A copy that cannot reach the clipboard has to say so: the failure mode of a
// silent one is pasting whatever was there before and never noticing.
func TestCopyURLReportsAFailedClipboardWrite(t *testing.T) {
	a := testApp(t, []pr.Summary{
		{Number: 1, Repo: "acme/one", State: "OPEN", URL: "https://github.com/acme/one/pull/1"},
	})
	a.copyClipboard = func(context.Context, string, string) error {
		return context.DeadlineExceeded
	}
	cmd := a.handleKey(keyMsg("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Error("a failed clipboard write did not surface an error")
	}
}

// The palette reaches the same path, so :copy and y cannot drift apart.
func TestPaletteCopyUsesTheSamePath(t *testing.T) {
	a := testApp(t, []pr.Summary{
		{Number: 9, Repo: "acme/one", State: "OPEN", URL: "https://github.com/acme/one/pull/9"},
	})
	var copied string
	recordCopy(a, &copied)
	cmd := a.runPalette("copy")
	if cmd == nil {
		t.Fatal(":copy produced no command")
	}
	if msg := cmd(); msg != nil {
		if err, ok := msg.(errMsg); ok {
			t.Fatalf(":copy failed: %v", err.err)
		}
	}
	if copied != "https://github.com/acme/one/pull/9" {
		t.Errorf(":copy copied %q", copied)
	}
}

// Adding y must not take a key another binding already owned. The list's `y`
// was free, but the confirmation prompt's `y` means "yes" and is checked before
// the view — asserting that keeps the modal order from silently inverting.
func TestCopyKeyDoesNotStealTheConfirmationYes(t *testing.T) {
	a := testApp(t, sampleRows)
	var copied string
	recordCopy(a, &copied)

	a.handleKey(keyMsg("a")) // approve → confirmation
	if a.confirm == nil {
		t.Fatal("approve did not open a confirmation")
	}
	a.handleKey(keyMsg("y"))
	if copied != "" {
		t.Errorf("y copied while a confirmation was open: %q", copied)
	}
	if a.confirm != nil {
		t.Error("y did not answer the confirmation")
	}
}
