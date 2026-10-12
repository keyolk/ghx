package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
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

// shareCutoff is the oldest fetch a poll will accept from a neighbour: one
// that was sent within the share window. Zero means no sharing at all.
func (m *prListModel) shareCutoff() time.Time {
	w := m.shareWindow()
	if w <= 0 {
		return time.Time{}
	}
	// Wall time, not m.now(): it is compared against stamps other processes
	// wrote with their own wall clocks.
	return time.Now().Add(-w)
}

// fetchSource loads one source off the UI goroutine, accepting a neighbour's
// answer from within the share window.
//
// A source pinned to one repo goes through `gh pr list`; an unpinned one needs
// `gh search prs`, which spans repositories. Both are GraphQL underneath, and
// both cost the account's shared pool — so before either, the shared cache is
// consulted: with a ghx parked in every tmux window, the usual reason a source
// is being fetched is that a sibling fetched it moments ago.
func (m *prListModel) fetchSource(i int) tea.Cmd {
	return m.fetchSourceSince(i, m.shareCutoff())
}

// refetchSource fetches anew, accepting only an answer that was sent after
// this moment.
//
// Every caller is a path where something just changed: an approve or a merge
// taken in this window, a commit the workspace sweep saw land on disk, or `R`.
// An entry written before that change cannot contain it, and serving it would
// show the queue exactly as it was before — the one moment the user is
// certain to be looking for the change.
//
// What it does accept is a fetch that started after the change, and that is
// not a corner case. A push is seen by every window watching the repository
// within one sweep of each other; the first to take the source's lock fetches,
// and the rest find its answer — sent after the push — when the lock comes
// round to them. Refusing every cached entry, as this used to, made each of
// them pay for the same push.
func (m *prListModel) refetchSource(i int) tea.Cmd {
	return m.fetchSourceSince(i, time.Now())
}

// fetchSourceSince fetches source i unless some ghx — this one or another —
// has an answer that was sent at or after notBefore. A zero notBefore always
// fetches.
//
// The check, the request and the write all happen under the source's
// cross-process lock. Without it the check is a race: every instance that
// misses at the same moment sees no entry and fetches, and the sharing only
// pays off for whoever comes after the last of them has written. With it the
// first fetches and writes before releasing, so the others re-check and find
// that answer.
func (m *prListModel) fetchSourceSince(i int, notBefore time.Time) tea.Cmd {
	if i < 0 || i >= len(m.sources) {
		return nil
	}
	src := m.sources[i]
	client := m.client
	cache := m.fileCache
	accounts := m.cfg.Accounts
	m.generations[i]++
	generation := m.generations[i]
	return func() tea.Msg {
		if !notBefore.IsZero() {
			// Checked before the lock too: the common case on a steady queue is
			// that a sibling answered within the window, and that should not
			// cost a lock round trip.
			if rows, savedAt, ok := cache.freshSince(src, notBefore); ok {
				return sharedListMsg(i, generation, rows, savedAt)
			}
			// The wait has its own bound, shorter than a request's, so a
			// sibling wedged on a hung gh costs this window its place in the
			// queue and not its refresh: once the wait runs out it fetches for
			// itself, with a full request deadline still ahead of it.
			wait, cancelWait := context.WithTimeout(context.Background(), lockWait)
			release := cache.lock(wait, src)
			cancelWait()
			defer release()
			if rows, savedAt, ok := cache.freshSince(src, notBefore); ok {
				return sharedListMsg(i, generation, rows, savedAt)
			}
		}
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		startedAt := time.Now()
		prs, warning, err := fetchRows(c, client, accounts, src)
		msg := prListMsg{sourceIdx: i, generation: generation, prs: prs, warning: warning, err: err}
		// Written here, under the lock, rather than when the message reaches
		// the UI goroutine: the waiters are blocked on this lock precisely to
		// read this answer. The same exception the handler makes applies — an
		// empty answer that came with a warning is not a verdict on the queue
		// and must not replace one that had rows.
		if err == nil && !(len(prs) == 0 && warning != nil) && cache != nil {
			cache.saveStarted(src, prs, startedAt)
			msg.persisted = true
		}
		return msg
	}
}

// lockWait bounds how long a fetch waits on a sibling holding the same
// source. A cross-repo search with enrichment is a few seconds at worst, so
// this outlasts any healthy holder with room to spare.
const lockWait = 15 * time.Second

// sharedListMsg delivers another instance's answer as an ordinary response, so
// it lands on the one path that clears loadings, releases inFlight and re-arms
// the poll. Short-cutting straight into the model would skip all three and
// wedge the chain.
//
// The stamp is the file's, not now: these rows are as old as the fetch that
// produced them, and the title's age readout exists precisely to stop a
// hand-off like this from being reported as fresh.
func sharedListMsg(i int, generation uint64, rows []pr.Summary, savedAt time.Time) prListMsg {
	return prListMsg{sourceIdx: i, generation: generation, prs: rows, fetchedAt: savedAt}
}

// fetchRows is the request itself: one repository through `gh pr list`, or a
// search across every configured account.
func fetchRows(ctx context.Context, client *gh.Client, accounts []config.AccountDef, src config.SourceDef) ([]pr.Summary, error, error) {
	if src.Repo != "" {
		scoped := client.WithRepo(src.Repo)
		prs, err := scoped.ListPRs(ctx, src.Query, 50)
		if err != nil {
			return nil, nil, err
		}
		// ListPRs answers about one repo, so it never echoes the slug back;
		// fill it in or every later call would have nothing to scope to.
		for j := range prs {
			if prs[j].Repo == "" {
				prs[j].Repo = src.Repo
			}
		}
		prs, warning := scoped.EnrichPRStatuses(ctx, prs)
		return prs, warning, nil
	}
	prs, warning, err := searchPRsAcrossAccounts(ctx, client, accounts, src.Query, 50)
	if err != nil {
		return nil, warning, err
	}
	prs, statusWarning := client.EnrichPRStatuses(ctx, prs)
	return prs, errors.Join(warning, statusWarning), nil
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
	return m.loadSourceSince(i, m.shareCutoff())
}

// reloadSource is loadSource for a repository that was just worked in.
//
// The sharing is off for the same reason refreshCurrent's is: the sweep fires
// because a commit or a push landed seconds ago, and every cached entry for
// that source predates it. Taking one would mean the tab refreshed — visibly,
// with the age readout resetting — and still showed the queue from before the
// push, which is worse than not having followed the work at all.
func (m *prListModel) reloadSource(i int) tea.Cmd {
	return m.loadSourceSince(i, time.Now())
}

func (m *prListModel) loadSourceSince(i int, notBefore time.Time) tea.Cmd {
	if i < 0 || i >= len(m.sources) || m.loadings[i] {
		return nil
	}
	m.loadings[i] = true
	m.dirty[i] = false
	return m.fetchSourceSince(i, notBefore)
}
