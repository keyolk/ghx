package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
	"github.com/keyolk/ghx/internal/tui/actions"
	"github.com/keyolk/ghx/internal/tui/admin"
)

// parseRepoFlag pulls --repo out of a subcommand's argv, in both the
// `--repo=owner/name` and `--repo owner/name` forms, and returns the remaining
// arguments. Supporting only the joined form silently drops the separated one,
// which then falls through to detection and targets the wrong repository.
func parseRepoFlag(args []string) (repo string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--repo="):
			repo = strings.TrimPrefix(a, "--repo=")
		case a == "--repo":
			if i+1 < len(args) {
				repo = args[i+1]
				i++
			}
		default:
			rest = append(rest, a)
		}
	}
	return repo, rest
}

// resolved from --repo or the surrounding tmux window, and share the same
// pre-flight (gh installed, authenticated) and terminal handling as the PR view.
func runSubcommand(args []string, kind string) error {
	// --repo flag; the rest is ignored for now (future: --limit, etc.)
	repoFlag, remaining := parseRepoFlag(args)
	_ = remaining

	// Pre-flight: same checks as the PR view.
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found in PATH — install from https://cli.github.com")
	}
	client := gh.NewClient(30_000_000_000)

	// Resolve the repository: --repo flag, then detection, then error. Detection
	// matches the PR list's, so `ghx admin` in a scratch pane targets the same
	// repository the list would have led with.
	repo := repoFlag
	if repo == "" {
		cfg, cfgErr := config.Load()
		if cfgErr != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", cfgErr)
		}
		if detected := detectRepos(context.Background(), cfg); len(detected) > 0 {
			repo = detected[0]
		}
	}
	if repo == "" {
		return fmt.Errorf("no repository specified — use --repo owner/name or run inside a git checkout")
	}
	client = client.WithRepo(repo)
	if err := client.AuthStatus(context.Background()); err != nil {
		return fmt.Errorf("gh not authenticated for %s — check git credentials or run `gh auth login`: %w", repo, err)
	}

	if os.Getenv("NO_COLOR") != "" || !isTTY(os.Stdout) {
		tui.SetNoColor()
	}

	var model tea.Model
	switch kind {
	case "admin":
		model = admin.NewApp(client, repo)
	case "actions":
		model = actions.NewApp(client, repo)
	default:
		return fmt.Errorf("unknown subcommand %q", kind)
	}

	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "ghx %s panicked: %v\n", kind, r)
		}
	}()

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}
