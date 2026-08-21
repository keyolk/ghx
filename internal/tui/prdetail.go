package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// detailTabKind identifies a detail pane tab.
type detailTabKind int

const (
	tabOverview detailTabKind = iota
	tabFiles
	tabDiff
	tabComments
	tabCommits
	tabChecks
)

var detailTabNames = map[detailTabKind]string{
	tabOverview: "Overview", tabFiles: "Files", tabDiff: "Diff",
	tabComments: "Comments", tabCommits: "Commits", tabChecks: "Checks",
}

// prDetailModel holds the PR detail view. Each tab's own state lives in its
// own type (diffView, commentsView, checksView) so this stays a coordinator.
type prDetailModel struct {
	cfg            *config.Config
	client         *gh.Client
	km             *Keymap
	number         int
	owner          string
	repo           string
	credentialRepo string

	activeTab detailTabKind
	tabs      []detailTabKind

	detail *pr.Detail

	diff     *diffView
	comments *commentsView
	checks   *checksView

	// Overview/Files/Commits scroll offsets — plain views, no sub-model needed.
	overviewOff int
	filesCursor int
	filesOff    int
	commitsOff  int

	loadingDetail  bool
	loadingDiff    bool
	loadingThreads bool
	loadingChecks  bool

	// rawDiff is kept so threads arriving after the diff can rebuild the overlay
	rawDiff string

	// cache holds this PR's payload between visits; updatedAt is the row's
	// timestamp, which decides whether a stored entry still describes the PR.
	// A zero updatedAt (opened without a list row) disables the cache rather
	// than trusting an entry nothing can validate.
	cache     *detailFileCache
	updatedAt time.Time

	// fetched marks which of the four parts have landed this visit, so the
	// entry is only written once all of them have. A partial write reads back
	// as a PR with no diff, which is indistinguishable from one that has none.
	fetchedDetail  bool
	fetchedDiff    bool
	fetchedChecks  bool
	fetchedThreads bool

	gen int

	// spinner frame, pushed in by app.go so every spinner animates in step
	spinner int

	width  int
	height int
}

// newPRDetailModel builds the detail view. repoSlug ("owner/name") comes from
// the list row: the queue spans repositories, so every gh call for this PR must
// be scoped explicitly rather than inheriting the working directory.
func newPRDetailModel(cfg *config.Config, client *gh.Client, km *Keymap, number int, repoSlug string) *prDetailModel {
	return newPRDetailModelWithCredential(cfg, client, km, number, repoSlug, "")
}

func newPRDetailModelWithCredential(
	cfg *config.Config,
	client *gh.Client,
	km *Keymap,
	number int,
	repoSlug string,
	credentialRepo string,
) *prDetailModel {
	d := &prDetailModel{
		cfg: cfg, km: km, number: number, credentialRepo: credentialRepo,
		cache:     newDetailFileCache(),
		activeTab: tabOverview,
		tabs:      []detailTabKind{tabOverview, tabFiles, tabDiff, tabComments, tabCommits, tabChecks},
		diff:      newDiffView(),
		comments:  newCommentsView(),
		checks:    newChecksView(),
	}
	if credentialRepo != "" {
		client = client.WithCredentialRepo(credentialRepo)
	}
	if repoSlug != "" {
		d.client = client.WithRepo(repoSlug)
		if owner, repo, ok := strings.Cut(repoSlug, "/"); ok {
			d.owner, d.repo = owner, repo
		}
	} else {
		d.client = client
	}
	return d
}

func (d *prDetailModel) loading() bool {
	return d.loadingDetail || d.loadingDiff || d.loadingThreads || d.loadingChecks
}

func (d *prDetailModel) resize(w, h int) { d.width, d.height = w, h }

