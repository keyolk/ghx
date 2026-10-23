package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// toastTTL is how long a success toast stays in the footer.
const toastTTL = 4 * time.Second

// Toast is the footer's one-line acknowledgement, shared by the main TUI and
// the subcommands.
//
// It expires on a tick rather than by an age check at render time. An idle
// Bubble Tea program does not render, so a toast that was only checked in View
// sat in the footer — covering the key hints — until the next poll or keypress
// happened to redraw. Errors are the exception: they stay until a key, because
// one that vanishes after four seconds is missed by anyone who looked away
// while the action ran, and the screen beneath it still shows the old state.
type Toast struct {
	text   string
	at     time.Time
	seq    int
	sticky bool
}

// ToastExpiredMsg is the tick that ends one toast.
type ToastExpiredMsg struct{ seq int }

// Set shows a transient message. The caller returns Expiry() from Update.
func (t *Toast) Set(s string) {
	t.text = s
	t.at = time.Now()
	t.seq++
	t.sticky = false
}

// SetError shows a message that stays until Ack.
func (t *Toast) SetError(s string) {
	t.Set(s)
	t.sticky = true
}

// Seq identifies the current toast, so a caller can tell whether handling a
// message raised a new one.
func (t *Toast) Seq() int { return t.seq }

// Expiry schedules the end of the current toast; nil for none or an error.
func (t *Toast) Expiry() tea.Cmd {
	if t.text == "" || t.sticky {
		return nil
	}
	seq := t.seq
	return tea.Tick(toastTTL, func(time.Time) tea.Msg { return ToastExpiredMsg{seq: seq} })
}

// Expire clears the toast if it is still the one the tick was armed for. A
// newer toast has its own tick and must keep its full time on screen.
func (t *Toast) Expire(msg ToastExpiredMsg) {
	if msg.seq != t.seq || t.sticky {
		return
	}
	t.text = ""
}

// Ack dismisses an error toast; call it when a key arrives, before handling it,
// so a toast the key itself raises survives.
func (t *Toast) Ack() {
	if t.sticky {
		t.text = ""
		t.sticky = false
	}
}

// Text is what the footer should show, or "" when nothing is current. The age
// check is a backstop for a tick that has not been delivered yet.
func (t *Toast) Text() string {
	if t.text == "" {
		return ""
	}
	if t.sticky || time.Since(t.at) < toastTTL {
		return t.text
	}
	return ""
}

// setToast shows a transient acknowledgement in the main TUI. Update schedules
// its expiry.
func (a *App) setToast(s string) { a.toast.Set(s) }

// setErrorToast shows a failure that stays until the next keypress.
func (a *App) setErrorToast(s string) { a.toast.SetError(s) }
