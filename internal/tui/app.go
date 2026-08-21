package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// Domain aliases keep the message structs readable without leaking the pr
// package name into every signature.
type (
	prSummary      = pr.Summary
	prDetail       = pr.Detail
	prCheck        = pr.Check
	prReviewThread = pr.ReviewThread
)

// viewState is the top-level screen.
type viewState int

const (
	viewPRList viewState = iota
	viewPRDetail
)

// App is the root model: state plus dispatch to the per-view models. Keeping
// view logic in prlist.go/prdetail.go is deliberate — ccx's single 8500-line
// model is the thing this layout exists to avoid.
type App struct {
	cfg    *config.Config
	client *gh.Client
	km     *Keymap

	width  int
	height int

	state  viewState
	list   *prListModel
	detail *prDetailModel

	// overlays, checked in a fixed order so one modal owns the keyboard
	composer *composer
	palette  *palette
	search   *search
	helpOpen bool

	// merge confirmation state; nil when no prompt is showing
	mergePrompt *mergePrompt

	// confirm gates the destructive-ish list actions (approve, close, ready) so
	// a keypress while scrolling cannot act on the wrong row.
	confirm *confirmPrompt

	// labels and statusFilter are modal list pickers; nil when closed.
	labels       *labelPicker
	statusFilter *statusFilterPicker

	// suggestion gates applying a review suggestion — the only action in the
	// diff view that writes a commit to someone's branch.
	suggestion *suggestionPrompt

	toast   string
	toastAt time.Time

	// copyClipboard, when set, replaces the real clipboard write. Tests set it
	// so asserting that `y` copies does not overwrite the developer's clipboard
	// and does not need pbcopy on PATH.
	copyClipboard clipboardFunc

	// verifyAccounts, when set, checks the configured GitHub accounts after the
	// first frame. It is a func rather than a direct call so this package stays
	// independent of how accounts are configured and verified.
	verifyAccounts func(context.Context) error

	spinnerFrame int
}

// mergePrompt holds the two-step merge confirmation.
type mergePrompt struct {
	strategy string // squash | merge | rebase
}

func NewApp(cfg *config.Config, km *Keymap, client *gh.Client) *App {
	return NewAppWithRepo(cfg, km, client, nil)
}

// NewAppWithRepo builds the app with detectedRepos leading the PR list, so
// starting ghx inside a checkout — or beside one in the same tmux window —
// opens on those repositories' pull requests.
func NewAppWithRepo(cfg *config.Config, km *Keymap, client *gh.Client, detectedRepos []string) *App {
	a := &App{
		cfg:      cfg,
		client:   client,
		km:       km,
		state:    viewPRList,
		composer: newComposer(),
		palette:  &palette{},
		search:   &search{},
	}
	a.list = newPRListModelWithRepo(cfg, client, km, detectedRepos)
	return a
}

// SetAccountVerifier installs a check to run after the first frame. Passing nil
// disables it. Keeping this off the startup path is what lets the cached PR
// rows render before any network round trip completes.
func (a *App) SetAccountVerifier(verify func(context.Context) error) {
	a.verifyAccounts = verify
}

