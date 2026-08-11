package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/repodetect"
	"github.com/keyolk/ghx/internal/tui"
	"github.com/keyolk/ghx/internal/tui/actions"
	"github.com/keyolk/ghx/internal/tui/admin"
)

// resolved from --repo or the working directory, and share the same pre-flight
// (gh installed, authenticated) and terminal handling as the PR view.
func runSubcommand(args []string, kind string) error {
	// --repo flag; the rest is ignored for now (future: --limit, etc.)
	repoFlag := ""
	remaining := args[:0]
	for _, a := range args {
		if strings.HasPrefix(a, "--repo=") {
			repoFlag = strings.TrimPrefix(a, "--repo=")
		} else if a == "--repo" {
			// handled below
		} else {
			remaining = append(remaining, a)
		}
	}
	_ = remaining

	// Pre-flight: same checks as the PR view.
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found in PATH — install from https://cli.github.com")
	}
	client := gh.NewClient(30_000_000_000)

	// Resolve the repository: --repo flag, then detection, then error.
	repo := repoFlag
	if repo == "" {
		if wd, err := os.Getwd(); err == nil {
			if r := repodetect.Detect(context.Background(), wd); r.Found() {
				repo = r.Slug
			}
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
