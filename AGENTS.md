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
- `internal/repowatch` — 로컬 git 이벤트 감지 (commit/checkout/push/fetch), 요청 0건
- `internal/tui/workspace.go` — tmux window 재탐지 + git 이벤트 기반 갱신
- `internal/tui/repostore.go` — repo 사용 이력(방문수 × 최근성) 랭킹, `~/.config/ghx/repos.json`
- `internal/tui/repo_picker.go` — `e` / `:repo`, 임의 repo를 탭으로 여는 피커
- `internal/tui/admin` — `ghx admin`: People/Teams/보호규칙/릴리스/브랜치/태그/웹훅
- `internal/tui/admin/actions.go` — admin 쓰기(협업자·팀 권한·팀 멤버)와 확인 프롬프트
- `internal/tui/admin/prompt.go` — 로그인 입력(타이핑) / 권한 선택(순환) 프롬프트
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
- **탐지와 갱신은 startup 고정값이 아니다.** pane이 나중에 다른 checkout으로 `cd`하거나
  pane이 새로 열려도 따라간다(`workspace.go`, 5초 sweep). sweep은 **요청을 쓰지 않는다** —
  `tmux list-panes` 한 번과 stat 몇 개뿐이라, unfocused여도 계속 돈다. 실측: 조용한
  sweep 20회 = gh 호출 **0건**, git 이벤트 1회 = `pr list` **1건**(REST).
  `workspace_cost_test.go`가 이 두 숫자를 고정한다.
- **git 이벤트가 트리거하는 것은 그 repo에 pin된 탭뿐이다.** cross-repo 검색 큐는 건드리지
  않는다 — 그쪽은 희소한 GraphQL search 예산을 쓰고, 애초에 unfocused에서 폴을 세운 이유가
  그 예산이다. 큐는 다음 일반 폴에서 따라잡는다.
- **탭은 추가만 하고 제거하지 않는다.** pane이 닫혀도 탭은 남는다 — 지우면 뒤 탭이 전부
  재번호되어 1-9 점프 키가 읽던 큐가 아닌 곳에 떨어진다. 새 탭은 **선택하지 않는다**:
  다른 pane이 `cd` 했다고 커서가 움직이는 건 요청의 반대다.
- **watcher는 slug가 아니라 checkout 경로별로 둔다.** worktree는 같은 slug의 별도 checkout이고
  자기 HEAD/reflog를 갖는다 — slug로 키를 잡으면 먼저 탐지된 pane만 감시하고 나머지 pane의
  커밋은 통째로 안 보인다. 이 레포가 실제로 그 레이아웃으로 작업된다. 새로고침 단계에서
  `touchedSlugs`로 다시 합쳐 한 탭을 두 번 fetch하지 않는다.
- **baseline은 sweep이 뜬 스냅샷을 그대로 저장한다.** 핸들러에서 다시 뜨면 그 사이에 들어온
  쓰기가 새 baseline에 흡수되어 **영영 보고되지 않는다** — 한 번 보이고, 관측됐다고 기록되고,
  그에 대한 fetch는 나가지 않는다.
- **`loadSource`는 `inFlight`를 건드리지 않는다.** 그건 백그라운드 폴 체인의 것이고, 폴은
  타이머를 정확히 하나만 무장한다 — git 이벤트 경로가 그 슬롯을 잡으면 폴이 영구히 멈춘다.
  superseded 응답은 소스별 generation이 버린다.
- **선택 밴드는 행 안의 스타일을 전부 덮는다.** 색이나 취소선으로만 표현한 상태는 커서가
  올라간 순간 사라진다 — 그리고 그 행이 지금 읽고 있는 행이다. 상태는 **밴드 밖의 글리프**로
  낸다(`threadStateGlyph`): 열로 정렬되고, 문자라서 NO_COLOR에서도 읽히고, 밴드가 삼키지
  못한다. review thread가 이 사례였다 — resolved 여부가 `[resolved]` 태그 + dim + 취소선
  뿐이었는데 선택 시 셋 다 무력화됐다.
- **상태는 값으로 들고 다닌다. 렌더된 문자열을 검사하지 않는다.** diff 뷰가
  `strings.Contains(text, "[resolved]")`로 스타일을 골랐는데, 그 단어를 인용한 코멘트가
  resolved로 렌더됐다. `diffRow.state`처럼 행에 실어 보낸다.
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
- **백엔드가 있다고 도달 가능한 게 아니다.** `internal/gh/admin.go`에 쓰기 9개가 있었는데
  TUI에서 닿는 것이 **0개**였다. `RemoveCollaborator`는 확인 프롬프트 배관까지 있었지만
  그것을 여는 키가 없어 실행될 수 없는 코드였다. 새 gh 래퍼를 추가하면 어느 키가 그것을
  호출하는지 같이 정한다 — footer의 힌트가 그 확인 지점이다.