// load fans out the independent fetches. Each section renders as soon as its
// own request lands rather than waiting for the slowest one.
//
// A cached entry short-circuits all four: a PR's detail is the most expensive
// thing ghx fetches, and returning to one you just left is the most common
// movement in a review. The entry is only used when the row's updatedAt matches
// what was stored, so what is shown is the PR as the list last saw it — not
// something a timer has decided is probably still fine.
func (d *prDetailModel) load() tea.Cmd {
	if d.loadFromCache() {
		return nil
	}
	return d.fetch()
}

// loadFromCache populates the view from disk, reporting whether it could.
func (d *prDetailModel) loadFromCache() bool {
	if d.cache == nil {
		return false
	}
	repo := d.repoSlug()
	if repo == "" {
		return false
	}
	entry := d.cache.load(repo, d.number, d.updatedAt)
	if entry == nil {
		return false
	}
	d.detail = entry.Detail
	d.rawDiff = entry.Diff
	d.checks.setChecks(entry.Checks)
	d.comments.setThreads(entry.Threads)
	if err := d.diff.setContent(entry.Diff, entry.Threads); err != nil {
		return d.discardCachedEntry(repo)
	}
	// The parser tolerates junk by returning no files rather than an error, so
	// the check that matters is on the rows the view needs. A PR the detail says
	// touches files, whose cached diff produces nothing to scroll, is a corrupt
	// entry — and it would be shown as a PR with an empty diff, which looks like
	// a real (if odd) state rather than a bug.
	if entry.Detail.ChangedFiles > 0 && len(d.diff.rows) == 0 {
		return d.discardCachedEntry(repo)
	}
	d.loadingDetail, d.loadingDiff = false, false
	d.loadingChecks, d.loadingThreads = false, false
	d.fetchedDetail, d.fetchedDiff = true, true
	d.fetchedChecks, d.fetchedThreads = true, true
	return true
}

// discardCachedEntry drops a corrupt entry and clears whatever it had already
// populated, so the caller falls back to a clean fetch rather than rendering
// half of something unusable. Always reports false, for use as a return value.
func (d *prDetailModel) discardCachedEntry(repo string) bool {
	d.cache.evict(repo, d.number)
	d.detail, d.rawDiff = nil, ""
	d.checks.setChecks(nil)
	d.comments.setThreads(nil)
	_ = d.diff.setContent("", nil)
	return false
}

// reload forces the four fetches, bypassing the cache. R and every action that
// changes the PR use this: the row's updatedAt has not caught up yet, so the
// stored entry still looks valid for a state that just stopped being true.
func (d *prDetailModel) reload() tea.Cmd {
	if d.cache != nil {
		if repo := d.repoSlug(); repo != "" {
			d.cache.evict(repo, d.number)
		}
	}
	return d.fetch()
}

// repoSlug is the "owner/name" this PR belongs to, or "" when it is not known.
func (d *prDetailModel) repoSlug() string {
	if d.owner == "" || d.repo == "" {
		return ""
	}
	return d.owner + "/" + d.repo
}

// saveCache writes the entry once every part has landed.
func (d *prDetailModel) saveCache() {
	if d.cache == nil || !d.fetchedDetail || !d.fetchedDiff ||
		!d.fetchedChecks || !d.fetchedThreads {
		return
	}
	repo := d.repoSlug()
	if repo == "" || d.detail == nil {
		return
	}
	d.cache.save(repo, d.number, d.updatedAt, cachedDetail{
		Detail:  d.detail,
		Diff:    d.rawDiff,
		Checks:  d.checks.checks,
		Threads: d.comments.threads,
	})
}

