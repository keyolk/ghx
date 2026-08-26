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
- `internal/pr/suggestion.go` — 코멘트의 ```suggestion 블록 파싱
- `internal/gh/suggestion.go` — suggestion 적용 (createCommitOnBranch, expectedHeadOid)
- `internal/tui/detail_comments.go` — `threadIdentity` (REST 스레드의 안정적 식별자)
- `internal/gh/budget.go` — 계정 GraphQL 예산 관측 (응답에 실려오는 rateLimit)
- `internal/tui/listpane.go` — 서브커맨드 TUI 공용 스크롤 리스트·필터 (`ListPane`)
- `internal/tui/admin` — `ghx admin`: People/Teams/보호규칙/릴리스/브랜치/태그/웹훅
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
- REST로 복구된 스레드는 `ID`가 없다 — REST에 thread 객체 자체가 없다. UI에서
  스레드를 식별할 때는 `t.ID`가 아니라 `threadIdentity(t)`를 쓴다. 빈 ID로 비교하면
  전부 첫 스레드에 매칭돼, `A`가 **다른 스레드의** suggestion을 그 스레드의 줄에
  커밋한다.
- suggestion 적용은 `isOutdated`로 게이팅한다. 적용하면 그 스레드 자신이 outdated가
  되고 `line`이 null로 떨어지는데, 남은 `originalLine`이 새 diff에도 대개 존재해서
  행은 멀쩡히 렌더된다 — 좌표만으로는 stale인지 알 수 없다.
- `gh api graphql`에 **객체 변수를 플래그로 넘길 수 없다.** `-f`와 `--raw-field`는
  같은 플래그이고 둘 다 값을 JSON *문자열*로 보낸다. 본문 전체를 `--input <file>`로
  넘긴다 (stdin은 안 된다 — credential fallback이 명령을 재실행한다).
- 네트워크가 필요한 경로는 `-tags e2e` 테스트로 실제로 태워본다. `suggestion_e2e_test.go`,
  `rest_parity_e2e_test.go`가 그 예이고, 둘 다 `GHX_E2E_REPO`/`GHX_E2E_PR`로 게이팅된다.
  suggestion 적용 경로가 단 한 번도 성공할 수 없는 상태로 머지됐던 이유가 이것이다 —
  파서와 줄 연산은 유닛 테스트가 덮었지만 요청 자체는 아무도 보내보지 않았다.
- 백그라운드 폴링은 인스턴스별이지만 **GraphQL 예산은 계정별**이다. tmux 창마다 켜둔
  ghx가 여러 개면 서로를 모르는 채 한 풀을 나눠 쓴다 — 실측으로 6개 × 30초 폴링이
  분당 ~141포인트를 태워 5,000 예산을 35분에 비웠다. 그래서 (1) 키 입력이 없으면
  `idle_after` 뒤에 `idle_poll_interval`로 물러나고, (2) enrichment 응답에 실려오는
  `rateLimit`으로 남은 예산을 **공짜로** 읽어 더 늘린다. 폴링 비용을 바꾸는 변경은
  이 두 축을 같이 본다.
- **프레임은 절대 `height`보다 커지면 안 된다.** bubbletea 렌더러는 넘치는 프레임의
  **뒤쪽** `height`줄을 남긴다(standard_renderer.go:186) — 즉 잘려나가는 건 위쪽,
  title과 tab strip이다. 화면이 있는 자리를 알려주는 두 줄이 사라지므로 "탭이 없어졌다"로
  보인다. 본문은 `a.height`가 아니라 `contentRows()`(= height − title − 헤더 − footer)로
  잘라 `FitRows`로 정규화한다. `FitRows`는 패딩도 한다 — 짧은 리스트에서 footer가
  화면 중간에 떠오르는 것을 막는다. 메인 앱은 #15에서 고쳤고 서브커맨드는 남아 있었다.
- **리스트는 자기 행 안에서 스크롤한다.** 전부 그려놓고 렌더러에 맡기면 위 항목이 된다.
  `tui.ListPane`이 offset을 소유하고, 커서가 창 밖으로 나갈 때만 최소한으로 움직인다
  (이미 보이는 커서를 재중앙정렬하지 않는다). 필터로 리스트가 줄면 offset이 끝을 넘어
  남을 수 있어 `scrollTo`가 마지막에 clamp한다 — 없으면 필터를 넓혔을 때 존재하는 행이
  빈 화면으로 보인다.
- **커서는 항상 필터링된 슬라이스를 인덱싱한다.** 원본을 인덱싱하면 필터 이전에 그
  자리에 있던 행에 액션이 간다 — `d`/`r`/`c`에서는 다른 대상을 지우거나 재실행한다.
  `visible*()` 헬퍼를 통해서만 행에 접근한다.
- **검색 프롬프트는 열려 있는 동안 키보드를 독점한다.** 그렇지 않으면 `j`, `q`, `r`,
  숫자가 든 질의를 입력할 수 없다 — 이름과 워크플로 이름에는 전부 들어간다.
- **`collaborators` 엔드포인트는 team을 개인으로 평탄화한다.** 응답에 team 정보가 없어서
  org 레포는 "개인 150명"으로 보인다 — 실측: `sendbird/ops-k8s`는 150명 중 **직접 권한이
  1명**, 나머지 149명은 19개 팀 경유다. `affiliation=direct`를 한 번 더 불러 대조해야
  구분되고, 구조 자체는 `repos/{repo}/teams`에만 있다. 팀 경유 사용자에게 `d`를 눌러도
  레포에는 해제할 것이 없다 — 그래서 행이 출처를 밝힌다.
- **타이머를 무장하는 곳은 `Init`과 `prListMsg` 단 두 곳**이다. 다른 데서 `armPoll`을
  부르면 *현재* generation 타이머가 둘이 되고, generation 검사는 둘 다 현행이라
  구분하지 못한다 — cadence가 조용히 2배가 된다. `idle_poll_test.go`의
  `TestWakingDoesNotDuplicateThePollChain`이 이 불변식을 고정한다.
- `App.View` must render at most `height` rows: it always draws a title line and
  a footer, so the body is sized to `contentRows()`, never to `a.height`. An
  overflowing frame loses its TOP rows — bubbletea keeps the last `height` lines
  — which silently hides the title and the tab strip. `view_height_test.go`
  guards this for every tab and every overlay.
