package config

import (
	"strings"
	"testing"
)

// The detected repository leads the tab strip because that is almost always what
// the user came to look at. The configured sources must survive intact behind it,
// though — the wider review queue is the reason to have them.

func names(sources []SourceDef) string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Name)
	}
	return strings.Join(out, ",")
}

func TestDefaultConfigIncludesMyPRsFirst(t *testing.T) {
	sources := DefaultConfig().Sources
	if len(sources) == 0 {
		t.Fatal("default sources are empty")
	}
	if sources[0].Name != "My PRs" {
		t.Errorf("first default source = %q, want My PRs", sources[0].Name)
	}
	if sources[0].Query != "author:@me state:open" {
		t.Errorf("My PRs query = %q, want author:@me state:open", sources[0].Query)
	}
}

func TestEffectiveSourcesPutsDetectedRepoFirst(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "Assigned", Query: "assignee:@me state:open"},
	}}

	got := c.EffectiveSources("keyolk/ghx")
	if len(got) != 3 {
		t.Fatalf("got %d sources, want 3: %s", len(got), names(got))
	}
	if got[0].Repo != "keyolk/ghx" {
		t.Errorf("first source is %q, want the detected repo", got[0].Repo)
	}
	// The label drops the owner: within one org it repeats on every tab.
	if got[0].Name != "ghx" {
		t.Errorf("tab label = %q, want ghx", got[0].Name)
	}
	// Open PRs, not everything ever merged.
	if !strings.Contains(got[0].Query, "state:open") {
		t.Errorf("detected source query = %q, want it scoped to open PRs", got[0].Query)
	}
	// The configured queue must still be there, in order.
	if names(got[1:]) != "My reviews,Assigned" {
		t.Errorf("configured sources = %q, want them kept in order", names(got[1:]))
	}
}

// Nothing detected means nothing changes: the list opens exactly as configured.
func TestEffectiveSourcesWithoutDetection(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
	}}
	got := c.EffectiveSources("")
	if names(got) != "My reviews" {
		t.Errorf("sources = %q, want the configured list untouched", names(got))
	}
}

// A repo the user already configured a tab for is promoted, not duplicated —
// otherwise their own query (which may filter further) would be shadowed by a
// generic one.
func TestEffectiveSourcesPromotesExistingTab(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "platform", Query: "state:open label:urgent", Repo: "keyolk/ghx"},
		{Name: "Assigned", Query: "assignee:@me state:open"},
	}}

	got := c.EffectiveSources("keyolk/ghx")
	if len(got) != 3 {
		t.Fatalf("got %d sources, want 3 (no duplicate): %s", len(got), names(got))
	}
	if got[0].Name != "platform" {
		t.Errorf("first source = %q, want the user's own tab promoted", got[0].Name)
	}
	if got[0].Query != "state:open label:urgent" {
		t.Errorf("query = %q, want the configured query preserved", got[0].Query)
	}
	if names(got) != "platform,My reviews,Assigned" {
		t.Errorf("order = %q, want the promoted tab first and the rest in order", names(got))
	}
}

// Matching is case-insensitive: GitHub treats owner and repo names that way, and
// a case difference in config must not produce a duplicate tab.
func TestEffectiveSourcesMatchesRepoCaseInsensitively(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "mine", Query: "state:open", Repo: "Keyolk/GHX"},
	}}
	got := c.EffectiveSources("keyolk/ghx")
	if len(got) != 1 {
		t.Errorf("got %d sources, want 1 — case differences are the same repo: %s",
			len(got), names(got))
	}
}

// An empty configuration still works: the defaults come in behind the detection.
func TestEffectiveSourcesWithEmptyConfig(t *testing.T) {
	c := &Config{}
	got := c.EffectiveSources("o/n")
	if len(got) < 2 {
		t.Fatalf("got %d sources, want the detected repo plus defaults", len(got))
	}
	if got[0].Repo != "o/n" {
		t.Errorf("first source = %q, want the detected repo", got[0].Repo)
	}
	if len(got) != 1+len(DefaultConfig().Sources) {
		t.Errorf("got %d sources, want 1 + %d defaults",
			len(got), len(DefaultConfig().Sources))
	}
}