// fetch issues the four requests unconditionally.
func (d *prDetailModel) fetch() tea.Cmd {
	d.loadingDetail = true
	d.loadingDiff = true
	d.loadingChecks = true
	d.loadingThreads = true
	d.fetchedDetail, d.fetchedDiff = false, false
	d.fetchedChecks, d.fetchedThreads = false, false
	n := d.number
	client := d.client

	cmds := []tea.Cmd{
		func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			det, err := client.ViewPR(c, n)
			return prDetailMsg{detail: det, err: err}
		},
		func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			raw, err := client.PRDiff(c, n)
			return prDiffMsg{diff: raw, err: err}
		},
		func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cks, err := client.PRChecks(c, n)
			return prChecksMsg{checks: cks, err: err}
		},
		func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			owner, repo, err := client.RepoSlug(c)
			if err != nil {
				return prThreadsMsg{err: err}
			}
			ts, err := client.ReviewThreads(c, owner, repo, n)
			return prThreadsMsg{threads: ts, err: err}
		},
	}
	return tea.Batch(cmds...)
}

// refreshThreads re-fetches just the threads, used after posting a comment or
// resolving one. The diff and the commit list cannot have changed, and the
// three requests that would fetch them again are the most expensive part of
// opening a PR.
//
// It evicts the cached entry first: the stored copy still holds the old thread
// list, and the row's updatedAt has not moved, so a return visit would serve
// the comment that was just posted as if it had never been.
func (d *prDetailModel) refreshThreads() tea.Cmd {
	d.evictCache()
	d.loadingThreads = true
	n, client := d.number, d.client
	owner, repo := d.owner, d.repo
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if owner == "" || repo == "" {
			var err error
			owner, repo, err = client.RepoSlug(c)
			if err != nil {
				return prThreadsMsg{err: err}
			}
		}
		ts, err := client.ReviewThreads(c, owner, repo, n)
		return prThreadsMsg{threads: ts, err: err}
	}
}

// evictCache drops this PR's stored entry, for the actions that change the PR
// without moving the row's updatedAt — which is every action taken from inside
// ghx, since the list has not re-fetched yet.
func (d *prDetailModel) evictCache() {
	if d.cache == nil {
		return
	}
	if repo := d.repoSlug(); repo != "" {
		d.cache.evict(repo, d.number)
	}
}

// refreshDetail re-fetches only the PR metadata: state, draft flag, review
// decision, mergeability, labels. Everything an approve, a ready-for-review, or
// a label edit can change, and nothing the diff or threads own.
func (d *prDetailModel) refreshDetail() tea.Cmd {
	d.evictCache()
	d.loadingDetail = true
	n, client := d.number, d.client
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		det, err := client.ViewPR(c, n)
		return prDetailMsg{detail: det, err: err}
	}
}

// refreshChecks re-fetches checks; used by the poll and by :refresh.
func (d *prDetailModel) refreshChecks() tea.Cmd {
	n, client := d.number, d.client
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cks, err := client.PRChecks(c, n)
		return prChecksMsg{checks: cks, err: err}
	}
}

func (d *prDetailModel) scheduleDebounce() tea.Cmd {
	d.gen++
	id := d.gen
	return tea.Tick(45*time.Millisecond, func(time.Time) tea.Msg {
		return detailDebounceMsg{gen: id}
	})
}

func (d *prDetailModel) handleDebounce(msg detailDebounceMsg) tea.Cmd {
	if msg.gen != d.gen {
		return nil
	}
	return d.load()
}

func (d *prDetailModel) handleDetailMsg(msg prDetailMsg) tea.Cmd {
	d.loadingDetail = false
	if msg.err != nil {
		return errCmd(msg.err)
	}
	d.detail = msg.detail
	d.fetchedDetail = true
	d.saveCache()
	return nil
}

func (d *prDetailModel) handleDiffMsg(msg prDiffMsg) tea.Cmd {
	d.loadingDiff = false
	if msg.err != nil {
		return errCmd(msg.err)
	}
	d.rawDiff = msg.diff
	if err := d.diff.setContent(msg.diff, d.diff.threads); err != nil {
		return errCmd(err)
	}
	d.fetchedDiff = true
	d.saveCache()
	return nil
}

