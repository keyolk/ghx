// Package config loads ~/.config/ghx/config.yaml with defaults for GitHub
// accounts, PR list sources, keymap overrides, colors, editor, and polling.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/keyolk/ghx/internal/pr"
	"gopkg.in/yaml.v3"
)

// Config is the loaded ghx configuration.
type Config struct {
	Sources        []SourceDef     `yaml:"sources"`
	Accounts       []AccountDef    `yaml:"accounts,omitempty"`
	Keymap         KeymapOverrides `yaml:"keymap"`
	Colors         ColorOverrides  `yaml:"colors"`
	Editor         string          `yaml:"editor"`
	Browser        string          `yaml:"browser"`
	PollInterval   string          `yaml:"poll_interval"`
	DiffSplitRatio int             `yaml:"diff_split_ratio"`

	// DetectRepo leads the PR list with the repositories the launch directory and
	// the current tmux window belong to. Set it to false to always open on the
	// configured sources — useful when ghx is run from inside one repo but used
	// mainly to triage a cross-repo queue.
	DetectRepo *bool `yaml:"detect_repo"`

	// DetectPanes extends detection to the other panes of the current tmux
	// window, which is where the rest of one task's checkouts usually sit. Set it
	// to false to detect only the launch directory: a wide window can otherwise
	// contribute more leading tabs than the 1-9 jump keys reach.
	DetectPanes *bool `yaml:"detect_panes"`

	// Derived (not YAML). PollInterval parsed into a duration.
	pollDuration time.Duration
}

// SourceDef is one PR list tab: a named gh search query, optionally scoped.
type SourceDef struct {
	Name  string `yaml:"name"`
	Query string `yaml:"query"`
	Repo  string `yaml:"repo"` // optional "owner/repo"
}

// AccountDef identifies one GitHub account. Either through a gh CLI login
// (gh_user, resolved with `gh auth token --user`) or through a repository whose
// Git credential belongs to that account (credential_repo). No token is ever
// stored here — only the selector used to look one up.
type AccountDef struct {
	Name           string `yaml:"name"`
	GHUser         string `yaml:"gh_user,omitempty"`
	CredentialRepo string `yaml:"credential_repo,omitempty"`
}

// GHUserSelectorPrefix marks a credential selector that names a gh CLI login
// rather than a repository.
const GHUserSelectorPrefix = pr.GHUserSelectorPrefix

// Selector returns the credential selector for this account, or "" when the
// account names neither a gh login nor a credential repository.
//
// gh_user wins: it addresses the account directly, while credential_repo only
// reaches it if the Git credential helper actually maps that path to a distinct
// token — which a `[credential "https://github.com"]` override can silently break.
func (a AccountDef) Selector() string {
	if user := strings.TrimSpace(a.GHUser); user != "" {
		return GHUserSelectorPrefix + user
	}
	return strings.TrimSpace(a.CredentialRepo)
}

// Label names the account in errors and warnings.
func (a AccountDef) Label() string {
	if a.Name != "" {
		return a.Name
	}
	if s := a.Selector(); s != "" {
		return s
	}
	return "(unnamed)"
}

// KeymapOverrides is a placeholder for per-key YAML overrides (Phase 6).
type KeymapOverrides map[string]string

// ColorOverrides maps semantic token names to hex strings (Phase 7).
type ColorOverrides map[string]string

// Path returns the config file path (~/.config/ghx/config.yaml).
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ghx", "config.yaml"), nil
}

// Load reads the config file. A missing file returns DefaultConfig (no error).
// A malformed file returns an error plus the defaults so the caller can proceed.
func Load() (*Config, error) {
	cfg := DefaultConfig()
	p, err := Path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", p, err)
	}
	var file Config
	if err := yaml.Unmarshal(data, &file); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", p, err)
	}
	merge(cfg, &file)
	return cfg, nil
}

// DefaultConfig returns the built-in defaults.
func DefaultConfig() *Config {
	return &Config{
		Sources: []SourceDef{
			{Name: "My PRs", Query: "author:@me state:open"},
			{Name: "My reviews", Query: "review-requested:@me state:open"},
			{Name: "Assigned to me", Query: "assignee:@me state:open"},
			{Name: "Mentioned", Query: "mentions:@me state:open"},
		},
		Editor:         "", // resolve from $EDITOR or "vi" at use site
		PollInterval:   "30s",
		DiffSplitRatio: 40,
	}
}

// merge overlays non-empty file fields onto defaults.
func merge(dst *Config, file *Config) {
	if len(file.Sources) > 0 {
		dst.Sources = migrateLegacySources(file.Sources)
	}
	if len(file.Accounts) > 0 {
		dst.Accounts = append([]AccountDef(nil), file.Accounts...)
	}
	if len(file.Keymap) > 0 {
		dst.Keymap = file.Keymap
	}
	if len(file.Colors) > 0 {
		dst.Colors = file.Colors
	}
	if file.Editor != "" {
		dst.Editor = file.Editor
	}
	if file.Browser != "" {
		dst.Browser = file.Browser
	}
	if file.PollInterval != "" {
		dst.PollInterval = file.PollInterval
	}
	if file.DiffSplitRatio > 0 {
		dst.DiffSplitRatio = file.DiffSplitRatio
	}
	// A pointer distinguishes "absent" from "explicitly false", so setting
	// detect_repo: false actually turns the behaviour off.
	if file.DetectRepo != nil {
		dst.DetectRepo = file.DetectRepo
	}
	if file.DetectPanes != nil {
		dst.DetectPanes = file.DetectPanes
	}
}

