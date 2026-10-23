package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/pr"
)

// An idle ghx does not render, so a toast whose expiry was only checked at
// render time stayed in the footer until some unrelated event arrived. Setting
// a toast must schedule its own end.
func TestToastSchedulesItsOwnExpiry(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})

	_, cmd := a.Update(toastMsg{text: "copied"})
	if cmd == nil {
		t.Fatal("a toast scheduled no expiry tick")
	}
	if a.toast.Text() != "copied" {
		t.Fatalf("toast = %q", a.toast.Text())
	}

	a.Update(ToastExpiredMsg{seq: a.toast.Seq()})
	if got := a.toast.Text(); got != "" {
		t.Errorf("the expiry tick left %q in the footer", got)
	}
}

// A tick armed for an earlier toast must not cut a newer one short.
func TestStaleToastTickKeepsTheNewerToast(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})

	a.Update(toastMsg{text: "first"})
	stale := a.toast.Seq()
	a.Update(toastMsg{text: "second"})

	a.Update(ToastExpiredMsg{seq: stale})
	if got := a.toast.Text(); got != "second" {
		t.Errorf("the first toast's tick cleared the second: toast = %q", got)
	}
}

// Errors stay until a key: one that vanishes after four seconds is missed by
// anyone who looked away while the action ran.
func TestErrorToastWaitsForAKey(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})

	a.Update(errMsg{err: errors.New("boom")})
	if a.toast.Expiry() != nil {
		t.Fatal("an error toast scheduled an expiry")
	}
	a.Update(ToastExpiredMsg{seq: a.toast.Seq()})
	if a.toast.Text() == "" {
		t.Fatal("the error toast expired on a timer")
	}

	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if got := a.toast.Text(); got != "" {
		t.Errorf("a keypress did not dismiss the error: %q", got)
	}
}