// EffectiveSources must not mutate the configuration it reads: it is called on
// every construction, and a growing source list would be the result.
func TestEffectiveSourcesDoesNotMutateConfig(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
	}}
	before := len(c.Sources)

	c.EffectiveSources("o/n")
	c.EffectiveSources("o/n")

	if len(c.Sources) != before {
		t.Errorf("config now has %d sources, started with %d", len(c.Sources), before)
	}
	if c.Sources[0].Name != "My reviews" {
		t.Errorf("configured source was overwritten: %q", c.Sources[0].Name)
	}
}

func TestShortRepoName(t *testing.T) {
	cases := map[string]string{
		"keyolk/ghx": "ghx",
		"o/n":        "n",
		"no-slash":   "no-slash",
		"":           "",
	}
	for in, want := range cases {
		if got := shortRepoName(in); got != want {
			t.Errorf("shortRepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// detect_repo: false must actually disable the behaviour — a pointer is used so
// an explicit false is distinguishable from the field being absent.
func TestEffectiveSourcesRespectsDetectRepoFalse(t *testing.T) {
	off := false
	c := &Config{
		Sources:    []SourceDef{{Name: "My reviews", Query: "review-requested:@me state:open"}},
		DetectRepo: &off,
	}
	got := c.EffectiveSources("keyolk/ghx")
	if names(got) != "My reviews" {
		t.Errorf("sources = %q, want detection skipped", names(got))
	}
}

func TestRepoDetectionEnabledDefaultsOn(t *testing.T) {
	if !(&Config{}).RepoDetectionEnabled() {
		t.Error("detection should be on when detect_repo is unset")
	}
	on, off := true, false
	if !(&Config{DetectRepo: &on}).RepoDetectionEnabled() {
		t.Error("detect_repo: true should enable detection")
	}
	if (&Config{DetectRepo: &off}).RepoDetectionEnabled() {
		t.Error("detect_repo: false should disable detection")
	}
}

// A config file that sets detect_repo: false must survive the merge with
// defaults, which is where an explicit false is easiest to lose.
func TestLoadMergeKeepsExplicitFalse(t *testing.T) {
	off := false
	dst := DefaultConfig()
	merge(dst, &Config{DetectRepo: &off})
	if dst.RepoDetectionEnabled() {
		t.Error("an explicit detect_repo: false was lost when merging over defaults")
	}
}

func TestMergeLoadsAccounts(t *testing.T) {
	dst := DefaultConfig()
	merge(dst, &Config{Accounts: []AccountDef{
		{Name: "personal", CredentialRepo: "keyolk/ghx"},
		{Name: "work", CredentialRepo: "sendbird/platform-tools"},
	}})

	if len(dst.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(dst.Accounts))
	}
	if dst.Accounts[1].Name != "work" || dst.Accounts[1].CredentialRepo != "sendbird/platform-tools" {
		t.Errorf("second account = %#v", dst.Accounts[1])
	}
}

func TestMergeAddsMyPRsToLegacyDefaultSources(t *testing.T) {
	dst := DefaultConfig()
	merge(dst, &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "Assigned", Query: "assignee:@me state:open"},
		{Name: "platform", Query: "state:open", Repo: "keyolk/ghx"},
	}})

	if names(dst.Sources) != "My PRs,My reviews,Assigned,platform" {
		t.Errorf("sources = %q, want legacy defaults prefixed with My PRs", names(dst.Sources))
	}
	if dst.Sources[0].Query != "author:@me state:open" {
		t.Errorf("My PRs query = %q, want author:@me state:open", dst.Sources[0].Query)
	}
}

func TestMergeDoesNotChangeCustomSources(t *testing.T) {
	custom := []SourceDef{{Name: "Security", Query: "team-review-requested:acme/security state:open"}}
	dst := DefaultConfig()
	merge(dst, &Config{Sources: custom})

	if names(dst.Sources) != "Security" {
		t.Errorf("sources = %q, want custom sources unchanged", names(dst.Sources))
	}
}

