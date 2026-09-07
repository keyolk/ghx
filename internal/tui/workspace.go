package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/repodetect"
	"github.com/keyolk/ghx/internal/repowatch"
)

// The queue follows the work, not a timer.
//
// Two things were fixed only at startup and shouldn't have been. Which
// repositories the window is about: detection ran once, so a pane that later
// cd'd into a different checkout — or a pane opened after ghx — was invisible
// for the life of the process. And when the queue is worth re-fetching: the
// poll is a fixed cadence that knows nothing about what the user did, so a
// branch pushed in the pane beside ghx surfaced whenever the next tick happened
// to land. In a window that is not on screen it never surfaced at all, because
// polling is suspended there deliberately — the GraphQL budget is per-account
// and several parked windows share it.
//
// Both are answered off the same sweep, and neither costs a request. Git writes
// every commit, checkout, push and fetch to disk, so "the user just did
// something to this repo" is a stat rather than an API call. That is what makes
// it safe to refresh a window nobody is looking at: an untouched checkout still
// costs exactly nothing, and a fetch happens because work actually happened.

// workspaceInterval is how often the panes and their git directories are
// swept.
//
// It is not the poll interval and must not be tied to it. This sweep costs
// filesystem stats and one `tmux list-panes`, not API requests, so it stays
// responsive at a cadence the poll could never afford — the point is that a
// push shows up in seconds rather than at the next poll. Five seconds is short
// enough to feel immediate and long enough that a wide window is not walking
// git directories continuously.
const workspaceInterval = 5 * time.Second

// workspaceTickMsg drives the sweep. Like the poll it carries a generation, so
// a timer armed before the app stood down does not resurrect the chain.
type workspaceTickMsg struct {
	at  time.Time
	gen uint64
}

// workspaceScanMsg carries a completed sweep back to the UI goroutine.
type workspaceScanMsg struct {
	// repos is the full detection result, most relevant first. Empty means the
	// sweep found nothing — outside tmux, or with every pane in a non-checkout —
	// which must not be read as "the repositories went away".
	repos []repodetect.Result
	// touched are the watched paths whose git directory moved since the last
	// sweep.
	touched []string
	// seen is the snapshot taken for every watched path during this sweep. It
	// becomes the next comparison's baseline, so a write that lands between the
	// sweep and its handling is reported by the following sweep rather than
	// being absorbed into a fresher baseline and lost.
	seen map[string]repowatch.Snapshot
	gen  uint64
}

// workspace tracks what the current tmux window is working on: which
// repositories its panes hold, and whether any of them has just been worked in.
type workspace struct {
	// dir is the directory detection starts from. It is captured once rather
	// than read per sweep: it is the launch directory, ghx never chdirs, and a
	// field is what lets a test drive a sweep without touching the process's
	// own working directory.
	dir string
	// watchers is one per checkout *path*, not per repository.
	//
	// Keying by slug would be the obvious choice — a repo is one tab, so one
	// event — and it is wrong here, because a worktree is a separate checkout of
	// the same slug with its own HEAD and reflog. Keeping the first path seen
	// would mean a window with `~/src/keyolk/ghx` in one pane and
	// `.worktree/feature` in another watches only whichever pane detection
	// listed first, and commits in the other are invisible. Which is precisely
	// the layout this repo is worked in.
	watchers map[string]*repowatch.Watcher
	// pathSlug maps a watched path back to the repository it belongs to, so
	// several checkouts of one repo collapse into one refresh.
	pathSlug map[string]string
	// prev is the last snapshot per watched path, against which the next is
	// compared.
	prev map[string]repowatch.Snapshot
	// known is every slug detection has reported, so a repo that appears in a
	// pane after startup can be told from one that was already there.
	known map[string]bool
	// gen retires timers armed under a superseded state, the way pollGen does.
	gen uint64
	// scanning guards against overlapping sweeps: the scan is off-goroutine and
	// a slow git directory must not queue a second one behind it.
	scanning bool
}

func newWorkspace() *workspace {
	dir, err := os.Getwd()
	if err != nil {
		dir = ""
	}
	return &workspace{
		dir:      dir,
		watchers: make(map[string]*repowatch.Watcher),
		pathSlug: make(map[string]string),
		prev:     make(map[string]repowatch.Snapshot),
		known:    make(map[string]bool),
	}
}

