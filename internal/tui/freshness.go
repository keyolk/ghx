package tui

import (
	"fmt"
	"strings"
	"time"
)

// How old are the rows on screen, and is the refresh still running.
//
// A polled list looks identical whether the poll is working or died twenty
// minutes ago — the rows are the same rows either way. Nothing else in the UI
// distinguishes an expired credential, a suspended poll, and a queue that
// genuinely has not changed, so the title says all three outright.

// now is the model's clock, replaced in tests.
func (m *prListModel) now() time.Time {
	if m.nowFunc != nil {
		return m.nowFunc()
	}
	return time.Now()
}

// lastFetchNote dates the rows on screen, and says when a fetch is running.
//
// A polled list looks identical whether the poll is working or died twenty
// minutes ago: the rows are the same rows either way. That is the failure this
// closes — an expired credential, a suspended poll, or a tab nobody has opened
// since startup all leave a queue that reads as current. The age is per source
// because only the visible tab polls.
//
// It returns "" when a source has never been answered for: an empty pane
// already says "Loading…" or names its error, and "never" beside it would add
// a second, vaguer version of the same thing.
func (m *prListModel) lastFetchNote() string {
	if m.curTab >= len(m.fetchedAt) {
		return ""
	}
	if m.loadings[m.curTab] || (m.inFlight && m.pollTab == m.curTab) {
		// The spinner in the pane only shows on an empty tab. With rows already
		// on screen — the normal case for a refresh — this is the only place a
		// fetch in flight is visible at all.
		return spinnerFrames[m.spinner%len(spinnerFrames)] + " fetching"
	}
	at := m.fetchedAt[m.curTab]
	if at.IsZero() {
		return ""
	}
	return relativeAge(m.now().Sub(at))
}

// relativeAge renders a fetch age the way someone would say it. Seconds are
// bucketed to 5 so the readout does not redraw as a ticking clock — the
// question it answers is "is this current", not "what time is it".
func relativeAge(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds())/5*5)
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}

// freshnessNote is the right-hand side of the title: how old the rows are, and
// why the refresh is not running at its configured cadence.
//
// Both belong in one slot because they answer one question — can I trust what I
// am looking at. Either alone is misleading: an age with no cadence looks like
// ghx is simply slow, and a cadence with no age says how often it *would*
// refresh without admitting when it last did.
func (a *App) freshnessNote() string {
	parts := make([]string, 0, 2)
	// Only the list has rows with an age. The detail view is fetched on open and
	// re-fetched per action, so "3m ago" there would date the wrong thing.
	if a.state == viewPRList && a.list != nil {
		if age := a.list.lastFetchNote(); age != "" {
			parts = append(parts, age)
		}
	}
	if poll := a.pollStatusNote(); poll != "" {
		parts = append(parts, poll)
	}
	if len(parts) == 0 {
		return ""
	}
	return dimStyle.Render(strings.Join(parts, " · ") + " ")
}

// pollStatusNote states the poll cadence, and why it is not the configured one.
//
// The cadence is stated even when it is exactly what the config says. It used
// to be omitted as redundant, but that made "polling every 30s" and "not
// polling at all" render identically — and the whole reason to look at this
// corner of the screen is to find out which of the two is happening. A number
// that is usually the same number is what makes its absence mean something.
//
// It is plain text: freshnessNote styles the assembled line, so styling here
// would nest escape sequences and break the width arithmetic that right-aligns
// it.
func (a *App) pollStatusNote() string {
	// A suspended poll has to say so, and must not name an interval: there is no
	// timer armed at all. Coming back to a window whose rows are an hour old,
	// with nothing on screen admitting it, is worse than the polling this
	// avoids — the marker is what makes the rows readable as "as of when I left"
	// rather than "as of now".
	//
	// "paused" alone would now be a lie in the other direction, though: the
	// timed poll is suspended, but the repositories this window is working in
	// still refresh when they are committed to or pushed. Naming the git watch
	// is what separates "these rows are frozen" from "the queues are frozen but
	// the repo you are pushing to is not" — and those call for different
	// reactions from the reader.
	if a.unfocused {
		if a.watchingRepos() {
			return "unfocused · on git"
		}
		return "paused · unfocused"
	}
	interval := a.pollInterval()
	if interval <= a.cfg.PollDuration() {
		return "poll " + shortDuration(interval)
	}
	b := a.client.GraphQLBudget()
	// The budget is the more urgent of the two reasons and the one the user can
	// act on (close some windows), so it wins the single slot.
	if b.Known && b.Fraction() < 0.5 {
		return fmt.Sprintf("API %d%% · poll %s",
			int(b.Fraction()*100), shortDuration(interval))
	}
	return fmt.Sprintf("idle · poll %s", shortDuration(interval))
}

// shortDuration renders a poll interval the way a person would say it.
func shortDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