func (d *prDetailModel) handleThreadsMsg(msg prThreadsMsg) tea.Cmd {
	d.loadingThreads = false
	if msg.err != nil {
		return errCmd(msg.err)
	}
	d.comments.setThreads(msg.threads)
	d.diff.setThreads(msg.threads)
	d.fetchedThreads = true
	d.saveCache()
	return nil
}

func (d *prDetailModel) handleChecksMsg(msg prChecksMsg) tea.Cmd {
	d.loadingChecks = false
	if msg.err != nil {
		return errCmd(msg.err)
	}
	d.checks.setChecks(msg.checks)
	d.fetchedChecks = true
	d.saveCache()
	// Keep polling only while something is still running, so a settled PR
	// costs nothing.
	if d.activeTab == tabChecks && d.checks.hasPending() {
		return tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
			return checksPollTickMsg(t)
		})
	}
	return nil
}

func (d *prDetailModel) handleChecksPoll() tea.Cmd {
	if d.activeTab != tabChecks || !d.checks.hasPending() {
		return nil
	}
	return d.refreshChecks()
}

func (d *prDetailModel) handleRunLogs(msg runLogsMsg) tea.Cmd {
	if msg.err != nil {
		// Drop back to the check list rather than leaving an empty log pane
		// open, which would look like the run genuinely produced no output.
		d.checks.closeLogs()
		return errCmd(msg.err)
	}
	d.checks.setLogs(msg.checkName, msg.logs)
	return nil
}

// contentHeight is the rows available to a tab body (title, tab strip, footer).
func (d *prDetailModel) contentHeight() int { return max(d.height-3, 1) }

func (d *prDetailModel) title() string {
	if d.detail != nil {
		state := d.detail.State
		if d.detail.IsDraft {
			state = "DRAFT"
		}
		return fmt.Sprintf("#%d %s · %s", d.number, d.detail.Title, state)
	}
	return fmt.Sprintf("#%d", d.number)
}

func (d *prDetailModel) view(w, h int) string {
	return joinVertical(d.renderTabs(w), d.renderActiveTab(w, h-1))
}

// renderTabs draws the tab strip, keeping every tab's index visible even when
// the terminal narrows — same per-tab shrinking strategy as the PR list's
// renderTabs, so detail tabs 4-6 don't vanish off the right edge.
func (d *prDetailModel) renderTabs(w int) string {
	tabs := make([]string, len(d.tabs))
	widths := make([]int, len(d.tabs))
	total := 0
	for i, t := range d.tabs {
		label := fmt.Sprintf("%d %s", i+1, detailTabNames[t])
		if c := d.tabCount(t); c != "" {
			label += tabCountStyle.Render("(" + c + ")")
		}
		if t == d.activeTab {
			tabs[i] = tabActiveStyle.Render("[" + label + "]")
		} else {
			tabs[i] = tabDimStyle.Render(" " + label + " ")
		}
		widths[i] = lipglossWidth(tabs[i])
		total += widths[i]
	}

	if total <= w {
		var b strings.Builder
		for _, t := range tabs {
			b.WriteString(t)
		}
		return b.String()
	}

	// Drop per-tab counts first; the index and name stay.
	for i, t := range d.tabs {
		if widths[i] <= 4 {
			continue
		}
		label := fmt.Sprintf("%d %s", i+1, detailTabNames[t])
		if t == d.activeTab {
			tabs[i] = tabActiveStyle.Render("[" + label + "]")
		} else {
			tabs[i] = tabDimStyle.Render(" " + label + " ")
		}
		widths[i] = lipglossWidth(tabs[i])
	}
	total = 0
	for _, w := range widths {
		total += w
	}
	if total <= w {
		var b strings.Builder
		for _, t := range tabs {
			b.WriteString(t)
		}
		return b.String()
	}

	// Cap each tab at an equal share, preserving at least the index.
	per := w / len(tabs)
	if per < 3 {
		per = 3
	}
	var b strings.Builder
	for i, t := range tabs {
		if widths[i] <= per {
			b.WriteString(t)
			continue
		}
		trunc, _ := truncateExact(t, per)
		if i != int(d.activeTabIndex()) && per > 3 && lipglossWidth(trunc) < per {
			trunc += " "
		}
		b.WriteString(trunc)
	}
	return b.String()
}