func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{
		a.list.init(),
		spinnerTickCmd(),
		prListPollCmd(a.cfg.PollDuration()),
	}
	// Account verification is a `gh auth status` round trip per account. Running
	// it here rather than before tea.NewProgram keeps it off the path to the
	// first frame — the cached rows are already on screen while it runs, and a
	// broken account surfaces as a warning instead of a blank startup delay.
	if a.verifyAccounts != nil {
		verify := a.verifyAccounts
		cmds = append(cmds, func() tea.Msg {
			if err := verify(context.Background()); err != nil {
				return errMsg{err: err}
			}
			return nil
		})
	}
	return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.list.resize(msg.Width, msg.Height)
		if a.detail != nil {
			a.detail.resize(msg.Width, msg.Height)
		}
		a.composer.resize(msg.Width, msg.Height)
		return a, nil

	case tea.KeyMsg:
		return a, a.handleKey(msg)

	case spinnerTickMsg:
		// Only animate while something is loading; idle costs no redraws.
		if a.anyLoading() {
			a.spinnerFrame++
			// The list needs the frame too: its setter existed but was never
			// called, so every list-side spinner sat frozen on one glyph.
			if a.list != nil {
				a.list.setSpinnerFrame(a.spinnerFrame)
			}
			if a.detail != nil {
				a.detail.setSpinnerFrame(a.spinnerFrame)
			}
			return a, spinnerTickCmd()
		}
		return a, nil

	case toastMsg:
		a.setToast(msg.text)
		return a, nil

	case threadResolvedMsg:
		a.setToast("thread resolved")
		if a.detail != nil {
			// Only the thread list moved. Re-fetching the diff, the commits, and
			// the checks to learn that one thread closed is three requests spent
			// on data that cannot have changed.
			return a, a.detail.refreshThreads()
		}
		return a, nil

	case errMsg:
		a.setToast(errorStyle.Render("error: ") + msg.err.Error())
		return a, nil

	case prListMsg:
		// Re-arm the poll whenever a fetch settles so the cadence holds without
		// ever running two fetches at once.
		return a, tea.Batch(
			a.list.handlePRListMsg(msg),
			prListPollCmd(a.cfg.PollDuration()),
		)

	case prListTickMsg:
		return a, a.list.handlePollTick()

	case searchSubmitMsg:
		a.list.applyQuery(msg.query)
		return a, nil

	case openDetailMsg:
		return a, a.openSelected()

	case listReturnMsg:
		a.state = viewPRList
		a.detail = nil
		return a, nil

	case detailDebounceMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleDebounce(msg)

	case prDetailMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleDetailMsg(msg)

	case prDiffMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleDiffMsg(msg)

	case prThreadsMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleThreadsMsg(msg)

	case prChecksMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleChecksMsg(msg)

	case checksPollTickMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleChecksPoll()

	case runLogsMsg:
		if a.detail == nil {
			return a, nil
		}
		return a, a.detail.handleRunLogs(msg)

	case openComposerMsg:
		return a, a.composer.open(msg.target, a.width, a.height)

	case postCommentMsg:
		return a, a.postComment(msg)

	case commentPostedMsg:
		a.composer.busy = false
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.composer.close()
		a.setToast("comment posted")
		if a.detail != nil {
			return a, a.detail.refreshThreads()
		}
		return a, nil

	case reviewPostedMsg:
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.setToast(msg.action + " submitted")
		if a.state == viewPRList && a.list != nil {
			// The row's review-decision dot is now wrong; refresh so it agrees
			// with what was just submitted.
			return a, a.list.refreshCurrent()
		}
		if a.detail != nil {
			// A review changes the decision and the review list, both of which
			// live on the PR metadata. The diff is untouched.
			return a, a.detail.refreshDetail()
		}
		return a, nil

	case actionDoneMsg:
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.setToast(msg.label)
		// An action taken from the list changes what the row should say, so pull
		// the source again rather than leaving stale state on screen.
		if a.state == viewPRList && a.list != nil {
			return a, a.list.refreshCurrent()
		}
		if a.detail != nil {
			// ready/draft, close/reopen, labels — all PR metadata. A checkout
			// changes nothing on GitHub at all, but refreshing the metadata is
			// cheap enough not to be worth special-casing.
			return a, a.detail.refreshDetail()
		}
		return a, nil

	case bulkActionDoneMsg:
		if a.list != nil {
			for _, key := range msg.completed {
				delete(a.list.selected, key)
			}
		}
		if msg.err == nil {
			a.setToast(msg.label)
		} else if len(msg.completed) > 0 {
			a.setToast(fmt.Sprintf("%s; some failed: %v", msg.label, msg.err))
		} else {
			return a, errCmd(msg.err)
		}
		if a.state == viewPRList && a.list != nil && len(msg.completed) > 0 {
			return a, a.list.invalidateCachesAndRefresh()
		}
		if a.detail != nil && len(msg.completed) > 0 {
			return a, a.detail.reload()
		}
		return a, nil

	case labelsLoadedMsg:
		if a.labels == nil {
			return a, nil
		}
		a.labels.loading = false
		if msg.err != nil {
			a.labels.err = msg.err
			return a, nil
		}
		a.labels.all = msg.all
		for _, name := range msg.applied {
			a.labels.applied[name] = true
		}
		for _, name := range msg.mixed {
			a.labels.mixed[name] = true
		}
		return a, nil

	case openSuggestionMsg:
		a.suggestion = msg.prompt
		return a, nil

	case suggestionAppliedMsg:
		a.suggestion = nil
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.setToast(fmt.Sprintf("applied the suggestion to %s:%d", msg.path, msg.line))
		if a.detail != nil {
			// The branch now has a commit the loaded diff does not, so nothing on
			// screen describes the PR any more.
			return a, a.detail.reload()
		}
		return a, nil

	case openURLMsg:
		return a, a.openBrowser(msg.url)

	case mergeResultMsg:
		a.mergePrompt = nil
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.setToast("merged")
		return a, nil

	case openEditorMsg:
		return a, a.openEditor(msg.draft)

	case editorDoneMsg:
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.composer.setBody(msg.body)
		return a, nil

	case paletteRunMsg:
		return a, a.runPalette(msg.line)

	case quitMsg:
		return a, tea.Quit
	}
	return a, nil
}

