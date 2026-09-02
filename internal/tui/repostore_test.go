package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
)

// People revisit a handful of repositories constantly and the rest almost
// never. A ranking that captures that makes the top three rows the whole answer
// and typing optional; alphabetical order would make it a list to read.

func TestRepoStoreRanksByUseAndRecency(t *testing.T) {
	s := newRepoStoreAt(filepath.Join(t.TempDir(), "repos.json"))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	// A heavy hitter nobody has touched in a month.
	for range 40 {
		s.record("acme/stale", now.Add(-30*24*time.Hour))
	}
	// Today's work, opened a handful of times.
	for range 6 {
		s.record("acme/current", now)
	}

	ranked := s.rankRepos(nil, now)
	if len(ranked) != 2 {
		t.Fatalf("ranked %d repos, want 2", len(ranked))
	}
	// Visits alone would freeze last month's repo on top forever; recency alone
	// would put a repo opened once above one opened daily for a year.
	if ranked[0].Slug != "acme/current" {
		t.Errorf("first row is %q, want acme/current", ranked[0].Slug)
	}
	if ranked[0].Visits != 6 {
		t.Errorf("visits = %d, want 6", ranked[0].Visits)
	}
}

// A repo seen only in a queue is worth offering, but must not outrank one the
// user actually opens — it is a candidate, not a favourite.
func TestVisitedReposOutrankQueueOnlyOnes(t *testing.T) {
	s := newRepoStoreAt(filepath.Join(t.TempDir(), "repos.json"))
	now := time.Now()
	s.record("acme/visited", now)

	ranked := s.rankRepos(map[string]int{"acme/busy": 40}, now)
	if len(ranked) != 2 {
		t.Fatalf("ranked %d repos, want 2", len(ranked))
	}
	if ranked[0].Slug != "acme/visited" {
		t.Errorf("first row is %q, want the visited repo", ranked[0].Slug)
	}
	if ranked[1].Visits != 0 || ranked[1].OpenPRs != 40 {
		t.Errorf("queue-only row is %+v", ranked[1])
	}
}

// The store is keyed lowercase so a repo typed with different capitalization
// does not split into two entries that each rank half as high.
func TestRepoStoreFoldsCase(t *testing.T) {
	s := newRepoStoreAt(filepath.Join(t.TempDir(), "repos.json"))
	now := time.Now()
	s.record("Acme/One", now)
	s.record("acme/one", now)

	if got := len(s.rankRepos(nil, now)); got != 1 {
		t.Errorf("ranked %d repos, want 1 — capitalization split the entry", got)
	}
	if got := s.visits["acme/one"].Visits; got != 2 {
		t.Errorf("visits = %d, want 2", got)
	}
}

// ghx is closed with ^C and by closing the terminal window. There is no
// shutdown path reliable enough to hold the only copy of this, so a visit is
// durable the moment it happens.
func TestRepoStorePersistsEveryVisitImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	now := time.Now()

	newRepoStoreAt(path).record("acme/one", now)

	reloaded := newRepoStoreAt(path)
	if got := reloaded.visits["acme/one"].Visits; got != 1 {
		t.Errorf("visits after reload = %d, want 1", got)
	}
}

// The store is an optimization. A corrupt file is an empty history, never an
// error on screen — the picker still lists what the queues contain.
func TestCorruptRepoStoreIsAnEmptyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newRepoStoreAt(path)
	if len(s.visits) != 0 {
		t.Errorf("a corrupt file yielded %d entries", len(s.visits))
	}
	if got := s.rankRepos(map[string]int{"acme/one": 2}, time.Now()); len(got) != 1 {
		t.Errorf("the picker lost its queue candidates too: %v", got)
	}
}

// A slug that cannot address a repository must never enter the history: it
// would rank, be offered, and open a tab whose every gh call fails.
func TestRepoStoreRejectsNonSlugs(t *testing.T) {
	s := newRepoStoreAt(filepath.Join(t.TempDir(), "repos.json"))
	now := time.Now()
	for _, bad := range []string{"", "kite", "/kite", "acme/", "a/b/c", "   "} {
		s.record(bad, now)
	}
	if len(s.visits) != 0 {
		t.Errorf("invalid slugs entered the history: %v", s.visits)
	}
}

// A record written on a machine whose clock is ahead must not score above a
// visit made a moment ago — a negative age would otherwise amplify it.
func TestFutureTimestampDoesNotOutrank(t *testing.T) {
	s := newRepoStoreAt(filepath.Join(t.TempDir(), "repos.json"))
	now := time.Now()
	s.record("acme/future", now.Add(48*time.Hour))
	for range 3 {
		s.record("acme/now", now)
	}
	ranked := s.rankRepos(nil, now)
	if ranked[0].Slug != "acme/now" {
		t.Errorf("first row is %q, want acme/now", ranked[0].Slug)
	}
}

// Detection returns the launch directory plus every tmux pane. Only the first
// is a visit: counting a wide window as eight would let whatever happened to be
// split beside the work outrank the repo ghx was started in.
func TestOnlyTheLaunchDirectoryCountsAsAVisit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{{Name: "test", Query: "state:open"}}
	a := NewAppWithRepo(cfg, DefaultKeymap(), gh.NewClient(0),
		[]string{"acme/launched", "acme/pane-one", "acme/pane-two"})

	if got := a.repoStore.visits["acme/launched"].Visits; got != 1 {
		t.Errorf("the launch directory counted %d visits, want 1", got)
	}
	if got := a.repoStore.visits["acme/pane-one"].Visits; got != 0 {
		t.Errorf("a tmux pane counted %d visits, want 0", got)
	}
}