// activeTabIndex returns the position of the active tab within d.tabs, for the
// inactive-padding check in renderTabs.
func (d *prDetailModel) activeTabIndex() int {
	for i, t := range d.tabs {
		if t == d.activeTab {
			return i
		}
	}
	return 0
}

func (d *prDetailModel) tabCount(t detailTabKind) string {
	switch t {
	case tabFiles:
		if d.detail != nil && len(d.detail.Files) > 0 {
			return fmt.Sprint(len(d.detail.Files))
		}
	case tabComments:
		if n := len(d.comments.threads); n > 0 {
			return fmt.Sprint(n)
		}
	case tabCommits:
		if d.detail != nil && len(d.detail.Commits) > 0 {
			return fmt.Sprint(len(d.detail.Commits))
		}
	case tabChecks:
		if n := len(d.checks.checks); n > 0 {
			fails := 0
			for _, c := range d.checks.checks {
				if c.Bucket == "fail" {
					fails++
				}
			}
			if fails > 0 {
				return fmt.Sprintf("%d✗", fails)
			}
			return fmt.Sprint(n)
		}
	}
	return ""
}

func (d *prDetailModel) renderActiveTab(w, h int) string {
	switch d.activeTab {
	case tabOverview:
		if d.loadingDetail && d.detail == nil {
			return renderSpinner(d.spinnerFrame(), fmt.Sprintf("Loading PR #%d…", d.number))
		}
		return d.renderOverview(w, h)
	case tabFiles:
		if d.loadingDetail && d.detail == nil {
			return renderSpinner(d.spinnerFrame(), "Loading files…")
		}
		return d.renderFiles(w, h)
	case tabDiff:
		if d.loadingDiff && d.rawDiff == "" {
			return renderSpinner(d.spinnerFrame(), "Loading diff…")
		}
		return d.diff.render(w, h)
	case tabComments:
		if d.loadingThreads && len(d.comments.threads) == 0 {
			return renderSpinner(d.spinnerFrame(), "Loading review threads…")
		}
		return d.comments.render(w, h)
	case tabCommits:
		if d.loadingDetail && d.detail == nil {
			return renderSpinner(d.spinnerFrame(), "Loading commits…")
		}
		return d.renderCommits(w, h)
	case tabChecks:
		if d.loadingChecks && len(d.checks.checks) == 0 {
			return renderSpinner(d.spinnerFrame(), "Loading checks…")
		}
		return d.checks.render(w, h)
	}
	return ""
}

// spinnerFrame is supplied by app.go via setSpinnerFrame so every spinner in
// the app animates in step.
func (d *prDetailModel) spinnerFrame() int { return d.spinner }

func (d *prDetailModel) setSpinnerFrame(f int) { d.spinner = f }

// helpLine delegates to the active tab so hints always match what keys do.
func (d *prDetailModel) helpLine() string {
	switch d.activeTab {
	case tabDiff:
		return d.diff.helpLine()
	case tabComments:
		return d.comments.helpLine()
	case tabChecks:
		return d.checks.helpLine()
	case tabFiles:
		return fmtHints("j/k", "file", "enter", "open in diff", "1-6", "tab",
			"esc", "back")
	default:
		// M and y work here — and have all along — but the footer never said so,
		// which for merge is the whole difference between a key existing and a
		// key being usable: nothing else in the view hints that the PR can be
		// merged from it.
		return fmtHints("1-6", "tab", "h/l", "cycle", "a", "approve",
			"r", "request", "M", "merge", "y", "copy", "o", "browser",
			"esc", "back")
	}
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return errMsg{err: err} }
}