// handleKey enforces the modal order: composer → merge prompt → search →
// palette → help → globals → active view. Whoever is on top owns the keyboard.
func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	if cmd, handled := a.composer.update(msg); handled {
		return cmd
	}
	if a.confirm != nil {
		return a.handleConfirmKey(msg)
	}
	if a.suggestion != nil {
		return a.handleSuggestionKey(msg)
	}
	if a.statusFilter != nil {
		return a.handleStatusFilterKey(msg)
	}
	if a.labels != nil {
		return a.handleLabelKey(msg)
	}
	if a.mergePrompt != nil {
		return a.handleMergePromptKey(msg)
	}
	if cmd, handled := a.search.update(msg); handled {
		return cmd
	}
	if cmd, handled := a.palette.update(msg); handled {
		return cmd
	}
	if a.helpOpen {
		// Help swallows everything so no key does double duty behind it.
		switch msg.String() {
		case "?", "esc", "q":
			a.helpOpen = false
		}
		return nil
	}

	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "?":
		a.helpOpen = true
		return nil
	case ":":
		a.palette.open()
		return nil
	case "/":
		if a.state == viewPRList {
			a.search.open(a.list.query)
		}
		return nil
	case "f":
		if a.state == viewPRList {
			return a.openStatusFilter()
		}
	case "q":
		return tea.Quit
	}

	// PR actions work from either view: the detail view acts on the open PR, the
	// list on the selected row.
	//
	// Inside the detail view the active tab is asked first, because a tab may own
	// a key that also names an action — `o` folds a file in the diff, and taking
	// it away for "open in browser" would break folding.
	if a.state == viewPRDetail && a.detail != nil {
		if cmd, handled := a.detailActionKey(msg); handled {
			return cmd
		}
		if cmd, handled := a.detail.update(msg); handled {
			return cmd
		}
		if cmd, handled := a.prActionKey(msg); handled {
			return cmd
		}
		return nil
	}

	if cmd, handled := a.prActionKey(msg); handled {
		return cmd
	}
	if a.state == viewPRList {
		return a.list.update(msg)
	}
	return nil
}

// detailActionKey handles keys that only make sense with a PR open. The
// PR-level actions shared with the list live in prActionKey; `c` is excluded
// from both because the diff tab owns it ("comment on this line").
func (a *App) detailActionKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "C":
		return func() tea.Msg {
			return openComposerMsg{target: composerTarget{issue: true}}
		}, true
	case "M":
		return a.startMerge(), true
	case "R":
		// Explicitly asking to refresh must not be answered from disk.
		return a.detail.reload(), true
	}
	return nil, false
}