func TestMergeDoesNotDuplicateExistingMyPRs(t *testing.T) {
	dst := DefaultConfig()
	merge(dst, &Config{Sources: []SourceDef{
		{Name: "Authored", Query: "author:@me state:open"},
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "Assigned", Query: "assignee:@me state:open"},
	}})

	if names(dst.Sources) != "Authored,My reviews,Assigned" {
		t.Errorf("sources = %q, want existing authored source unchanged", names(dst.Sources))
	}
}

// A tmux window spans several checkouts, so every detected repo gets a tab —
// in the order detection reported them, working directory first.
func TestEffectiveSourcesLeadsWithEveryDetectedRepo(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
	}}

	got := c.EffectiveSources("keyolk/ghx", "sendbird/platform-tools", "keyolk/kmd")
	if want := "ghx,platform-tools,kmd,My reviews"; names(got) != want {
		t.Errorf("sources = %q, want %q", names(got), want)
	}
	for i, repo := range []string{"keyolk/ghx", "sendbird/platform-tools", "keyolk/kmd"} {
		if got[i].Repo != repo {
			t.Errorf("source %d scoped to %q, want %q", i, got[i].Repo, repo)
		}
	}
}

// The same repository detected twice (a split beside the same checkout) must not
// produce two identical tabs.
func TestEffectiveSourcesDeduplicatesDetectedRepos(t *testing.T) {
	c := &Config{Sources: []SourceDef{{Name: "My reviews", Query: "review-requested:@me state:open"}}}

	got := c.EffectiveSources("keyolk/ghx", "KEYOLK/GHX", "keyolk/ghx")
	if want := "ghx,My reviews"; names(got) != want {
		t.Errorf("sources = %q, want %q", names(got), want)
	}
}

// A configured tab for a detected repo is promoted, keeping its own name and
// query, rather than being shadowed by a synthesized duplicate.
func TestEffectiveSourcesPromotesConfiguredTabsForDetectedRepos(t *testing.T) {
	c := &Config{Sources: []SourceDef{
		{Name: "My reviews", Query: "review-requested:@me state:open"},
		{Name: "platform-tools", Query: "state:open", Repo: "sendbird/platform-tools"},
	}}

	got := c.EffectiveSources("keyolk/ghx", "sendbird/platform-tools")
	if want := "ghx,platform-tools,My reviews"; names(got) != want {
		t.Errorf("sources = %q, want %q", names(got), want)
	}
	// Promotion must not duplicate the configured tab further down the strip.
	seen := 0
	for _, s := range got {
		if strings.EqualFold(s.Repo, "sendbird/platform-tools") {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("platform-tools appears %d times, want 1", seen)
	}
}

// detect_repo: false turns off every leading tab, panes included.
func TestEffectiveSourcesRespectsDetectionDisabled(t *testing.T) {
	off := false
	c := &Config{
		Sources:    []SourceDef{{Name: "My reviews", Query: "review-requested:@me state:open"}},
		DetectRepo: &off,
	}

	if got := c.EffectiveSources("keyolk/ghx", "keyolk/kmd"); names(got) != "My reviews" {
		t.Errorf("sources = %q, want only the configured tab", names(got))
	}
}

// detect_panes gates only the pane scan; the caller asks the config before
// deciding how wide to detect.
func TestPaneDetectionToggle(t *testing.T) {
	if !(&Config{}).PaneDetectionEnabled() {
		t.Error("pane detection should default to on")
	}
	off := false
	if (&Config{DetectPanes: &off}).PaneDetectionEnabled() {
		t.Error("detect_panes: false must disable pane detection")
	}
	// With detection off entirely, panes are moot.
	if (&Config{DetectRepo: &off}).PaneDetectionEnabled() {
		t.Error("detect_repo: false must also disable pane detection")
	}
	on := true
	if !(&Config{DetectPanes: &on}).PaneDetectionEnabled() {
		t.Error("detect_panes: true must enable pane detection")
	}
}
