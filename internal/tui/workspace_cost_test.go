package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
)

// A change that adds a refresh path has to be measured, not reasoned about: the
// output can be right while the cost is wrong, and a test that only asserts the
// rows would pass that regression. gh goes through one gate (Client.exec), so a
// logging shim on PATH counts every request the sweep actually causes.
//
// The number that matters is the one for a quiet window, because that is the
// steady state: several ghx instances parked in tmux for days, sharing one
// account's GraphQL budget. Suspending the poll while unfocused is what made
// that affordable, and this refresh runs while unfocused — so it has to cost
// nothing until real work happens.

// countingGH puts a gh on PATH that logs its arguments and answers with an
// empty list, so a sweep can run to completion without a network.
func countingGH(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\necho '[]'\n", logPath)
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

// fetchSettle is how long a command is given to prove it is a request rather
// than an armed timer. The shim on PATH answers immediately, so this only has
// to outlast process startup — seconds of headroom per sweep would otherwise
// make a 20-sweep measurement take a minute to learn a number that is zero.
const fetchSettle = 250 * time.Millisecond

// runAll runs a cmd tree to completion, timers included. Callers use it when
// the point is that a request was sent; the timers in these trees are not armed
// on this path.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runAll(c)
		}
	}
}

// runFetches drains the request-bearing commands of a cmd tree, skipping the
// armed timers.
//
// A tea.Tick blocks for its whole interval, so running one would have this test
// sleep through the sweep cadence to learn nothing — the question here is how
// many requests were sent, and a timer sends none. Each branch is run
// concurrently and joined, because a fetch closure blocks on its subprocess.
func runFetches(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var wg sync.WaitGroup
			for _, c := range batch {
				wg.Add(1)
				go func(c tea.Cmd) { defer wg.Done(); runFetches(c) }(c)
			}
			wg.Wait()
		}
	case <-time.After(fetchSettle):
		// Still pending past any real subprocess: an armed timer. Nothing to
		// count, and nothing to wait for.
	}
}

// The steady state. A parked window sweeping every 5s for an hour is 720
// sweeps; if any of them cost a request, the budget saving that suspending the
// poll bought is gone.
func TestQuietSweepsCostZeroRequests(t *testing.T) {
	calls := countingGH(t)
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))
	a.workspace.seed([]string{"keyolk/ghx"})
	a.Update(tea.BlurMsg{})

	// The armed sweep timer is dropped rather than run: it is a tea.Tick that
	// blocks for the sweep interval, and draining it would make this test sleep
	// through 20 of them to learn something about requests.
	for range 20 {
		_, cmd := a.Update(workspaceScanMsg{
			repos: detected("keyolk/ghx"), touched: nil, gen: a.workspace.gen,
		})
		runFetches(cmd)
	}

	if got := calls(); len(got) != 0 {
		t.Errorf("20 quiet sweeps sent %d requests, want 0:\n%s",
			len(got), strings.Join(got, "\n"))
	}
}

// One git event costs one repo-scoped fetch, not a refresh of every tab. The
// pinned path is `gh pr list`, which is REST with its own generous limit; the
// unpinned queues spend the scarce GraphQL search budget and must stay out of
// this path.
func TestOneGitEventCostsOneScopedFetch(t *testing.T) {
	calls := countingGH(t)
	a := workspaceApp(t)
	a.list.appendSource(config.SourceDef{
		Name: "ghx", Query: "state:open", Repo: "keyolk/ghx",
	}, rowsFor(1))

	// This one must actually wait for the request, or a too-eager deadline
	// makes "zero search requests" pass by having sent nothing at all — which
	// is the assertion silently inverting itself.
	path := watchRepo(t, a, "keyolk/ghx")
	runAll(a.refreshTouchedRepos(a.workspace.touchedSlugs([]string{path})))

	got := calls()
	if len(got) == 0 {
		t.Fatal("a git event sent no request at all — the fetch never ran")
	}
	searches := 0
	for _, c := range got {
		if strings.Contains(c, "search") || strings.Contains(c, "graphql") {
			searches++
		}
	}
	if searches != 0 {
		t.Errorf("a git event spent %d search/graphql requests:\n%s",
			searches, strings.Join(got, "\n"))
	}
	t.Logf("one git event = %d gh invocations: %v", len(got), got)
}