// seed records the repositories detection found at startup as already known, so
// the first sweep does not report all of them as newly appeared.
func (w *workspace) seed(repos []string) {
	for _, slug := range repos {
		if slug != "" {
			w.known[strings.ToLower(slug)] = true
		}
	}
}

// scanCmd sweeps the window off the UI goroutine.
//
// Detection shells out to git and tmux, and the git directory walk touches the
// filesystem; neither may run on the goroutine that draws frames. The result
// comes back as a message like every other async result in this app.
//
// The watcher map is read and written only from the returned closure's caller —
// handleWorkspaceScan — never inside the closure, so the sweep needs no lock.
func (w *workspace) scanCmd(paneDetection bool, gen uint64) tea.Cmd {
	// The snapshot targets are copied out under the UI goroutine, so the
	// closure never touches the maps the handler mutates.
	type target struct {
		path string
		w    *repowatch.Watcher
		prev repowatch.Snapshot
	}
	dir := w.dir
	targets := make([]target, 0, len(w.watchers))
	for path, watcher := range w.watchers {
		targets = append(targets, target{path: path, w: watcher, prev: w.prev[path]})
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var repos []repodetect.Result
		if paneDetection {
			repos = repodetect.DetectAll(ctx, dir)
		} else if r := repodetect.Detect(ctx, dir); r.Found() {
			repos = []repodetect.Result{r}
		}
		// The snapshot taken here is the one carried back, not one re-taken on
		// the UI goroutine afterwards. Re-taking it would let a write that
		// landed in between be folded into the new baseline and never reported
		// — the change would be seen once, recorded as observed, and the fetch
		// for it never sent.
		var touched []string
		seen := make(map[string]repowatch.Snapshot, len(targets))
		for _, t := range targets {
			now := t.w.Snapshot()
			seen[t.path] = now
			if now.Changed(t.prev) {
				touched = append(touched, t.path)
			}
		}
		return workspaceScanMsg{repos: repos, touched: touched, seen: seen, gen: gen}
	}
}

// observe installs or refreshes the watcher for a detected repository and
// returns whether the slug is one that has not been seen before.
//
// A repository with no resolvable root — detection found the slug through a
// remote but the path is gone — is still recorded as known. Otherwise it would
// be reported as new on every single sweep.
func (w *workspace) observe(r repodetect.Result) (isNew bool) {
	key := strings.ToLower(r.Slug)
	if key == "" {
		return false
	}
	isNew = !w.known[key]
	w.known[key] = true
	if r.Path == "" {
		return isNew
	}
	if _, ok := w.watchers[r.Path]; !ok {
		if watcher := repowatch.NewWatcher(r.Path); watcher != nil {
			w.watchers[r.Path] = watcher
			// The first snapshot is a baseline, not an event: recording it here
			// is what keeps a newly watched checkout from firing a fetch on the
			// very next sweep for work that predates ghx.
			w.prev[r.Path] = watcher.Snapshot()
		}
	}
	w.pathSlug[r.Path] = key
	return isNew
}

// commit stores the snapshots the sweep took, so the next one compares against
// what was actually observed rather than re-reporting the same change forever.
//
// It stores the sweep's own snapshots rather than taking fresh ones. A fresh
// snapshot would silently absorb anything written between the sweep and this
// call: that write would never differ from any baseline, so the fetch it should
// have caused would never be sent.
func (w *workspace) commit(seen map[string]repowatch.Snapshot) {
	for path, snap := range seen {
		if _, ok := w.watchers[path]; ok {
			w.prev[path] = snap
		}
	}
}

// touchedSlugs maps the watched paths a sweep reported back to repositories,
// deduplicated: two checkouts of one repo — a worktree and its main clone —
// are one tab and must not be fetched twice.
func (w *workspace) touchedSlugs(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		slug := w.pathSlug[path]
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out
}

// workspaceTickCmd arms the next sweep.
func workspaceTickCmd(gen uint64) tea.Cmd {
	return tea.Tick(workspaceInterval, func(t time.Time) tea.Msg {
		return workspaceTickMsg{at: t, gen: gen}
	})
}

// --- app wiring ---

// armWorkspace schedules the next sweep.
//
// Unlike armPoll this keeps running while the window is unfocused, and that is
// the whole point: the sweep spends filesystem stats, not API budget, so a
// window nobody is looking at can still notice that its repo was pushed to. The
// fetch that follows is caused by work that actually happened — an untouched
// checkout produces no event and therefore no request.
func (a *App) armWorkspace() tea.Cmd {
	if a.workspace == nil {
		return nil
	}
	return workspaceTickCmd(a.workspace.gen)
}

