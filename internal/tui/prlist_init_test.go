package tui

import (
	"testing"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

func eightSources() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{
		{Name: "a", Query: "state:open"}, {Name: "b", Query: "state:open"},
		{Name: "c", Query: "state:open"}, {Name: "d", Query: "state:open"},
		{Name: "e", Query: "state:open"}, {Name: "f", Query: "state:open"},
		{Name: "g", Query: "state:open"}, {Name: "h", Query: "state:open"},
	}
	return cfg
}

// coldListModel builds a list with no seeded rows, so init()'s choices are not
// influenced by whatever is in the developer's real cache directory.
func coldListModel(t *testing.T, cfg *config.Config) *prListModel {
	t.Helper()
	m := newPRListModel(cfg, nil, DefaultKeymap())
	m.fileCache = &prFileCache{dir: t.TempDir()}
	for i := range m.caches {
		m.caches[i] = nil
		m.loadings[i] = false
	}
	return m
}

func loadingCount(m *prListModel) int {
	n := 0
	for _, l := range m.loadings {
		if l {
			n++
		}
	}
	return n
}

// Fetching every tab up front sends one API call per tab for data the user is
// not looking at, and the visible tab's response competes with the rest.
// selectTab already loads a source on first visit, so a cold start needs only
// the visible one.
func TestInitFetchesOnlyTheVisibleSourceWhenCold(t *testing.T) {
	m := coldListModel(t, eightSources())
	m.init()

	if got := loadingCount(m); got != 1 {
		t.Errorf("cold start fetched %d of %d tabs, want 1", got, len(m.sources))
	}
	if !m.loadings[m.curTab] {
		t.Error("the visible tab was not fetched")
	}
}

// A tab seeded from disk renders immediately from stale rows, so refreshing it
// in the background costs nothing visible and keeps the count honest.
func TestInitAlsoRefreshesCachedSources(t *testing.T) {
	m := coldListModel(t, eightSources())
	rows := []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}}
	m.caches[2], m.caches[5] = rows, rows

	m.init()

	if got := loadingCount(m); got != 3 {
		t.Errorf("warm start fetched %d tabs, want 3 (visible + 2 cached)", got)
	}
	for _, i := range []int{m.curTab, 2, 5} {
		if !m.loadings[i] {
			t.Errorf("tab %d should be refreshing", i)
		}
	}
	// An uncached, unvisited tab must stay untouched.
	if m.loadings[4] {
		t.Error("an uncached background tab was fetched")
	}
}

// The visible tab is fetched even when it already has cached rows — it is the
// one the user is reading, so it must not be left stale.
func TestInitRefreshesVisibleSourceEvenWhenCached(t *testing.T) {
	m := coldListModel(t, eightSources())
	m.caches[m.curTab] = []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}}

	m.init()

	if !m.loadings[m.curTab] {
		t.Error("a cached visible tab must still refresh")
	}
	if got := loadingCount(m); got != 1 {
		t.Errorf("fetched %d tabs, want only the visible one", got)
	}
}

// Visiting a tab loads it, which is what makes deferring safe.
func TestSelectTabLoadsOnFirstVisit(t *testing.T) {
	m := coldListModel(t, eightSources())
	m.init()

	if cmd := m.selectTab(3); cmd == nil {
		t.Fatal("visiting an unloaded tab produced no fetch")
	}
	if !m.loadings[3] {
		t.Error("the visited tab was not marked loading")
	}
}

// An empty source list must not panic on the curTab index.
func TestInitWithNoSources(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Sources = nil
	m := newPRListModel(cfg, nil, DefaultKeymap())
	m.sources = nil
	m.caches, m.loadings = nil, nil
	if cmd := m.init(); cmd != nil {
		t.Error("no sources should produce no fetch")
	}
}
