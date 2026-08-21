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
↵:open · a:approve · x:close · L:labels · r:request · o:browser · y:copy · /:filter · R:refresh · ::palette · ?:help
```

A `*` marks a tab for a repository you are working in. ghx detects the launch
directory and every pane of the current tmux window — a window is usually one
task spanning several checkouts — and leads with those repos' PRs, the launch
directory first. `ghx admin` and `ghx actions` resolve their target repository
the same way, so they work from a scratch pane beside the code.

## Install

    make install    # → ~/.local/bin/ghx

Requires [`gh`][gh] installed and authenticated.

### Multiple GitHub accounts

ghx resolves credentials per repository through `git credential fill`, so it
uses the same `credential.helper`, `credential.useHttpPath`, and `includeIf`
rules as Git. For example:

```gitconfig
[credential]
  helper = ""
  helper = "!pass-git-helper $@"
  useHttpPath = true

[includeIf "gitdir:~/src/keyolk/"]
  path = ~/.gitconfig-keyolk
```

For `keyolk/ghx`, ghx asks Git for the credential associated with
`https://github.com/keyolk/ghx.git` and supplies it only to that `gh`
subprocess.

To combine cross-repository queues from multiple accounts, register each one:

```yaml
accounts:
  - name: "personal"
    gh_user: "keyolk"
  - name: "work"
    gh_user: "gavin-jeong"
```

`gh_user` names a login from `gh auth status`; ghx reads its token with `gh auth
token --user`. This is the reliable selector, because it addresses the account
directly rather than depending on Git's credential routing.

An account may instead be named by `credential_repo` — any repository whose `git
credential fill` result selects that account. That form only works when the
credential helper really does return a different token per repository path. A
global `[credential "https://github.com"] helper = !gh auth git-credential`
section (which `gh auth login` writes) resets the helper list for every
github.com URL, so a path-mapping helper such as `pass-git-helper` is bypassed
and every repository resolves to the active account's token. Verify with:

```sh
printf 'url=https://github.com/OWNER/REPO.git\n\n' | git credential fill
```

If two accounts resolve to the same token, ghx warns at startup rather than
silently listing one identity's PRs under both.

ghx stores no token in its config. Unscoped sources such
as My PRs and My reviews run once per account, merge results by update time,
and remove duplicate PRs. If one account fails, results from the others remain
visible with a warning. Detail views, comments, reviews, labels, and merges keep
using the account that found each PR.

Configured accounts are strict identity boundaries: a missing or stale account
credential is reported instead of silently falling back to another active
account. Ordinary repository-scoped calls retain the existing one-time fallback
to active `gh auth` after an HTTP 401. Permission failures remain on the
selected account. Merge uses that credential and GitHub's own permissions and
branch protection, with an explicit confirmation but no additional ghx policy.

## Features

- **PR list** — source tabs (My PRs, My reviews, Assigned, configured repos),
  merged queues across configured GitHub accounts, repo detection from the cwd
  and the tmux window's panes, client-side filtering, live polling
- **PR status** — compact `D/M/A/C/U` markers for draft, merged, approved,
  changes requested, and unresolved conversations
- **Status filters** — `f` selects one or more statuses (OR); combines with `/`
  text search using AND. Filters stay within the current source query, so merged
  PRs require a source that includes merged results (`state:all`/`state:merged`)
- **Multi-select** — `space` toggles a PR and `A` toggles all visible PRs;
  approve, close/reopen, or squash-merge the selected set with one confirmation
- **Diff viewer** — unified and side-by-side (`s` to toggle), tab-aware column
  alignment, comment line wrapping, file folding, syntax highlighting; `J`/`K`
  jump hunk to hunk and `{`/`}` file to file
- **Inline comments** — `c` on a diff line, `v` for visual range selection,
  replies via `enter` on a thread; posts to the correct path/line/side
- **Review actions** — approve (`a`), request changes (`r`), PR-level comment
  (`C`), all from the list without opening the PR
- **Checks** — CI status with bucket colors, workflow run log viewer
- **Label picker** — `L` to toggle labels with live filtering
- **Merge** — explicit strategy/confirmation, authorized by the selected repository credential
- **Copy URLs** — `y` puts the focused PR's URL, or the whole multi-selection's,
  on the clipboard
- **Command palette** — `:` for vim-style ex commands
- **NO_COLOR** — reverse-video selection, readable in monochrome
- **GraphQL / REST fallback** — nearly everything gh reads is GraphQL under the
  hood (`pr list`, `pr view`, `search prs`), and its budget is far smaller than
  REST's — a fine-grained token can also lack GraphQL entirely. When GraphQL
  refuses, the PR list, status markers, and inline review threads come from REST
  instead. Thread *resolution* has no REST representation, so those threads read
  "resolution unknown" rather than guessing, and `X` explains why it cannot
  resolve them
- **Caching** — PR lists and PR detail are cached to disk; a return visit to a
  PR you just left costs nothing. Detail entries are validated against the row's
  `updatedAt` rather than a TTL, so what you see is the PR as the list last saw
  it. `R` and `:refresh` always re-read
- **Responsive** — degrades gracefully on narrow terminals

## Configuration

    ghx config init    # write ~/.config/ghx/config.yaml

```yaml
# Optional: merge cross-repository sources from both GitHub accounts.
accounts:
  - name: "personal"
    gh_user: "keyolk"
  - name: "work"
    gh_user: "gavin-jeong"

sources:
  - name: "My PRs"
    query: "author:@me state:open"
  - name: "My reviews"
    query: "review-requested:@me state:open"
  - name: "Assigned"
    query: "assignee:@me state:open"

# Lead with the repos you're working in (cwd, then the tmux window's panes)
detect_repo: true
detect_panes: true

# Editor for ^e in the comment composer
editor: ""

# Command that opens PR URLs; empty uses $BROWSER, then open/xdg-open
browser: ""

# Command that receives copied text on stdin; empty uses pbcopy on macOS,
# then wl-copy, then "xclip -selection clipboard"
clipboard: ""

# List/preview split ratio
diff_split_ratio: 40
```

## Key bindings

Press `?` inside the TUI for the full list. Highlights:

| Key | Action |
|-----|--------|
| `enter` | open PR |
| `space` / `A` | toggle current PR / all visible PRs for multi-select |
| `a` / `x` / `M` / `o` | approve / close-reopen / squash-merge / open selected PRs (from list) |
| `L` | edit labels on selected PRs; cross-repo selections show common labels |
| `d` / `:ready` | toggle ready/draft on selected PRs |
| `f` | choose draft / merged / approved / changes requested / unresolved filters |
| `R` | reload the current source (the tab shows a spinner while it runs) |
| `/` | text search (AND with active status filters) |
| `c` | comment on diff line |
| `X` | resolve / unresolve a review thread (Comments tab) |
| `v` | visual range for multi-line comment |
| `s` | toggle unified / side-by-side diff |
| `h` / `l` | switch diff column (side-by-side) |
| `o` | fold file (diff tab) / open selected PRs in browser |
| `y` / `:copy` | copy the selected PRs' URLs to the clipboard (one per line) |
| `J` / `K` | next / previous hunk (diff tab) |
| `{` / `}` | previous / next file (diff tab) |
| `:` | command palette |
| `?` | help |

## License

MIT

[gh]: https://cli.github.com
