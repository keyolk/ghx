package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/repodetect"
)

// The queue follows the work rather than a timer. These pin the two halves of
// that: a repository that appears in a pane gets a tab, and a repository that
// was just committed to or pushed gets re-fetched — including while the window
// is not on screen, which is the case the suspended poll cannot cover.

// workspaceApp is a list app whose sweep can be driven by hand.
func workspaceApp(t *testing.T) *App {
	t.Helper()
	a := listApp(t, rowsFor(1, 2, 3))
	// Detection would otherwise reach the developer's real tmux window and
	// working directory, making the test depend on where it was run from.
	a.workspace.dir = t.TempDir()
	return a
}

// scan feeds a sweep result in, as the off-goroutine scan would.
//
// touched carries watched *paths*, matching what a real sweep reports: a
// worktree and its main checkout are separate paths for one repository, and
// keying the sweep by slug would hide that distinction from every test here.
func scan(a *App, repos []repodetect.Result, touched ...string) tea.Cmd {
	_, cmd := a.Update(workspaceScanMsg{
		repos: repos, touched: touched, gen: a.workspace.gen,
	})
	return cmd
}

// watchRepo attaches a real checkout to a slug and returns its path, so a test
// can report it as touched the way a sweep would.
func watchRepo(t *testing.T, a *App, slug string) string {
	t.Helper()
	path := repoOnDisk(t)
	a.workspace.observe(repodetect.Result{Slug: slug, Path: path})
	return path
}

func detected(slugs ...string) []repodetect.Result {
	out := make([]repodetect.Result, 0, len(slugs))
	for _, s := range slugs {
		out = append(out, repodetect.Result{Slug: s, Source: "tmux"})
	}
	return out
}

func tabNames(a *App) []string {
	out := make([]string, 0, len(a.list.sources))
	for _, s := range a.list.sources {
		out = append(out, s.Name)
	}
	return out
}

// A pane that cd's into another checkout after ghx started was invisible for
// the life of the process: detection ran once.
func TestPaneRepoAppearingLaterGetsATab(t *testing.T) {
	a := workspaceApp(t)
	before := len(a.list.sources)

	scan(a, detected("sendbird/ops-k8s"))

	if got := len(a.list.sources); got != before+1 {
		t.Fatalf("sources = %d, want %d — the new pane repo got no tab: %v",
			got, before+1, tabNames(a))
	}
	i, ok := a.list.sourceForRepo("sendbird/ops-k8s")
	if !ok {
		t.Fatal("no tab scoped to the newly detected repository")
	}
	if a.list.sources[i].Repo != "sendbird/ops-k8s" {
		t.Errorf("tab %d is scoped to %q", i, a.list.sources[i].Repo)
	}
}

// The tab is offered, not forced. Something appearing under the cursor because
// a pane elsewhere in the window cd'd somewhere is the opposite of the ask.
func TestNewTabDoesNotStealTheCursor(t *testing.T) {
	a := workspaceApp(t)
	before := a.list.curTab

	scan(a, detected("sendbird/ops-k8s"))

	if a.list.curTab != before {
		t.Errorf("the sweep moved the cursor to tab %d, want it left on %d",
			a.list.curTab, before)
	}
}

// Every sweep reports every pane, so adopting has to be idempotent — otherwise
// the strip grows a duplicate tab every five seconds.
func TestRepeatedSweepsDoNotDuplicateTabs(t *testing.T) {
	a := workspaceApp(t)
	repos := detected("sendbird/ops-k8s")

	scan(a, repos)
	after := len(a.list.sources)
	scan(a, repos)
	scan(a, repos)

	if got := len(a.list.sources); got != after {
		t.Errorf("sources grew to %d over repeated sweeps, want %d: %v",
			got, after, tabNames(a))
	}
}

// A repository the startup detection already gave a tab to must not be
// announced as new by the first sweep.
func TestSeededReposAreNotReadopted(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, nil)
	a.workspace.seed([]string{"keyolk/ghx"})
	before := len(a.list.sources)

	scan(a, detected("keyolk/ghx"))

	if got := len(a.list.sources); got != before {
		t.Errorf("sources = %d after sweeping a seeded repo, want %d: %v",
			got, before, tabNames(a))
	}
}

