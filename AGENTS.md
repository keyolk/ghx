# ghx

A TUI for reviewing GitHub pull requests: list, diff, inline comments, approve,
request changes, checks, and workflow run logs. Wraps the `gh` CLI for data and
actions.

## Build

    make build      # → bin/ghx
    make run        # build + run
    make install    # → ~/.local/bin/ghx
    make test       # go test ./...
    make vet        # go vet ./...

## Layout

- `main.go` — entry point, gh pre-flight, repo detection, tea.NewProgram
- `internal/gh` — gh CLI wrapper (search, view, diff, checks, reviews, runs)
- `internal/pr` — domain types
- `internal/diff` — unified diff parser with LEFT/RIGHT line mapping
- `internal/config` — ~/.config/ghx/config.yaml
- `internal/repodetect` — cwd/tmux repo detection
- `internal/gh/credential.go` — repo별 Git credential 선택과 gh 인증 fallback
- `internal/tui` — Bubble Tea app (split per view, files <500 lines)

## Conventions

- Files stay under 500 lines; split when they grow.
- Styles are semantic tokens in styles.go, never raw hex in render paths.
- Tabs and CJK width handled via expandTabs / lipgloss.Width.
- Tests are hermetic: forceColor for ANSI assertions, t.Setenv for isolation.