// prActionKey handles the actions that work on whichever PR is in focus — the
// open one in the detail view, or the selected row in the list. Acting from the
// list is the point: triaging a queue should not require opening every PR.
//
// `y` sits here rather than in a per-view handler for the same reason `o` does:
// the URL you want to copy is the URL of the PR you are looking at, whichever
// screen you are looking at it from.
//
// The irreversible-ish ones (approve, close, ready) go through a confirmation,
// because in the list the cursor moves under the same fingers that press them.
func (a *App) prActionKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()
	switch key {
	// L rather than l: lowercase l is the vim "right" alias, which opens the
	// preview pane in the list and cycles tabs in the detail view.
	case "a", "x", "L", "r", "M", "d", "o", "y":
		// fall through to the target lookup below
	default:
		return nil, false
	}

	switch key {
	case "a":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		return a.askConfirmTargets(confirmApprove, targets), true
	case "x":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		if a.state == viewPRList && a.list != nil && len(a.list.selected) > 0 {
			kind := confirmClose
			if len(targets) > 1 {
				kind = confirmToggleState
			} else if targets[0].state == "CLOSED" {
				kind = confirmReopen
			}
			return a.askConfirmTargets(kind, targets), true
		}
		if targets[0].state == "CLOSED" {
			return a.askConfirm(confirmReopen, targets[0]), true
		}
		return a.askConfirm(confirmClose, targets[0]), true
	case "M":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		return a.askConfirmTargets(confirmMerge, targets), true
	case "L":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		return a.openLabelPickerTargets(targets), true
	case "d":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		return a.askConfirmTargets(confirmReady, targets), true
	case "o":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		return a.openTargetsInBrowser(targets), true
	case "y":
		targets, ok := a.actionTargets()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		return a.copyTargets(targets), true
	}

	t, ok := a.currentTarget()
	if !ok {
		return errCmd(fmt.Errorf("no pull request selected")), true
	}
	switch key {
	case "r":
		// Requesting changes without saying why isn't useful, so this opens the
		// composer and submits the review with its body.
		return func() tea.Msg {
			return openComposerMsg{target: composerTarget{
				issue: true, review: "request-changes",
				prNumber: t.number, repo: t.repo, credentialRepo: t.credentialRepo,
			}}
		}, true
	}
	return nil, false
}

// openTargetInBrowser opens the PR's web page, resolving the URL when the row
// did not carry one.
//
// The target's own URL is used rather than re-reading the focused row: with a
// multi-selection every target would otherwise resolve to whatever the cursor
// happens to sit on.
func (a *App) openTargetInBrowser(t actionTarget) tea.Cmd {
	if t.url != "" {
		return a.openBrowser(t.url)
	}
	client := a.clientFor(t)
	n := t.number
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		url, err := client.PRPermalink(c, n)
		if err != nil {
			return errMsg{err: err}
		}
		return openURLMsg{url: url}
	}
}

// openTargetsInBrowser opens every selected PR. Opening is read-only, so it
// runs without a confirmation and leaves the selection intact — unlike close or
// merge, looking at a PR does not consume the mark that chose it.
//
// Targets whose row already carried a URL are opened together, so the batch
// reports one result instead of a toast per PR. Only a target missing its URL
// needs an API round trip, which stays its own command.
func (a *App) openTargetsInBrowser(targets []actionTarget) tea.Cmd {
	if len(targets) == 1 {
		return a.openTargetInBrowser(targets[0])
	}
	var urls []string
	var cmds []tea.Cmd
	for _, target := range targets {
		if target.url != "" {
			urls = append(urls, target.url)
			continue
		}
		if cmd := a.openTargetInBrowser(target); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := a.openBrowserAll(urls); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

func (a *App) anyLoading() bool {
	if a.list != nil && a.list.loading() {
		return true
	}
	if a.detail != nil && a.detail.loading() {
		return true
	}
	return false
}

func (a *App) setToast(s string) {
	a.toast = s
	a.toastAt = time.Now()
}

// openSelected switches to the detail view for the list's selection.
func (a *App) openSelected() tea.Cmd {
	if a.list == nil {
		return nil
	}
	item, ok := a.list.selectedItem()
	if !ok {
		return nil
	}
	a.state = viewPRDetail
	a.detail = newPRDetailModelWithCredential(
		a.cfg, a.client, a.km, item.pr.Number, item.pr.Repo, item.pr.CredentialRepo,
	)
	// The row's updatedAt is what makes the detail cache safe to use: GitHub
	// bumps it on every push, comment, review, and label change, so an entry
	// stamped with the same value describes the same PR.
	a.detail.updatedAt = item.pr.UpdatedAt
	a.detail.resize(a.width, a.height)
	return a.detail.load()
}

// --- actions ---

// submitReview approves or requests changes on the current PR.
// --- shared helpers ---

func joinVertical(parts ...string) string {
	return strings.Join(parts, "\n")
}

// truncateFooter clips a one-line footer to the terminal width, cell-accurately.
func truncateFooter(s string, w int) string {
	if w <= 0 {
		return s
	}
	out, _ := truncateExact(s, w)
	return out
}

// ctx returns a background context; callers attach their own timeouts.
func ctx() context.Context { return context.Background() }

var _ = lipgloss.Width
