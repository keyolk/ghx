// Command ghx is a TUI for reviewing GitHub pull requests: list, diff, inline
// comments, approve/request-changes, checks, and workflow run logs. It wraps
// the gh CLI for data and actions.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/repodetect"
	"github.com/keyolk/ghx/internal/tui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ghx: %v\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Println("ghx", version)
			return nil
		case "--help", "-h", "help":
			printUsage()
			return nil
		case "config":
			return runConfig(os.Args[2:])
		case "admin":
			return runSubcommand(os.Args[2:], "admin")
		case "actions":
			return runSubcommand(os.Args[2:], "actions")
		}
	}

	// Pre-flight: gh must be installed and authenticated.
	if _, lookupErr := exec.LookPath("gh"); lookupErr != nil {
		return fmt.Errorf("gh CLI not found in PATH — install from https://cli.github.com")
	}
	client := gh.NewClient(30 * 1_000_000_000) // 30s

	// Load config (missing file = defaults, no error).
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		// warn but continue with defaults
		fmt.Fprintf(os.Stderr, "warning: %v\n", cfgErr)
	}

	// Route logging to a file, never stdout (stdout is the render surface).
	if logErr := setupLogging(); logErr == nil {
		defer func() {
			// surface setup errors only if run() failed for another reason
			_ = logErr
		}()
	}

	km := tui.DefaultKeymap()

	// Lead the PR list with the repositories the user is working in. Detection is
	// best-effort: outside a checkout, or with an unrelated remote, the list just
	// opens on the configured sources as before.
	detected := detectRepos(context.Background(), cfg)
	if len(cfg.Accounts) == 0 {
		// A single-identity setup still verifies up front: there is exactly one
		// check, and without it a bad login yields an empty list with no
		// explanation.
		authClient := client
		if len(detected) > 0 {
			authClient = client.WithRepo(detected[0])
		} else {
			for _, source := range cfg.Sources {
				if source.Repo != "" {
					authClient = client.WithRepo(source.Repo)
					break
				}
			}
		}
		if authErr := authClient.AuthStatus(context.Background()); authErr != nil {
			return fmt.Errorf("gh not authenticated for the selected repository — check git credentials or run `gh auth login`: %w", authErr)
		}
	}
	app := tui.NewAppWithRepo(cfg, km, client, detected)
	if len(cfg.Accounts) > 0 {
		// Verified after the first frame: two accounts cost ~1.5s of `gh auth
		// status`, and the cached rows are useful long before that returns.
		accounts := cfg.Accounts
		app.SetAccountVerifier(func(ctx context.Context) error {
			return verifyAccounts(ctx, client, accounts, os.Stderr)
		})
	}

	// NO_COLOR / non-tty gate: strip color before rendering.
	if os.Getenv("NO_COLOR") != "" || !isTTY(os.Stdout) {
		tui.SetNoColor()
	}

	// Panic recovery + terminal restore.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "ghx panicked: %v\n", r)
			if err == nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}
	}()

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, runErr := p.Run(); runErr != nil {
		return fmt.Errorf("run: %w", runErr)
	}
	return nil
}

// accountVerifier is the slice of the gh client that account verification uses.
// Taking an interface keeps the check testable without a real gh or credential
// helper on PATH, which a hermetic test cannot rely on.
type accountVerifier interface {
	AuthStatusFor(ctx context.Context, selector string) error
	CredentialToken(ctx context.Context, selector string) (string, bool)
}

// verifyAccounts checks every configured account, so an unusable identity is
// reported rather than showing up as a mysteriously empty tab. It fails only
// when no account works at all; partial failures are warnings, since one broken
// account should not withhold the other's review queue.
//
// Each check is a `gh auth status` round trip against the API — ~1.5s per
// account — so they run concurrently. Serially this was the single largest
// component of startup latency.
func verifyAccounts(ctx context.Context, client accountVerifier, accounts []config.AccountDef, warn io.Writer) error {
	type result struct {
		label string
		err   error
		token string
		hasTk bool
	}
	results := make([]result, len(accounts))
	var wg sync.WaitGroup
	for i, account := range accounts {
		i, account := i, account
		results[i].label = account.Label()
		selector := account.Selector()
		if selector == "" {
			results[i].err = fmt.Errorf("gh_user or credential_repo is required")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.AuthStatusFor(ctx, selector); err != nil {
				results[i].err = err
				return
			}
			results[i].token, results[i].hasTk = client.CredentialToken(ctx, selector)
		}()
	}
	wg.Wait()

	var authErrs []error
	authenticated := 0
	owners := make(map[string]string)
	// Report in configuration order regardless of which check finished first.
	for _, r := range results {
		if r.err != nil {
			authErrs = append(authErrs, fmt.Errorf("account %s: %w", r.label, r.err))
			continue
		}
		authenticated++
		// Two accounts resolving to one token is the silent failure this feature
		// exists to avoid: every tab looks like it queried both identities while
		// listing only one. The token is compared, never printed.
		if !r.hasTk {
			continue
		}
		if prev, dup := owners[r.token]; dup {
			fmt.Fprintf(warn,
				"warning: accounts %s and %s resolve to the same token — only one identity's PRs will be listed\n",
				prev, r.label)
			continue
		}
		owners[r.token] = r.label
	}
	if authenticated == 0 {
		return fmt.Errorf("no configured GitHub account is authenticated: %w", errors.Join(authErrs...))
	}
	for _, authErr := range authErrs {
		fmt.Fprintf(warn, "warning: %v\n", authErr)
	}
	return nil
}

// detectRepos returns the repositories to lead with, most relevant first: the
// launch directory, then the current tmux window's panes. It is the single
// definition of "the repo I am working on" — the PR list leads with all of
// them, and the admin/actions subcommands take the first as their target.
func detectRepos(ctx context.Context, cfg *config.Config) []string {
	if !cfg.RepoDetectionEnabled() {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	var found []repodetect.Result
	if cfg.PaneDetectionEnabled() {
		found = repodetect.DetectAll(ctx, wd)
	} else if r := repodetect.Detect(ctx, wd); r.Found() {
		found = []repodetect.Result{r}
	}
	out := make([]string, 0, len(found))
	for _, r := range found {
		log.Printf("detected repo %s from %s (%s)", r.Slug, r.Path, r.Source)
		out = append(out, r.Slug)
	}
	return out
}

// isTTY reports whether f is a terminal.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// setupLogging redirects the log package to ~/.config/ghx/ghx.log.
func setupLogging() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "ghx")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return mkErr
	}
	f, err := os.OpenFile(filepath.Join(dir, "ghx.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	log.SetOutput(f)
	// Keep a reference so the file isn't GC'd closed; log package owns it now.
	_ = io.Discard
	return nil
}
