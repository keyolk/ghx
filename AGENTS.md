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
- `internal/tui/account_search.go` — 복수 GitHub account 검색 병합과 중복 제거
- `internal/gh/fallback.go` — GraphQL 거부/rate-limit 판별 (REST 재시도 여부)
- `internal/gh/rest.go` — 목록·검색·상태·리뷰 스레드의 REST 대체 경로
- `internal/tui/clipboard.go` — `y` / `:copy`, PR URL 복사 (외부 clipboard 명령)
- `internal/tui/detail_diff_jump.go` — diff의 hunk/file 단위 점프 (`J`/`K`, `{`/`}`)
- `internal/tui/detail_cache.go` — PR 상세 디스크 캐시 (updatedAt로 유효성 판정)
- `internal/tui` — Bubble Tea app (split per view, files <500 lines)

## Conventions

- Files stay under 500 lines; split when they grow.
- Styles are semantic tokens in styles.go, never raw hex in render paths.
- Tabs and CJK width handled via expandTabs / lipgloss.Width.
- Tests are hermetic: forceColor for ANSI assertions, t.Setenv for isolation.
  Side effects that reach outside the process (clipboard writes) go through an
  injected func so a test never touches the developer's real clipboard.
- gh 서브커맨드는 대부분 GraphQL이다 — `pr list`/`pr view`/`pr checks`/`search prs`
  전부 (`pr diff`만 REST). GraphQL 예산은 REST보다 훨씬 작고 fine-grained PAT은
  아예 GraphQL 없이 발급될 수 있으므로, 새 읽기 경로를 추가하면 `isGraphQLUnavailable`
  분기로 REST 대체를 함께 넣는다. REST가 답할 수 없는 것(스레드 resolution)은
  추측하지 말고 unknown으로 남긴다 — `pr.ReviewThread.ResolutionKnown`.
- 요청 수를 늘리는 변경은 **측정하고 커밋 메시지에 남긴다.** 출력이 옳아도 비용이
  틀릴 수 있고, 결과만 단언하는 테스트는 그 회귀를 통과시킨다. `gh`는 단일 관문
  (`Client.exec`)을 지나므로 PATH에 로깅 shim을 두면 실제 TUI를 몰면서 전수 집계할
  수 있다 — `internal/gh/rest_cost_test.go`의 `countingGH`, `refetch_scope_test.go`의
  `countBatch`가 그 단언을 테스트로 고정한 예다.
- 액션 후 재페치는 **그 액션이 바꾼 것만** 받는다. 스레드 resolve에 diff를 다시 받지
  않는다. 다만 좁힌 경로도 캐시는 evict해야 한다 — ghx 안에서 한 액션은 리스트 행의
  `updatedAt`을 움직이지 않으므로, 캐시가 방금 바뀐 상태를 계속 내놓는다.
- `App.View` must render at most `height` rows: it always draws a title line and
  a footer, so the body is sized to `contentRows()`, never to `a.height`. An
  overflowing frame loses its TOP rows — bubbletea keeps the last `height` lines
  — which silently hides the title and the tab strip. `view_height_test.go`
  guards this for every tab and every overlay.