- **admin 쓰기는 전부 확인을 거치고, `y` 외의 키는 동의가 아니다.** 접근 권한 변경은
  여기서 되돌릴 수 없다(재추가는 "제거하지 않은 것"과 다른 사건이고, 팀 권한 회수는 그
  뒤의 전원을 데려간다). `write_test.go`의 `TestOnlyYConfirmsAWrite`가 고정한다.
- **repo 범위를 벗어나는 액션은 프롬프트가 스스로 말해야 한다.** 팀 멤버 추가/제거는
  **org 변경**이고 그 팀이 닿는 모든 repo에 영향을 준다 — 화면 제목은 repo 하나인데.
  경고는 문장 **앞**에 온다: footer는 잘리고, 잘리면 안 되는 부분이 경고이기 때문이다
  (초안은 100칸에서 "EVERY repo that team reaches"가 잘려 평범한 y/n으로 읽혔다).
  footer 마커와 문장 양쪽에 넣지 않는다 — 같은 말을 두 번 하게 된다.
- **admin 쓰기는 성공 시 재조회가 액션의 일부다.** 행이 그대로면 조용히 실패한 것과
  구분되지 않는다(토스트는 4초짜리 텍스트이고 그 아래 리스트는 옛 상태다). 실패는
  `a.err`이 아니라 토스트로 낸다 — 리스트를 에러 화면으로 갈아치우면 다음 판단에 필요한
  바로 그 상태가 사라진다. 팀 멤버 쓰기는 `teamMembers` 캐시를 **삭제**해야 재조회가
  실제로 나간다.
- **`collaborators` 엔드포인트는 team을 개인으로 평탄화한다.** 응답에 team 정보가 없어서
  org 레포는 "개인 150명"으로 보인다 — 실측: `sendbird/ops-k8s`는 150명 중 **직접 권한이
  1명**, 나머지 149명은 19개 팀 경유다. `affiliation=direct`를 한 번 더 불러 대조해야
  구분되고, 구조 자체는 `repos/{repo}/teams`에만 있다. 팀 경유 사용자에게 `d`나 `p`를
  눌러도 레포에는 바꿀 것이 없다 — 그래서 행이 출처를 밝히고, 눌렀을 때 **이유를 말하며
  거부한다**(Teams 탭으로 안내). 조용히 아무 일도 안 하면 "via team" 표시가 장식이 된다.
  실질 접근 제어는 팀의 권한이므로 그쪽(`p`)이 실제로 효과가 있는 편집이다.
- **폴링되는 리스트는 폴이 죽어도 똑같이 보인다.** 행이 그대로이므로 만료된 credential,
  중단된 폴, 정말 변한 게 없는 큐가 화면상 구분되지 않는다. 그래서 타이틀 우측이 (1) 지금
  행의 나이, (2) 폴 cadence를 항상 함께 낸다 — 둘 중 하나만으로는 오해를 부른다. cadence는
  설정값과 같아도 표시한다: 생략하면 "30초마다 폴링"과 "아예 폴링 안 함"이 같은 화면이 되고,
  그게 바로 여기서 답해야 할 질문이다. 나이는 **소스별**이다(보이는 탭만 폴링하므로).
  디스크 캐시로 seed된 행은 파일의 `SavedAt`을 물려받는다 — 재시작을 "방금"으로 찍는 것이
  이 표시가 막으려는 바로 그 거짓말이다. unfocused에서 `paused`만 내면 이제 반대 방향의
  거짓말이 된다 — 폴은 멈췄지만 push하는 repo는 갱신되므로, watcher가 있으면
  `unfocused · on git`으로 구분한다.
- **`inFlight`는 superseded 응답에서도 해제한다.** 그건 "요청이 떠 있는가"이지 "답이
  쓸모있었는가"가 아니다. 폐기 경로에서 잡고 있으면 폴 체인이 영구히 막히고, 타이틀이
  "fetching"으로 굳는다.
- **타이머를 무장하는 곳은 `Init`과 `prListMsg` 단 두 곳**이다. 다른 데서 `armPoll`을
  부르면 *현재* generation 타이머가 둘이 되고, generation 검사는 둘 다 현행이라
  구분하지 못한다 — cadence가 조용히 2배가 된다. `idle_poll_test.go`의
  `TestWakingDoesNotDuplicateThePollChain`이 이 불변식을 고정한다.
  sweep 타이머(`armWorkspace`)는 **별개 체인**이다: 자기 generation을 쓰고, 무장하는
  곳도 `Init`과 `workspaceScanMsg` 둘뿐이다. 두 체인을 엮지 않는다 — sweep은 예산을
  쓰지 않아 unfocused에서도 돌아야 하고, 폴은 그 반대다.
- `App.View` must render at most `height` rows: it always draws a title line and
  a footer, so the body is sized to `contentRows()`, never to `a.height`. An
  overflowing frame loses its TOP rows — bubbletea keeps the last `height` lines
  — which silently hides the title and the tab strip. `view_height_test.go`
  guards this for every tab and every overlay.
