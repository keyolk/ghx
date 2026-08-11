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
func (d *prDetailModel) load() tea.Cmd {
	d.loadingDetail = true
	d.loadingDiff = true
	d.loadingChecks = true
	d.loadingThreads = true
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

// refreshThreads re-fetches just the threads, used after posting a comment.
func (d *prDetailModel) refreshThreads() tea.Cmd {
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
	return nil
}

func (d *prDetailModel) handleThreadsMsg(msg prThreadsMsg) tea.Cmd {
	d.loadingThreads = false
	if msg.err != nil {
		return errCmd(msg.err)
	}
	d.comments.setThreads(msg.threads)
	d.diff.setThreads(msg.threads)
	return nil
}

func (d *prDetailModel) handleChecksMsg(msg prChecksMsg) tea.Cmd {
	d.loadingChecks = false
	if msg.err != nil {
		return errCmd(msg.err)
	}
	d.checks.setChecks(msg.checks)
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

// renderTabs draws the tab strip with per-tab counts where they're meaningful.
func (d *prDetailModel) renderTabs(w int) string {
	var b strings.Builder
	for i, t := range d.tabs {
		label := fmt.Sprintf("%d %s", i+1, detailTabNames[t])
		if c := d.tabCount(t); c != "" {
			label += tabCountStyle.Render("(" + c + ")")
		}
		if t == d.activeTab {
			b.WriteString(tabActiveStyle.Render("[" + label + "]"))
		} else {
			b.WriteString(tabDimStyle.Render(" " + label + " "))
		}
	}
	out, _ := truncateExact(b.String(), w)
	return out
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
		return fmtHints("j/k", "file", "enter", "open in diff", "1-6", "tab", "esc", "back")
	default:
		return fmtHints("1-6", "tab", "h/l", "cycle", "a", "approve",
			"r", "request", "C", "comment", "o", "browser", "esc", "back")
	}
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return errMsg{err: err} }
}
