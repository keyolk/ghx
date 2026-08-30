package tui

import (
	"errors"
	"strings"
	"testing"
)

// A cross-account search reports success as soon as one account answers. A
// broken credential on the account that actually has the PRs therefore comes
// back as zero rows and a warning — not as an error — and overwriting the cache
// with that turned a working queue into "No PRs in this source", which is the
// one reading that is certainly wrong.

const credentialWarning = "account sendbird: gh search prs: no Git credential " +
	"found for configured account repository ghuser:gavin-jeong"

func TestPartialSearchKeepsTheRowsItAlreadyHad(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))

	cmd := a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		prs:        nil,
		warning:    errors.New(credentialWarning),
	})

	if got := len(a.list.list.VisibleItems()); got != 3 {
		t.Errorf("visible rows = %d after an incomplete search, want the 3 it had", got)
	}
	if body := stripANSI(a.list.view(120, 20)); strings.Contains(body, "No PRs in this source") {
		t.Errorf("an unreachable account was reported as an empty queue:\n%s", body)
	}

	// The rows are kept, so the reason has to arrive some other way.
	if cmd == nil {
		t.Fatal("the warning was swallowed entirely")
	}
	toast, ok := cmd().(toastMsg)
	if !ok {
		t.Fatalf("warning surfaced as %T, want a toast", cmd())
	}
	if !strings.Contains(toast.text, "no Git credential") {
		t.Errorf("toast = %q, want the reason", toast.text)
	}
	// The footer is one line; a multi-line warning would push the list off screen.
	if strings.Contains(toast.text, "\n") {
		t.Errorf("toast is multi-line: %q", toast.text)
	}
}

// With nothing cached there are no rows to keep, but the pane must still not
// claim the queue is empty — the difference between "no PRs" and "an account
// could not be reached" is the whole question being asked of this screen.
func TestPartialSearchOnAColdStartSaysWhatWentWrong(t *testing.T) {
	a := listApp(t, nil)
	a.list.caches[0] = nil
	a.list.syncListItems()

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		prs:        nil,
		warning:    errors.New(credentialWarning),
	})

	body := stripANSI(a.list.view(120, 20))
	if strings.Contains(body, "No PRs in this source") {
		t.Errorf("a failed account was reported as an empty queue:\n%s", body)
	}
	if !strings.Contains(body, "could not be searched in full") {
		t.Errorf("the pane did not say the search was incomplete:\n%s", body)
	}
	if !strings.Contains(body, "no Git credential") {
		t.Errorf("the pane did not carry the reason:\n%s", body)
	}
}

// A genuinely empty queue still reads as one. Warning-driven text on a healthy
// source would be worse than the bug it replaced.
func TestAnEmptySourceIsStillReportedAsEmpty(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		prs:        nil,
	})

	body := stripANSI(a.list.view(120, 20))
	if !strings.Contains(body, "No PRs in this source") {
		t.Errorf("an empty queue did not say so:\n%s", body)
	}
}

// Once a complete answer arrives, the warning pane must not linger.
func TestRecoveredSourceDropsTheWarning(t *testing.T) {
	a := listApp(t, nil)
	a.list.caches[0] = nil
	a.list.syncListItems()
	gen := a.list.generations[0]

	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, generation: gen,
		prs: nil, warning: errors.New(credentialWarning)})
	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, generation: gen, prs: rowsFor(1, 2)})

	if a.list.warnings[0] != nil {
		t.Error("a complete answer left the previous warning in place")
	}
	if got := len(a.list.list.VisibleItems()); got != 2 {
		t.Errorf("visible rows = %d after recovery, want 2", got)
	}
}

// An outright failure keeps its own pane: that path has no rows and no warning,
// and an empty list that is really an error must not read as "no PRs here".
func TestOutrightFailureStillExplainsTheEmptyPane(t *testing.T) {
	a := listApp(t, nil)
	a.list.caches[0] = nil
	a.list.syncListItems()

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		err:        errors.New("gh search prs: HTTP 401"),
	})

	if body := stripANSI(a.list.view(120, 20)); !strings.Contains(body, "Could not load this source") {
		t.Errorf("a failed load did not say why the pane was empty:\n%s", body)
	}
}
