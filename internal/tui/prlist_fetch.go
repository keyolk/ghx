package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// How a source's rows are obtained, and when another ghx instance's answer will
// do instead of a request.
//
// The two questions belong together because they are one decision. Every caller
// that wants rows is either asking "what does this queue look like" — which a
// neighbour answered a moment ago just as well — or "what did the thing I just
// did change", which nothing already written can contain. Splitting the
// share-or-fetch choice away from the fetch itself is how a new caller ends up
// on the wrong side of it.

// shareWindowFraction is how much of the poll interval an entry may be reused
// for. It sets the steady-state fetch rate, and the arithmetic is worth
// spelling out because the intuitive value is the wrong one.
//
// With N instances polling every I and a window of f·I, a poll reuses the
// entry until it ages past f·I. Polls arrive every I/N, so a fetch happens
// every f·N of them — a fetch rate of 1/(f·I), independent of N. The number of
// windows drops out entirely, which is the property worth having: the account
// pays for the queue at one rate whether one ghx is open or eight.
//
// But f is what that rate is measured against, and f = 1/2 means 2/I — twice
// the cadence the user configured, from a change whose whole purpose is to
// spend less. f has to be near 1.
//
// It cannot be 1. A lone instance's own entry is exactly I old when its next
// poll comes round, and a window of exactly I would have it reuse or refetch
// depending on scheduling jitter of a few milliseconds — a single ghx skipping
// every other refresh at random. Nine tenths leaves a margin of 3s at the
// default 30s cadence, far larger than any jitter, and puts the shared rate at
// 1.11/I: within a ninth of what one window alone would cost.
const shareWindowNumerator, shareWindowDenominator = 9, 10

// shareWindow is how recently another ghx must have fetched a source for this
// one to reuse the answer instead of asking again.
//
// It is derived from the poll interval rather than fixed, because the question
// it answers is "would this instance's own poll have fetched by now". A window
// that has backed off to the idle cadence, or been throttled by a draining
// budget, should accept a correspondingly older answer — it was not going to
// ask for anything fresher itself.
func (m *prListModel) shareWindow() time.Duration {
	if m.pollIntervalFunc == nil {
		return 0
	}
	interval := m.pollIntervalFunc()
	if interval <= 0 {
		return 0
	}
	return interval * shareWindowNumerator / shareWindowDenominator
}

// sharedRows is another instance's recent answer for this source, when one is
// allowed and one exists.
func (m *prListModel) sharedRows(src config.SourceDef, share bool) ([]pr.Summary, time.Time, bool) {
	if !share {
		return nil, time.Time{}, false
	}
	return m.fileCache.fresh(src, m.shareWindow())
}

// fetchSource loads one source off the UI goroutine.
//
// A source pinned to one repo goes through `gh pr list`, which is REST and has
// its own generous rate limit. Only an unpinned source needs `gh search prs`,
// which spans repositories but spends the much scarcer GraphQL search budget.
//
// Before either, the shared cache is consulted: with an ghx parked in every
// tmux window, the usual reason a source is being fetched is that a sibling
// instance fetched it moments ago. Reusing that answer is the difference
// between the measured six-instance burn rate and one instance's.
func (m *prListModel) fetchSource(i int) tea.Cmd {
	return m.fetchSourceShared(i, true)
}

// refetchSource fetches without accepting a neighbour's answer.
//
// Every caller is a path where something just changed that no cached entry can
// know about: an approve or a merge taken in this window, or a commit the
// workspace sweep saw land on disk. A sibling's entry from ten seconds ago
// predates all of them, and serving it would show the queue exactly as it was
// before the action — the one moment the user is certain to be looking for the
// change. `R` is here for the same reason: a manual refresh that answers from
// a file is not a refresh.
func (m *prListModel) refetchSource(i int) tea.Cmd {
	return m.fetchSourceShared(i, false)
}