// handleWorkspaceTick starts a sweep, unless one is still running.
//
// The tick chain is re-armed by the scan result rather than here, for the same
// reason the poll is: two timers of the current generation cannot be told apart
// by the generation check, and the cadence silently doubles. A sweep that is
// still in flight re-arms so the chain survives a slow filesystem.
func (a *App) handleWorkspaceTick(msg workspaceTickMsg) tea.Cmd {
	if a.workspace == nil || msg.gen != a.workspace.gen {
		return nil
	}
	if a.workspace.scanning {
		return a.armWorkspace()
	}
	a.workspace.scanning = true
	return a.workspace.scanCmd(a.cfg.PaneDetectionEnabled(), a.workspace.gen)
}

// handleWorkspaceScan applies a completed sweep: new repositories become tabs,
// and repositories that were worked in get re-fetched.
func (a *App) handleWorkspaceScan(msg workspaceScanMsg) tea.Cmd {
	if a.workspace == nil || msg.gen != a.workspace.gen {
		return nil
	}
	a.workspace.scanning = false

	cmds := []tea.Cmd{a.armWorkspace()}
	if cmd := a.adoptDetectedRepos(msg.repos); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Adoption runs first so a repository detected for the first time in this
	// very sweep already has its tab when the refresh looks for one.
	if cmd := a.refreshTouchedRepos(a.workspace.touchedSlugs(msg.touched)); cmd != nil {
		cmds = append(cmds, cmd)
	}
	a.workspace.commit(msg.seen)
	return tea.Batch(cmds...)
}

// adoptDetectedRepos gives a tab to every repository the window now holds that
// did not have one.
//
// Tabs are only ever added, never removed. A pane that closes takes its
// repository's tab with it only in the sense that nothing refreshes it — the
// tab stays where it is, because removing it would renumber every tab after it
// and the 1-9 jump keys would land somewhere else mid-session, on a queue the
// user was reading. A stale tab costs a line of the strip; a renumbered one
// costs the reader their place.
//
// The new tab is not selected. Something appearing under the cursor because a
// pane in another part of the window cd'd somewhere is the opposite of what the
// user asked for; the tab is offered, and a number key takes it.
func (a *App) adoptDetectedRepos(repos []repodetect.Result) tea.Cmd {
	if a.list == nil || len(repos) == 0 || !a.cfg.RepoDetectionEnabled() {
		return nil
	}
	var cmds []tea.Cmd
	for _, r := range repos {
		if !validRepoSlug(r.Slug) {
			continue
		}
		isNew := a.workspace.observe(r)
		// The detection marker follows the panes, so a repo that has a tab from
		// the repo picker starts showing `*` once a pane is in it.
		a.list.markDetected(r.Slug)
		if !isNew {
			continue
		}
		if _, exists := a.list.sourceForRepo(r.Slug); exists {
			continue
		}
		a.list.appendSource(config.SourceDef{
			Name:  config.ShortRepoName(r.Slug),
			Query: "state:open",
			Repo:  r.Slug,
		}, nil)
		// Load it now rather than on first visit. The tab exists because the
		// user is working in that repo, so its count is the thing they would
		// look at the strip for — an unloaded tab shows no count at all.
		if cmd := a.list.loadSource(len(a.list.sources) - 1); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// refreshTouchedRepos re-fetches the tabs scoped to repositories that were just
// worked in.
//
// This is the part that works while unfocused, so it is deliberately narrow: it
// refreshes only tabs pinned to the touched repository, never the cross-repo
// search queues. A `gh pr list` against one repo is REST with its own generous
// limit, while the unpinned sources spend the scarce GraphQL search budget —
// and the budget is what suspending the poll was protecting in the first place.
// The queues catch up on the next ordinary poll.
func (a *App) refreshTouchedRepos(touched []string) tea.Cmd {
	if a.list == nil || len(touched) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, slug := range touched {
		i, ok := a.list.sourceForRepo(slug)
		if !ok {
			continue
		}
		if cmd := a.list.reloadSource(i); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// watchingRepos reports whether any repository is being watched for local git
// activity, which is what decides whether an unfocused window is genuinely
// frozen or still following the work.
func (a *App) watchingRepos() bool {
	return a.workspace != nil && len(a.workspace.watchers) > 0
}
