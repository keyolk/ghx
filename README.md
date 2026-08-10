# ghx

A terminal UI for reviewing GitHub pull requests — list, diff, inline comments,
approve, request changes, checks, and workflow run logs. Wraps the [`gh` CLI][gh]
for data and actions.

```
ghx  platform-tools · 50
[1 platform-tools*(50)] 2 My PRs(12)  3 My reviews(50)  4 Assigned
#898    ✓ platform-tools     [CPLAT-10365] Add first/last and five-page jump controls to the pager
#897    ● platform-tools     [CPLAT-10365] Bump Makefile default image tag to v0.0.191
#854    ● platform-tools     Bump brace-expansion from 5.0.6 to 5.0.8 in /drongo/web/frontend
↵:open · a:approve · x:close · L:labels · r:request · o:browser · /:filter · ::palette · ?:help
```

The `*` marks the tab for the repository you launched from — ghx detects the
current directory (or the active tmux pane) and leads with that repo's PRs.

## Install

    make install    # → ~/.local/bin/ghx

Requires [`gh`][gh] installed and authenticated.

## Features

- **PR list** — source tabs (My PRs, My reviews, Assigned, configured repos),
  repo detection from cwd/tmux, client-side filtering, live polling
- **PR status** — compact `M/A/C/U` markers for merged, approved, changes
  requested, and unresolved conversations
- **Status filters** — `f` selects one or more statuses (OR); combines with `/`
  text search using AND. Filters stay within the current source query, so merged
  PRs require a source that includes merged results (`state:all`/`state:merged`)
- **Multi-select** — `space` toggles a PR and `A` toggles all visible PRs;
  approve or close/reopen the selected set with one confirmation
- **Diff viewer** — unified and side-by-side (`s` to toggle), tab-aware column
  alignment, comment line wrapping, file folding, syntax highlighting
- **Inline comments** — `c` on a diff line, `v` for visual range selection,
  replies via `enter` on a thread; posts to the correct path/line/side
- **Review actions** — approve (`a`), request changes (`r`), PR-level comment
  (`C`), all from the list without opening the PR
- **Checks** — CI status with bucket colors, workflow run log viewer
- **Label picker** — `L` to toggle labels with live filtering
- **Merge gate** — two-step confirmation, blocked by session policy by default
- **Command palette** — `:` for vim-style ex commands
- **NO_COLOR** — reverse-video selection, readable in monochrome
- **Responsive** — degrades gracefully on narrow terminals

## Configuration

    ghx config init    # write ~/.config/ghx/config.yaml

```yaml
sources:
  - name: "My PRs"
    query: "author:@me state:open"
  - name: "My reviews"
    query: "review-requested:@me state:open"
  - name: "Assigned"
    query: "assignee:@me state:open"

# Lead with the repo you're in (cwd or active tmux pane)
detect_repo: true

# Editor for ^e in the comment composer
editor: ""

# List/preview split ratio
diff_split_ratio: 40
```

## Key bindings

Press `?` inside the TUI for the full list. Highlights:

| Key | Action |
|-----|--------|
| `enter` | open PR |
| `space` / `A` | toggle current PR / all visible PRs for multi-select |
| `a` / `x` | approve / close or reopen selected PRs (from list) |
| `L` | edit labels on the focused PR |
| `f` | choose merged / approved / changes requested / unresolved filters |
| `/` | text search (AND with active status filters) |
| `c` | comment on diff line |
| `v` | visual range for multi-line comment |
| `s` | toggle unified / side-by-side diff |
| `h` / `l` | switch diff column (side-by-side) |
| `o` | fold file (diff tab) / open in browser |
| `:` | command palette |
| `?` | help |

## License

MIT

[gh]: https://cli.github.com
