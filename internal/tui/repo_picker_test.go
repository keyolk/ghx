package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// Neither the tab strip nor the text filter can open an arbitrary repository:
// the strip holds what ghx guessed at startup, and the filter only narrows rows
// that were already fetched. A repo whose PRs are in none of the queues was
// unreachable without editing the config.

func pickerApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, now := freshApp(t)
	a.repoStore = newRepoStoreAt(filepath.Join(t.TempDir(), "repos.json"))
	return a, now
}

func TestRepoPickerOpensARepoAsANewTab(t *testing.T) {
	a, _ := pickerApp(t)
	before := len(a.list.sources)

	a.handleKey(keyMsg("e"))
	if a.repos == nil {
		t.Fatal("e did not open the repo picker")
	}
	for _, r := range "sendbird/kite" {
		a.handleKey(keyMsg(string(r)))
	}
	a.handleKey(keyMsg("enter"))

	if a.repos != nil {
		t.Error("the picker stayed open after opening a repo")
	}
	if len(a.list.sources) != before+1 {
		t.Fatalf("sources = %d, want %d", len(a.list.sources), before+1)
	}
	added := a.list.sources[len(a.list.sources)-1]
	if added.Repo != "sendbird/kite" {
		t.Errorf("new tab is scoped to %q", added.Repo)
	}
	// A picked tab must name itself the way a detected one does, or the two
	// read as different kinds of thing when they are the same thing.
	if added.Name != "kite" {
		t.Errorf("tab name = %q, want kite", added.Name)
	}
	if a.list.curTab != len(a.list.sources)-1 {
		t.Error("opening a repo did not focus its tab")
	}
	// The queue the user came from is still there, in its position.
	if a.list.sources[0].Name != "test" {
		t.Error("opening a repo replaced the queue instead of adding to it")
	}
}

// The second visit should be a number key, not another round of typing.
func TestRepoPickerReusesAnExistingTab(t *testing.T) {
	a, _ := pickerApp(t)
	a.openRepo("sendbird/kite")
	n := len(a.list.sources)

	a.list.selectTab(0)
	a.openRepo("SendBird/Kite") // GitHub slugs are case-insensitive
	if len(a.list.sources) != n {
		t.Errorf("sources = %d, want %d — a duplicate tab was added", len(a.list.sources), n)
	}
	if a.list.curTab != n-1 {
		t.Error("the existing tab was not focused")
	}
}

// A typo becomes a tab whose every gh call fails, with nothing on screen saying
// why. It has to be refused at the point it is typed.
func TestRepoPickerRefusesANonSlug(t *testing.T) {
	a, _ := pickerApp(t)
	before := len(a.list.sources)
	if cmd := a.openRepo("kite"); cmd == nil {
		t.Fatal("a bare name was accepted as a repository")
	}
	if len(a.list.sources) != before {
		t.Error("a bad slug still added a tab")
	}
}

// The history is empty on first run. A picker that opens blank teaches people
// it is broken before it has had a chance to learn anything, so it starts from
// the repositories whose PRs are already in the queues.
func TestRepoPickerOffersReposFromTheQueues(t *testing.T) {
	a, _ := pickerApp(t)
	a.list.caches[0] = []pr.Summary{
		{Number: 1, Repo: "acme/one"},
		{Number: 2, Repo: "acme/one"},
		{Number: 3, Repo: "acme/two"},
	}
	a.openRepoPicker()

	vis := a.repos.visible()
	if len(vis) != 2 {
		t.Fatalf("picker offers %d repos, want 2", len(vis))
	}
	// More open PRs first, since nothing has been visited yet.
	if vis[0].Slug != "acme/one" || vis[0].OpenPRs != 2 {
		t.Errorf("first row is %+v, want acme/one with 2 open", vis[0])
	}
}

// Every source's rows count, not just the visible tab's: which queue a repo's
// PRs happen to sit in is not something the person typing knows about.
func TestRepoPickerSeesEveryTabsRows(t *testing.T) {
	a, _ := pickerApp(t)
	a.list.appendSource(config.SourceDef{Name: "other", Query: "state:open"}, []pr.Summary{
		{Number: 9, Repo: "acme/hidden"},
	})
	seen := a.list.seenRepos()
	if seen["acme/hidden"] != 1 {
		t.Errorf("a repo on a non-visible tab is invisible to the picker: %v", seen)
	}
}

// Typing is optional for the repos you use, but the picker must still reach a
// repository it has never seen — that is precisely the one you had to type out.
func TestRepoPickerOpensAnUnseenRepoTypedInFull(t *testing.T) {
	a, _ := pickerApp(t)
	a.openRepoPicker()
	a.repos.query = "sendbird/brand-new"

	if len(a.repos.visible()) != 0 {
		t.Fatal("the test repo was already known")
	}
	slug, ok := a.repos.typed()
	if !ok || slug != "sendbird/brand-new" {
		t.Fatalf("typed() = (%q, %v)", slug, ok)
	}
	a.handleRepoPickerKey(keyMsg("enter"))
	if a.repos != nil {
		t.Fatal("the picker stayed open")
	}
	if got := a.list.sources[len(a.list.sources)-1].Repo; got != "sendbird/brand-new" {
		t.Errorf("opened %q", got)
	}
}

// j and k are letters that appear in real repository names. Reserving them for
// navigation would make "sendbird/kite" untypeable one keystroke in.
func TestRepoPickerLettersTypeRatherThanNavigate(t *testing.T) {
	a, _ := pickerApp(t)
	a.openRepoPicker()
	for _, r := range "jk" {
		a.handleRepoPickerKey(keyMsg(string(r)))
	}
	if a.repos.query != "jk" {
		t.Errorf("query = %q, want jk — a letter was eaten by navigation", a.repos.query)
	}
}

// A queue tab reached through the picker still polls and still refreshes, which
// only holds if it is an ordinary source. The parallel per-source slices are
// how that goes wrong: appendSource has to extend every one of them.
func TestPickedTabHasCompletePerSourceState(t *testing.T) {
	a, _ := pickerApp(t)
	a.openRepo("acme/new")
	i := len(a.list.sources) - 1
	for name, n := range map[string]int{
		"caches":      len(a.list.caches),
		"loadings":    len(a.list.loadings),
		"generations": len(a.list.generations),
		"errs":        len(a.list.errs),
		"dirty":       len(a.list.dirty),
		"warnings":    len(a.list.warnings),
		"fetchedAt":   len(a.list.fetchedAt),
	} {
		if n != i+1 {
			t.Errorf("%s has %d entries for %d sources", name, n, i+1)
		}
	}
	// The freshness readout indexes fetchedAt by the current tab; a short slice
	// is a panic on the next frame, not a compile error.
	if got := a.list.lastFetchNote(); got != "" && !strings.Contains(got, "fetching") {
		t.Errorf("a never-fetched picked tab reports an age: %q", got)
	}
}
