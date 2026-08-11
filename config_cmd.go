package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/keyolk/ghx/internal/config"
)

func printUsage() {
	fmt.Print(`ghx — review GitHub pull requests in the terminal

Usage:
  ghx                 open the PR review TUI
  ghx admin           manage a repository (collaborators, branches, releases, …)
  ghx actions         manage GitHub Actions (runs, workflows, logs)
  ghx config <cmd>    inspect or edit configuration
  ghx version         print the version

Options:
  --repo owner/name   target repository (default: detected from cwd or tmux)

Config commands:
  ghx config path     print the config file path
  ghx config view     print the effective configuration
  ghx config edit     open the config file in $EDITOR
  ghx config init     write a starter config file

Keys are listed under ? inside the TUI.
`)
}

func runConfig(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	p, err := config.Path()
	if err != nil {
		return err
	}

	switch args[0] {
	case "path":
		fmt.Println(p)
		return nil

	case "view":
		cfg, loadErr := config.Load()
		// A broken file still prints the defaults that would be used, but the
		// parse failure is reported rather than passed off as the config.
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", loadErr)
		}
		data, err := cfg.Marshal()
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil

	case "edit":
		if err := ensureConfigFile(p); err != nil {
			return err
		}
		cfg, _ := config.Load()
		editor := cfg.EditorCommand()
		cmd := exec.Command(editor, p)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()

	case "init":
		if _, err := os.Stat(p); err == nil {
			// Overwriting would silently discard the user's sources.
			return fmt.Errorf("%s already exists — edit it with `ghx config edit`", p)
		}
		if err := ensureConfigFile(p); err != nil {
			return err
		}
		fmt.Println("wrote", p)
		return nil
	}
	return fmt.Errorf("unknown config command %q (path|view|edit|init)", args[0])
}

// starterConfig is what `ghx config init` writes. It is a literal rather than
// marshalled defaults so the options can carry comments — a generated file tells
// you the current values but not which knobs exist or what they do.
const starterConfig = `# ghx configuration

# Accounts used for cross-repository sources such as My PRs and My reviews.
# credential_repo is any repository whose git credential fill result selects
# that account. Tokens stay in Git's credential helper and are never stored here.
# accounts:
#   - name: "personal"
#     credential_repo: "keyolk/ghx"
#   - name: "work"
#     credential_repo: "sendbird/platform-tools"

# PR list sources. Each becomes a tab; 1-9 jumps between them.
sources:
  - name: "My PRs"
    query: "author:@me state:open"
  - name: "My reviews"
    query: "review-requested:@me state:open"
  - name: "Assigned"
    query: "assignee:@me state:open"
  - name: "Mentioned"
    query: "mentions:@me state:open"
  # Pin a repository to its own tab. A repo-scoped source uses the REST listing,
  # which has a far more generous rate limit than the cross-repo search.
  # - name: "platform-tools"
  #   query: "state:open"
  #   repo: "keyolk/ghx"

# Lead the list with the repository you launched from (or the active tmux pane's
# repository, when the launch directory is not a checkout). The tab is marked
# with * and the configured sources follow behind it. Set to false to always
# open on the sources above.
detect_repo: true

# Editor for ^e in the comment composer. Empty uses $EDITOR, then vi.
editor: ""

# How often to refresh the visible source.
poll_interval: "30s"

# List width as a percentage of the terminal, in the list/preview split.
diff_split_ratio: 40
`

// ensureConfigFile creates the config file with the starter template if it is
// missing.
func ensureConfigFile(p string) error {
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(starterConfig), 0o644)
}
