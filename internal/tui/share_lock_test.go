package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// Sharing an entry only helps whoever reads it after it is written. These pin
// the two moments that used to fall outside that: several windows missing at
// once, and a push that every window watching the repository notices.

// slowGH is countingGH with a gh that takes a while to answer, so concurrent
// fetches genuinely overlap — the case the lock exists for.
func slowGH(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\nsleep 0.3\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, _ := os.ReadFile(logPath)
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runConcurrently runs every cmd at the same moment, the way separate ghx
// processes would, and waits for all of them.
func runConcurrently(cmds ...func()) {
	var wg sync.WaitGroup
	for _, c := range cmds {
		wg.Add(1)
		go func(c func()) { defer wg.Done(); c() }(c)
	}
	wg.Wait()
}

// Six windows opening on a queue nobody has fetched used to send six requests:
// each checked the cache before any had written to it. Under the lock the
// first fetches and the rest read its answer.
func TestWindowsMissingTogetherFetchOnce(t *testing.T) {
	calls := slowGH(t)
	dir := t.TempDir()
	windows := make([]*App, 6)
	for i := range windows {
		windows[i] = sharedCacheApp(t, dir, oneSource)
	}
	cmds := make([]func(), len(windows))
	for i, w := range windows {
		cmd := w.list.fetchSource(0)
		cmds[i] = func() { cmd() }
	}
	runConcurrently(cmds...)

	// One window's fetch is one `search prs` (enrichment of zero rows sends
	// nothing). Anything above that is a window that did not wait.
	if n := len(calls()); n != 1 {
		t.Errorf("six windows missing together sent %d requests, want 1:\n%s",
			n, strings.Join(calls(), "\n"))
	}
}

// A push is noticed by every window watching the repository within one sweep
// of each other. They all need a fetch sent after the push — and one such
// fetch answers all of them.
func TestAPushSeenByEveryWindowIsFetchedOnce(t *testing.T) {
	calls := slowGH(t)
	dir := t.TempDir()
	src := config.SourceDef{Name: "ghx", Query: "state:open", Repo: "keyolk/ghx"}
	// An entry from before the push: it must not satisfy anyone.
	(&prFileCache{dir: dir}).saveStarted(src,
		[]pr.Summary{{Number: 1, Repo: "keyolk/ghx", State: "OPEN"}}, time.Now().Add(-time.Second))

	windows := make([]*App, 4)
	cmds := make([]func(), len(windows))
	for i := range windows {
		windows[i] = sharedCacheApp(t, dir, nil)
		windows[i].list.appendSource(src, nil)
		cmd := windows[i].list.reloadSource(len(windows[i].list.sources) - 1)
		cmds[i] = func() { cmd() }
	}
	runConcurrently(cmds...)

	var lists int
	for _, c := range calls() {
		if strings.HasPrefix(c, "pr list") {
			lists++
		}
	}
	if lists != 1 {
		t.Errorf("four windows reacting to one push sent %d `pr list`, want 1:\n%s",
			lists, strings.Join(calls(), "\n"))
	}
}

// The guarantee a refresh makes is unchanged: an entry written before it was
// asked for is never its answer.
func TestARefreshIgnoresAnEntryFromBeforeIt(t *testing.T) {
	calls := countingGH(t)
	dir := t.TempDir()
	(&prFileCache{dir: dir}).saveStarted(oneSource[0],
		[]pr.Summary{{Number: 1, Repo: "acme/one", State: "OPEN"}}, time.Now().Add(-50*time.Millisecond))

	a := sharedCacheApp(t, dir, oneSource)
	runAll(a.list.refreshCurrent())
	if len(calls()) == 0 {
		t.Error("R was answered by an entry fetched before R was pressed")
	}
}

// An entry written before started_at existed has no proof of when its fetch
// was sent, so it must not count as fresh for anyone.
func TestALegacyEntryIsNeverFresh(t *testing.T) {
	dir := t.TempDir()
	c := &prFileCache{dir: dir}
	c.save(oneSource[0], nil)
	// Rewrite it without started_at, the way an older ghx left it.
	path := filepath.Join(dir, cacheKey(oneSource[0]))
	raw, _ := os.ReadFile(path)
	legacy := strings.Replace(string(raw), `"started_at"`, `"ignored"`, 1)
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.fresh(oneSource[0], time.Hour); ok {
		t.Error("an entry with no fetch start was served as fresh")
	}
}

// The waiter must see the rows the holder fetched, not just skip a request.
func TestAWaiterGetsTheHoldersRows(t *testing.T) {
	slowGH(t)
	dir := t.TempDir()
	cache := &prFileCache{dir: dir}
	first := sharedCacheApp(t, dir, oneSource)
	second := sharedCacheApp(t, dir, oneSource)

	release := cache.lock(t.Context(), oneSource[0])
	firstCmd, secondCmd := first.list.fetchSource(0), second.list.fetchSource(0)
	got := make(chan any, 1)
	go func() { got <- secondCmd() }()
	// The holder writes a fresh answer, then lets go.
	cache.saveStarted(oneSource[0], []pr.Summary{{Number: 9, Repo: "acme/one", State: "OPEN"}}, time.Now())
	release()
	_ = firstCmd

	msg, ok := (<-got).(prListMsg)
	if !ok || len(msg.prs) != 1 || msg.prs[0].Number != 9 {
		t.Fatalf("the waiter did not receive the holder's rows: %+v", msg)
	}
	if msg.persisted || msg.fetchedAt.IsZero() {
		t.Error("handed-over rows must keep their original age and not be re-saved")
	}
}

// A PR with CI running, left open on the Checks tab in a parked window, sent a
// request every 15s for as long as CI ran. The tick lapses while unfocused and
// the poll restarts when focus comes back.
func TestChecksPollLapsesWhileUnfocused(t *testing.T) {
	calls := countingGH(t)
	a := testApp(t, nil)
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 7, "acme/one")
	a.detail.activeTab = tabChecks
	a.detail.checks.setChecks([]pr.Check{{Name: "build", Bucket: "pending"}})

	a.Update(tea.BlurMsg{})
	_, cmd := a.Update(checksPollTickMsg(time.Now()))
	runAll(cmd)
	if n := len(calls()); n != 0 {
		t.Fatalf("an unfocused window polled checks %d times:\n%s", n, strings.Join(calls(), "\n"))
	}

	_, cmd = a.Update(tea.FocusMsg{})
	runAll(cmd)
	var checks int
	for _, c := range calls() {
		if strings.HasPrefix(c, "pr checks") {
			checks++
		}
	}
	if checks != 1 {
		t.Errorf("regaining focus sent %d checks requests, want the lapsed poll restarted once:\n%s",
			checks, strings.Join(calls(), "\n"))
	}
}
