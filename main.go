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

	// Lead the PR list with the repository the user is in. Detection is
	// best-effort: outside a checkout, or with an unrelated remote, the list just
	// opens on the configured sources as before.
	detected := ""
	if cfg.RepoDetectionEnabled() {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			if r := repodetect.Detect(context.Background(), wd); r.Found() {
				detected = r.Slug
				log.Printf("detected repo %s from %s (%s)", r.Slug, r.Path, r.Source)
			}
		}
	}
	if len(cfg.Accounts) > 0 {
		var authErrs []error
		authenticated := false
		for _, account := range cfg.Accounts {
			if account.CredentialRepo == "" {
				authErrs = append(authErrs, fmt.Errorf("account %s: credential_repo is required", account.Name))
				continue
			}
			accountClient := client.WithCredentialRepo(account.CredentialRepo)
			if authErr := accountClient.AuthStatus(context.Background()); authErr != nil {
				authErrs = append(authErrs, fmt.Errorf("account %s: %w", account.Name, authErr))
				continue
			}
			authenticated = true
		}
		if !authenticated {
			return fmt.Errorf("no configured GitHub account is authenticated: %w", errors.Join(authErrs...))
		}
	} else {
		authClient := client
		if detected != "" {
			authClient = client.WithRepo(detected)
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