func (m *prListModel) fetchSourceShared(i int, share bool) tea.Cmd {
	if i < 0 || i >= len(m.sources) {
		return nil
	}
	src := m.sources[i]
	client := m.client
	m.generations[i]++
	generation := m.generations[i]
	if rows, savedAt, ok := m.sharedRows(src, share); ok {
		// Delivered as the ordinary response so it lands on the one path that
		// clears loadings, releases inFlight and re-arms the poll. Short-cutting
		// straight into the model here would skip all three and wedge the chain.
		//
		// The stamp is the file's, not now: these rows are as old as the fetch
		// that produced them, and the title's age readout exists precisely to
		// stop a hand-off like this from being reported as fresh.
		return func() tea.Msg {
			return prListMsg{
				sourceIdx: i, generation: generation,
				prs: rows, fetchedAt: savedAt,
			}
		}
	}
	if src.Repo != "" {
		scoped := client.WithRepo(src.Repo)
		query, repo := src.Query, src.Repo
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			prs, err := scoped.ListPRs(c, query, 50)
			// ListPRs answers about one repo, so it never echoes the slug back;
			// fill it in or every later call would have nothing to scope to.
			for j := range prs {
				if prs[j].Repo == "" {
					prs[j].Repo = repo
				}
			}
			var warning error
			if err == nil {
				prs, warning = scoped.EnrichPRStatuses(c, prs)
			}
			return prListMsg{sourceIdx: i, generation: generation, prs: prs, warning: warning, err: err}
		}
	}
	query := src.Query
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prs, warning, err := searchPRsAcrossAccounts(c, client, m.cfg.Accounts, query, 50)
		if err == nil {
			var statusWarning error
			prs, statusWarning = client.EnrichPRStatuses(c, prs)
			warning = errors.Join(warning, statusWarning)
		}
		return prListMsg{sourceIdx: i, generation: generation, prs: prs, warning: warning, err: err}
	}
}

// refreshCurrent reloads the visible source from GitHub.
//
// It never takes a neighbour's cached answer: its callers are `R`, the palette,
// and the actions taken in this window, and all of them are asking about a
// change that no already-written entry can contain.
func (m *prListModel) refreshCurrent() tea.Cmd {
	m.loadings[m.curTab] = true
	m.dirty[m.curTab] = false
	return m.refetchSource(m.curTab)
}

// invalidateCachesAndRefresh marks every source for reload after a bulk action,
// because one PR can appear in several queues. Only the visible source reloads
// immediately; the others reload on their next visit.
//
// No tab loses its rows. Emptying the cache is what put a "Loading…" line where
// the queue had been — on the visible tab straight away, and on every other tab
// the moment it was opened — so a single merge blanked whatever the user was
// working through and brought it back a round trip later. The rows are seconds
// stale at worst; the arriving response replaces them in place, and dirty is
// what makes sure that response is actually asked for.
func (m *prListModel) invalidateCachesAndRefresh() tea.Cmd {
	for i := range m.sources {
		m.generations[i]++
		m.loadings[i] = false
		m.errs[i] = nil
		if i != m.curTab {
			m.dirty[i] = true
		}
	}
	m.syncListItems()
	return m.refreshCurrent()
}

// markAllDirty flags every source for refetch without touching what is on
// screen. An action taken in the detail view changes a PR the list is also
// showing, and returning to a queue that still lists a merged PR is worse than
// a tab that refetches when it is next opened.
func (m *prListModel) markAllDirty() {
	for i := range m.sources {
		m.generations[i]++
		m.dirty[i] = true
	}
}

// loadSource fetches one source by index, whether or not it is visible.
//
// It is what lets a repository refresh because it was pushed to rather than
// because its tab is on screen. Sources already loading are skipped: the
// workspace sweep runs far more often than a fetch takes to return, and without
// this a slow queue would accumulate one in-flight request per sweep.
//
// It does not touch inFlight. That flag belongs to the background poll chain,
// which arms exactly one timer and would stall permanently if this path held
// its slot; the per-source generation is what discards a superseded response
// here.
func (m *prListModel) loadSource(i int) tea.Cmd {
	return m.loadSourceShared(i, true)
}

// reloadSource is loadSource for a repository that was just worked in.
//
// The sharing is off for the same reason refreshCurrent's is: the sweep fires
// because a commit or a push landed seconds ago, and every cached entry for
// that source predates it. Taking one would mean the tab refreshed — visibly,
// with the age readout resetting — and still showed the queue from before the
// push, which is worse than not having followed the work at all.
func (m *prListModel) reloadSource(i int) tea.Cmd {
	return m.loadSourceShared(i, false)
}

func (m *prListModel) loadSourceShared(i int, share bool) tea.Cmd {
	if i < 0 || i >= len(m.sources) || m.loadings[i] {
		return nil
	}
	m.loadings[i] = true
	m.dirty[i] = false
	return m.fetchSourceShared(i, share)
}