// A tab opened by hand through the repo picker should start reading as detected
// once a pane is actually in it.
func TestSweepMarksAnExistingTabAsDetected(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ops-k8s", Query: "state:open", Repo: "sendbird/ops-k8s",
	}, nil)
	before := len(a.list.sources)

	scan(a, detected("sendbird/ops-k8s"))

	if got := len(a.list.sources); got != before {
		t.Errorf("a repo with an existing tab got another one: %v", tabNames(a))
	}
	if !a.list.detectedRepos["sendbird/ops-k8s"] {
		t.Error("the existing tab was not marked as detected")
	}
}

// The other half: a repo that was just worked in gets re-fetched.
func TestTouchedRepoIsRefetched(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	i, _ := a.list.sourceForRepo("keyolk/ghx")
	path := watchRepo(t, a, "keyolk/ghx")

	if cmd := scan(a, nil, path); cmd == nil {
		t.Fatal("a touched repository did not produce a fetch")
	}
	if !a.list.loadings[i] {
		t.Error("the touched repository's tab was not marked as loading")
	}
}

// The case the suspended poll cannot cover, and the reason this exists: a push
// in the pane beside a window nobody is looking at.
func TestTouchedRepoIsRefetchedWhileUnfocused(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	i, _ := a.list.sourceForRepo("keyolk/ghx")
	path := watchRepo(t, a, "keyolk/ghx")

	a.Update(tea.BlurMsg{})
	if a.armPoll() != nil {
		t.Fatal("setup: an unfocused app should not arm a poll")
	}

	if cmd := scan(a, nil, path); cmd == nil {
		t.Fatal("an unfocused window ignored a push to a repo it is showing")
	}
	if !a.list.loadings[i] {
		t.Error("the touched tab was not refreshed while unfocused")
	}
}

// The counterpart that keeps the budget saving intact. Suspending the poll was
// protecting an account-wide allowance; a sweep that found nothing must spend
// nothing.
func TestQuietSweepCostsNoRequests(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	a.workspace.seed([]string{"keyolk/ghx"})
	a.Update(tea.BlurMsg{})

	if cmd := a.refreshTouchedRepos(a.workspace.touchedSlugs(nil)); cmd != nil {
		t.Error("a sweep with nothing touched still produced a fetch")
	}
	for i, loading := range a.list.loadings {
		if loading {
			t.Errorf("tab %d is loading after a quiet sweep", i)
		}
	}
}

// A push refreshes the repo it happened in, not the cross-repo search queues.
// Those spend the scarce GraphQL search budget — the very thing suspending the
// poll was protecting — and they catch up on the next ordinary poll.
func TestTouchRefreshesOnlyTheRepoScopedTab(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	scoped, _ := a.list.sourceForRepo("keyolk/ghx")
	path := watchRepo(t, a, "keyolk/ghx")

	scan(a, nil, path)

	for i := range a.list.sources {
		if i == scoped {
			continue
		}
		if a.list.loadings[i] {
			t.Errorf("unpinned source %q (%d) was refetched by a git event",
				a.list.sources[i].Name, i)
		}
	}
}

// The sweep runs far more often than a fetch takes to return. Without a guard,
// a slow queue accumulates one in-flight request per sweep.
func TestTouchDoesNotStackFetchesOnALoadingSource(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	i, _ := a.list.sourceForRepo("keyolk/ghx")
	path := watchRepo(t, a, "keyolk/ghx")

	scan(a, nil, path)
	if !a.list.loadings[i] {
		t.Fatal("setup: the first touch did not start a fetch")
	}
	if cmd := a.list.loadSource(i); cmd != nil {
		t.Error("a second touch queued another fetch on a source already loading")
	}
}

// A git event must not wedge the background poll: inFlight belongs to that
// chain, which arms exactly one timer and would stall permanently if this path
// held its slot.
func TestTouchDoesNotHoldThePollSlot(t *testing.T) {
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	path := watchRepo(t, a, "keyolk/ghx")

	scan(a, nil, path)

	if a.list.inFlight {
		t.Error("a git-triggered fetch claimed the background poll's slot")
	}
}