// migrateLegacySources adds My PRs to source lists generated by older ghx
// defaults. Fully custom lists remain untouched; recognizing both former default
// queries avoids silently changing a user's intentional tab set.
func migrateLegacySources(sources []SourceDef) []SourceDef {
	hasReviews := false
	hasAssigned := false
	for _, source := range sources {
		if source.Repo != "" {
			continue
		}
		switch source.Query {
		case "author:@me state:open":
			return sources
		case "review-requested:@me state:open":
			hasReviews = true
		case "assignee:@me state:open":
			hasAssigned = true
		}
	}
	if !hasReviews || !hasAssigned {
		return sources
	}

	out := make([]SourceDef, 0, len(sources)+1)
	out = append(out, SourceDef{Name: "My PRs", Query: "author:@me state:open"})
	return append(out, sources...)
}

// Marshal renders the config as YAML, for `ghx config view|init`.
func (c *Config) Marshal() ([]byte, error) {
	return yaml.Marshal(c)
}

// RepoDetectionEnabled reports whether the PR list should lead with the
// repository the user is in. It defaults to true: opening on the repo you are
// standing in is what makes the tool feel aware of context, and the configured
// sources are still one keystroke away.
func (c *Config) RepoDetectionEnabled() bool {
	if c.DetectRepo == nil {
		return true
	}
	return *c.DetectRepo
}

// PaneDetectionEnabled reports whether the other panes of the current tmux
// window should contribute tabs too. It defaults to true: a window is one task,
// and its panes are the checkouts that task spans. It is meaningless with
// detection off entirely.
func (c *Config) PaneDetectionEnabled() bool {
	if !c.RepoDetectionEnabled() {
		return false
	}
	if c.DetectPanes == nil {
		return true
	}
	return *c.DetectPanes
}

// EffectiveSources returns the source tabs to show, leading with the
// repositories the user is currently working in.
//
// The detected repos go first because that is almost always what the user came
// to look at; the configured sources follow unchanged, so the wider queue is
// still one keystroke away. A configured source already scoped to a detected
// repo is promoted rather than duplicated, and detectedRepos keeps its caller's
// order — working directory first, then tmux panes.
func (c *Config) EffectiveSources(detectedRepos ...string) []SourceDef {
	base := c.Sources
	if len(base) == 0 {
		base = DefaultConfig().Sources
	}
	if !c.RepoDetectionEnabled() {
		return base
	}

	// Promoted tabs keep their configured name and query; only their position
	// changes. Everything else stays in configured order behind them.
	var promoted []SourceDef
	used := make(map[int]bool)
	seen := make(map[string]bool)
	for _, repo := range detectedRepos {
		if repo == "" || seen[strings.ToLower(repo)] {
			continue
		}
		seen[strings.ToLower(repo)] = true
		matched := false
		for i, s := range base {
			if used[i] || !strings.EqualFold(s.Repo, repo) {
				continue
			}
			promoted = append(promoted, base[i])
			used[i] = true
			matched = true
			break
		}
		if !matched {
			promoted = append(promoted, SourceDef{
				Name:  shortRepoName(repo),
				Query: "state:open",
				Repo:  repo,
			})
		}
	}
	if len(promoted) == 0 {
		return base
	}

	out := make([]SourceDef, 0, len(promoted)+len(base))
	out = append(out, promoted...)
	for i, s := range base {
		if !used[i] {
			out = append(out, s)
		}
	}
	return out
}

// shortRepoName labels the detected tab. Within one org the owner repeats on
// every tab and only costs width, so it is dropped unless the name would be
// ambiguous on its own.
func shortRepoName(slug string) string {
	_, name, ok := strings.Cut(slug, "/")
	if !ok || name == "" {
		return slug
	}
	return name
}

// PollDuration returns the parsed poll interval, defaulting to 30s.
func (c *Config) PollDuration() time.Duration {
	if c.pollDuration > 0 {
		return c.pollDuration
	}
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil || d <= 0 {
		d = 30 * time.Second
	}
	c.pollDuration = d
	return d
}

// EditorCommand resolves the editor command: config > $EDITOR > "vi".
func (c *Config) EditorCommand() string {
	if c.Editor != "" {
		return c.Editor
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

// BrowserCommand resolves the URL opener: config > $BROWSER > the platform
// default (open on macOS, xdg-open elsewhere).
//
// Like EditorCommand this is a plain command name that receives the URL as its
// single argument. $BROWSER's fuller conventions — %s placeholders and
// colon-separated fallback lists — are deliberately not interpreted: a value
// using them would be handed to exec as a literal command name and fail
// visibly, which beats guessing at a syntax nobody agrees on.
func (c *Config) BrowserCommand() string {
	if c.Browser != "" {
		return c.Browser
	}
	if b := os.Getenv("BROWSER"); b != "" {
		return b
	}
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// Ensure pr.Summary is referenced so the import stays valid even if the
// package grows. (Keeps go vet happy in Phase 1.)
var _ = pr.Summary{}
