//go:build e2e

package tui

import (
	"os"
	"os/exec"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The unit tests feed sweep results in by hand, which proves the handling but
// not the sweep: the scan shells out to git and tmux and walks real
// directories, and none of that is exercised by a hand-built message. This runs
// the actual scanCmd against an actual checkout — the path that decides whether
// the feature does anything at all.
//
//	go test -tags e2e ./internal/tui/ -run TestSweepSeesRealGitWork -v
func TestSweepSeesRealGitWork(t *testing.T) {
	work := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(work, "init", "-q", work)
	run(work, "remote", "add", "origin", "https://github.com/keyolk/ghx.git")
	if err := os.WriteFile(work+"/a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", ".")
	run(work, "commit", "-qm", "first")

	a := listApp(t, rowsFor(1))
	// Detection is pointed at the temp checkout rather than the developer's own
	// working directory, and panes are left out: the question here is whether a
	// real git directory produces a real event, not what this machine's tmux
	// window happens to hold.
	a.workspace.dir = work
	off := false
	a.cfg.DetectPanes = &off

	sweep := func() workspaceScanMsg {
		t.Helper()
		msg, ok := a.workspace.scanCmd(false, a.workspace.gen)().(workspaceScanMsg)
		if !ok {
			t.Fatal("the sweep returned something other than a scan result")
		}
		return msg
	}

	// The first sweep discovers the repository; nothing is watched yet, so it
	// reports no events. Handling it is what installs the watcher.
	first := sweep()
	if len(first.repos) == 0 {
		t.Fatal("the sweep did not detect the checkout it was pointed at")
	}
	if first.repos[0].Slug != "keyolk/ghx" {
		t.Fatalf("detected %q, want keyolk/ghx", first.repos[0].Slug)
	}
	a.Update(first)
	if len(a.workspace.watchers) == 0 {
		t.Fatal("handling a sweep installed no watcher")
	}

	// Quiet: no work, no event. This is the steady state on a parked window.
	if got := sweep(); len(got.touched) != 0 {
		t.Errorf("an untouched checkout reported %d events", len(got.touched))
	}

	// A commit lands. Filesystem mtimes can be second-granular, so the write has
	// to be distinguishable from the baseline's timestamp.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(work+"/b.txt", []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", ".")
	run(work, "commit", "-qm", "second")

	touched := sweep()
	if len(touched.touched) == 0 {
		t.Fatal("a real commit produced no event from the real sweep")
	}
	if got := a.workspace.touchedSlugs(touched.touched); len(got) != 1 || got[0] != "keyolk/ghx" {
		t.Errorf("touched slugs = %v, want [keyolk/ghx]", got)
	}
}

// The same path while the window is not on screen, which is the case the
// suspended poll cannot cover and the reason this exists.
func TestUnfocusedWindowStillArmsTheSweep(t *testing.T) {
	a := listApp(t, rowsFor(1))
	a.workspace.dir = t.TempDir()

	a.Update(tea.BlurMsg{})
	if a.armPoll() != nil {
		t.Fatal("setup: an unfocused window should not arm a poll")
	}
	if a.armWorkspace() == nil {
		t.Error("an unfocused window stopped sweeping — the queue can no longer follow the work")
	}
}