// The sweep chain must survive, or the feature works once and stops.
//
// The swept repo is one already known, so the batch holds the timer and nothing
// else. That keeps the count honest — an armed timer is recognized by still
// being pending, and a fetch closure waiting on the network is pending too, so
// a test that also adopted would count the fetch as a second timer (and shell
// out to the real gh to do it).
func TestSweepRearmsItself(t *testing.T) {
	a := workspaceApp(t)
	a.workspace.seed([]string{"sendbird/ops-k8s"})

	cmd := scan(a, detected("sendbird/ops-k8s"))
	if cmd == nil {
		t.Fatal("a completed sweep armed nothing — the chain died")
	}
	if n := countWorkspaceTicks(t, cmd, a.workspace.gen); n != 1 {
		t.Errorf("a completed sweep armed %d timers, want exactly 1", n)
	}
}

// It keeps sweeping while unfocused. Stopping there would put the feature back
// exactly where the poll already was.
func TestSweepKeepsRunningWhileUnfocused(t *testing.T) {
	a := workspaceApp(t)
	a.Update(tea.BlurMsg{})

	if a.armWorkspace() == nil {
		t.Fatal("an unfocused app stopped sweeping")
	}
	_, cmd := a.Update(workspaceTickMsg{at: time.Now(), gen: a.workspace.gen})
	if cmd == nil {
		t.Error("an unfocused app ignored a sweep tick")
	}
}

// A slow filesystem must not queue sweeps behind each other, but the chain has
// to stay alive across the wait.
func TestOverlappingSweepIsSkippedButRearms(t *testing.T) {
	a := workspaceApp(t)
	a.workspace.scanning = true

	_, cmd := a.Update(workspaceTickMsg{at: time.Now(), gen: a.workspace.gen})
	if cmd == nil {
		t.Fatal("a tick during a running sweep killed the chain")
	}
	if n := countWorkspaceTicks(t, cmd, a.workspace.gen); n != 1 {
		t.Errorf("armed %d timers while a sweep was running, want 1", n)
	}
}

// A result from a superseded generation is from a sweep whose state no longer
// applies; acting on it would adopt repos the app has since stopped tracking.
func TestStaleSweepResultIsDropped(t *testing.T) {
	a := workspaceApp(t)
	stale := a.workspace.gen
	a.workspace.gen++

	before := len(a.list.sources)
	_, cmd := a.Update(workspaceScanMsg{repos: detected("acme/widget"), gen: stale})
	if cmd != nil {
		t.Error("a stale sweep result still scheduled work")
	}
	if got := len(a.list.sources); got != before {
		t.Errorf("a stale sweep added tabs: %v", tabNames(a))
	}
}

// Detection turned off must stay off: the sweep is not a way around the config.
func TestDetectionDisabledAdoptsNothing(t *testing.T) {
	a := workspaceApp(t)
	off := false
	a.cfg.DetectRepo = &off
	before := len(a.list.sources)

	scan(a, detected("sendbird/ops-k8s"))

	if got := len(a.list.sources); got != before {
		t.Errorf("sources = %d with detect_repo off, want %d: %v",
			got, before, tabNames(a))
	}
}

// An empty sweep is "found nothing" — outside tmux, or every pane in a
// non-checkout. It must not be read as the repositories having gone away.
func TestEmptySweepKeepsExistingTabs(t *testing.T) {
	a := workspaceApp(t)
	scan(a, detected("sendbird/ops-k8s"))
	before := len(a.list.sources)

	scan(a, nil)

	if got := len(a.list.sources); got != before {
		t.Errorf("an empty sweep changed the strip to %v", tabNames(a))
	}
}

// A malformed slug must never become a tab: every later gh call against it
// fails, and the tab is not removable.
func TestSweepRejectsInvalidSlugs(t *testing.T) {
	a := workspaceApp(t)
	before := len(a.list.sources)

	scan(a, detected("not-a-slug", "too/many/parts", ""))

	if got := len(a.list.sources); got != before {
		t.Errorf("an invalid slug became a tab: %v", tabNames(a))
	}
}

