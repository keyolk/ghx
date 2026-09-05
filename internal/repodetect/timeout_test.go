package repodetect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The budget has to clear the one-off cost of this process's first fork/exec,
// which is not the cost of the command being run.
//
// Measured on a loaded machine: the first spawn takes ~3.6s and every one after
// it ~10ms. Detection is usually the first thing in the process to shell out,
// so it pays that alone — and a budget that does not cover it produces not a
// slow startup but a silently wrong one, because an exhausted context leaves
// every candidate empty, which reads exactly like "none of these directories is
// a checkout".
//
// This is the shape of that failure rather than the load that caused it: a
// `tmux` that takes longer than one spawn's worth of delay must still leave
// room for the git calls behind it.
func TestDetectionSurvivesASlowFirstSpawn(t *testing.T) {
	launch := t.TempDir()
	initRepo(t, launch, "https://github.com/keyolk/ghx.git")
	pane := t.TempDir()
	initRepo(t, pane, "https://github.com/acme/widget.git")

	// Stands in for the first-fork delay: the pane listing answers correctly but
	// slowly, exactly as it does when the process has not spawned before.
	//
	// Well short of the ~3.6s measured, because the assertion does not need the
	// real magnitude — it needs to exceed the two-second budget that produced
	// the bug, and every second spent here is a second on every test run.
	slowTmux(t, "0\t"+pane, 2500*time.Millisecond)

	got := slugs(DetectAll(context.Background(), launch))
	want := []string{"keyolk/ghx", "acme/widget"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("DetectAll = %v, want %v — the budget did not survive a slow "+
			"first spawn, and an empty result is indistinguishable from "+
			"'not a checkout'", got, want)
	}
}

// The bound still exists for what it was written for. A git that never returns
// must not hold the first frame, so detection gives up and reports nothing
// rather than hanging.
func TestDetectionStillGivesUpOnAHang(t *testing.T) {
	launch := t.TempDir()
	initRepo(t, launch, "https://github.com/keyolk/ghx.git")

	// A ctx already past its deadline stands in for a hang without making the
	// test wait out the real budget.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	done := make(chan []Result, 1)
	go func() { done <- DetectAll(ctx, launch) }()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("DetectAll = %v on an expired context, want nothing", slugs(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DetectAll did not return on an expired context")
	}
}

// The budget has to leave room for the git calls after the listing, not just
// for the listing itself — each candidate costs two or three git invocations
// and they are what actually resolve a slug.
func TestTheBudgetLeavesRoomForTheGitCallsBehindTheListing(t *testing.T) {
	if timeout < 5*time.Second {
		t.Fatalf("timeout is %s: a single slow first spawn (~3.6s measured) "+
			"consumes it, leaving nothing for the git calls that resolve the "+
			"slugs — which fails as an empty result, not as a delay", timeout)
	}
}

// slowTmux is fakeTmux with a delay, for asserting what the budget tolerates.
func slowTmux(t *testing.T, panes string, delay time.Duration) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nsleep %.2f\ncat <<'PANES'\n%s\nPANES\n",
		delay.Seconds(), panes)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
}