// A polled list looks identical whether the poll is working or died — that is
// the failure the title corner exists to close. Suspending the poll while still
// following git makes "paused" wrong in the other direction: the queues really
// are frozen, but the repo being pushed to is not, and the reader reacts
// differently to each.
func TestUnfocusedTitleSaysGitIsStillWatched(t *testing.T) {
	a := workspaceApp(t)
	a.workspace.observe(repodetect.Result{Slug: "keyolk/ghx", Path: repoOnDisk(t)})
	a.Update(tea.BlurMsg{})

	note := stripANSISeqs(a.pollStatusNote())
	if !strings.Contains(note, "unfocused") {
		t.Errorf("note = %q, want it to admit the window is unfocused", note)
	}
	if !strings.Contains(note, "git") {
		t.Errorf("note = %q, want it to say the git watch is still live", note)
	}
	if strings.Contains(note, "paused") {
		t.Errorf("note = %q claims paused while git events still refresh", note)
	}
}

// With nothing watched — outside a checkout, detection off — the window really
// is frozen, and must still say so.
func TestUnfocusedWithoutWatchersStillSaysPaused(t *testing.T) {
	a := workspaceApp(t)
	a.Update(tea.BlurMsg{})

	if note := stripANSISeqs(a.pollStatusNote()); !strings.Contains(note, "paused") {
		t.Errorf("note = %q, want it to say the poll is paused", note)
	}
}

// repoOnDisk makes a real checkout, so observe installs an actual watcher
// rather than silently recording a slug with no path.
func repoOnDisk(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// A worktree is a separate checkout of the same repository, with its own HEAD
// and reflog. Watching one path per slug would follow whichever pane detection
// listed first and go blind to commits in the other — which is the layout this
// repo is actually worked in.
func TestWorktreeAndMainCheckoutAreBothWatched(t *testing.T) {
	a := workspaceApp(t)
	main, tree := repoOnDisk(t), repoOnDisk(t)

	a.workspace.observe(repodetect.Result{Slug: "keyolk/ghx", Path: main})
	a.workspace.observe(repodetect.Result{Slug: "keyolk/ghx", Path: tree})

	if got := len(a.workspace.watchers); got != 2 {
		t.Errorf("watchers = %d, want one per checkout path", got)
	}
}

// The two checkouts are still one tab, so a sweep that saw both move must not
// fetch it twice.
func TestTouchedWorktreesCollapseToOneRefresh(t *testing.T) {
	a := workspaceApp(t)
	main, tree := repoOnDisk(t), repoOnDisk(t)
	a.workspace.observe(repodetect.Result{Slug: "keyolk/ghx", Path: main})
	a.workspace.observe(repodetect.Result{Slug: "keyolk/ghx", Path: tree})

	got := a.workspace.touchedSlugs([]string{main, tree})
	if len(got) != 1 || got[0] != "keyolk/ghx" {
		t.Errorf("touchedSlugs = %v, want one keyolk/ghx entry", got)
	}
}

// The baseline stored is the sweep's own snapshot, not a fresh one. Re-taking
// it on the UI goroutine would fold a write that landed in between into the new
// baseline: it would never differ from anything, and the fetch it should have
// caused would never be sent.
func TestSweepBaselineComesFromTheSweep(t *testing.T) {
	a := workspaceApp(t)
	path := repoOnDisk(t)
	a.workspace.observe(repodetect.Result{Slug: "keyolk/ghx", Path: path})

	// A sweep that reports a path but carries no snapshot for it must leave the
	// old baseline alone, so the next sweep still sees the difference.
	before := a.workspace.prev[path]
	a.workspace.commit(nil)
	if got := a.workspace.prev[path]; got.Changed(before) != before.Changed(got) {
		t.Error("committing an empty sweep replaced the baseline")
	}
	if a.workspace.prev[path].Empty() {
		t.Error("the baseline was cleared by a sweep that carried nothing")
	}
}

// countWorkspaceTicks counts armed sweep timers in a cmd tree. Like the poll's
// counterpart it identifies a timer by still being pending after a deadline far
// shorter than the interval, never by running it to completion.
func countWorkspaceTicks(t *testing.T, cmd tea.Cmd, gen uint64) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if bm, ok := msg.(tea.BatchMsg); ok {
			n := 0
			for _, c := range bm {
				n += countWorkspaceTicks(t, c, gen)
			}
			return n
		}
		if tick, ok := msg.(workspaceTickMsg); ok && tick.gen == gen {
			return 1
		}
		return 0
	case <-time.After(200 * time.Millisecond):
		return 1
	}
}
