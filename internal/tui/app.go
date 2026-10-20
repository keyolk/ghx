package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/prefs"

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
	km     keys.Keymap

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
	// helpOff is the help overlay's scroll offset, clamped where it is stored
	// (clampHelp). helpStack and helpOpts are the view the overlay documents,
	// captured when it opened.
	helpOff   int
	helpStack []keys.Ctx
	helpOpts  keys.Opts

	// saver writes the UI state worth keeping across restarts (ui_state.go);
	// lastUI is what it last saved, so only a change is written. quitting is
	// set once the final write is under way.
	saver    prefs.Saver[UIState]
	lastUI   UIState
	quitting bool

	// merge confirmation state; nil when no prompt is showing
	mergePrompt *mergePrompt

	// confirm gates the destructive-ish list actions (approve, close, ready) so
	// a keypress while scrolling cannot act on the wrong row.
	confirm *confirmPrompt
	// mergeFailure, when set, is the box explaining a merge that did not happen.
	mergeFailure *mergeFailure

	// labels and statusFilter are modal list pickers; nil when closed.
	labels       *labelPicker
	statusFilter *statusFilterPicker

	// repos is the open-a-repository picker; nil when closed.
	repos *repoPicker

	// repoStore ranks repositories by use so that picker leads with the handful
	// someone actually works in. It outlives the process — the ranking is only
	// useful once it has watched a few sessions.
	repoStore *repoStore
	// startupSave persists the launch directory's visit; Init runs it.
	startupSave tea.Cmd

	// suggestion gates applying a review suggestion — the only action in the
	// diff view that writes a commit to someone's branch.
	suggestion *suggestionPrompt

	// pending is the confirmed action currently in flight, or nil. See
	// pendingAction for why the gap it fills was worth closing.
	pending *pendingAction

	toast Toast

	// lastKeyAt is when the user last pressed a key, which is what decides
	// whether the background poll runs at the active or the idle cadence. It is
	// the only signal available: a ghx sitting in a tmux window nobody is looking
	// at is indistinguishable from one being read, and both are indistinguishable
	// from a terminal whose window is not even visible.
	//
	// This matters because the instances multiply. Six ghx windows left open for
	// days all poll every 30 seconds against one account's GraphQL budget, which
	// is the same load as one window polling every five seconds — measured at
	// ~141 points/minute, or the whole 5,000/hour budget in 35 minutes, with
	// nobody watching any of them.
	lastKeyAt time.Time

	// unfocused is set while the terminal reports that ghx does not have focus:
	// another tmux window or pane has it, or the terminal itself is behind
	// something else. The poll slows to unfocused_poll_interval in that state
	// rather than stopping: tmux reports focus per pane, so a ghx split beside
	// the pane being typed in is unfocused and on screen at the same time.
	//
	// This is the signal lastKeyAt cannot supply. A reviewer who reads a row,
	// switches to another tmux window, and works there for an hour has "pressed
	// a key recently" the whole time, so the idle backoff never engages and the
	// window keeps spending an account-wide budget on frames nobody sees.
	//
	// Terminals that do not report focus never send the message, so unfocused
	// stays false and the cadence is exactly what it was before.
	unfocused bool

	// workspace watches the tmux window: which repositories its panes hold, and
	// whether any of them has just been committed to, pushed, or fetched. Both
	// were startup-only facts before — see workspace.go for why neither should
	// have been.
	workspace *workspace

	// pollGen invalidates in-flight poll timers. Backing off means arming a new
	// timer while an old one is still pending; without this the first keypress
	// after an idle stretch would leave both running.
	pollGen uint64

	// nowFunc is time.Now, replaced in tests so idle behaviour can be asserted
	// without sleeping through the real interval.
	nowFunc func() time.Time

	// copyClipboard, when set, replaces the real clipboard write. Tests set it
	// so asserting that `y` copies does not overwrite the developer's clipboard
	// and does not need pbcopy on PATH.
	copyClipboard clipboardFunc

	// verifyAccounts, when set, checks the configured GitHub accounts after the
	// first frame. It is a func rather than a direct call so this package stays
	// independent of how accounts are configured and verified.
	verifyAccounts func(context.Context) error

	spinnerFrame int

	// clicks remembers the last click so a second one on the same, already
	// selected item can be told from two clicks on different ones.
	clicks mouse.Clicks
}

// mergePrompt holds the two-step merge confirmation.
type mergePrompt struct {
	strategy string // squash | merge | rebase
}

func NewApp(cfg *config.Config, km keys.Keymap, client *gh.Client) *App {
	return NewAppWithRepo(cfg, km, client, nil)
}

// NewAppWithRepo builds the app with detectedRepos leading the PR list, so
// starting ghx inside a checkout — or beside one in the same tmux window —
// opens on those repositories' pull requests.
func NewAppWithRepo(cfg *config.Config, km keys.Keymap, client *gh.Client, detectedRepos []string) *App {
	a := &App{
		cfg:       cfg,
		client:    client,
		km:        km,
		state:     viewPRList,
		composer:  newComposer(),
		palette:   &palette{},
		search:    &search{},
		repoStore: newRepoStore(),
		workspace: newWorkspace(),
		nowFunc:   time.Now,
		saver:     prefs.NewSaver[UIState](prefsApp, 0),
	}
	// Startup counts as activity: opening ghx is the most deliberate keypress
	// there is, and an app that began life idle would poll at the slow cadence
	// while being actively read.
	a.lastKeyAt = a.nowFunc()
	a.list = newPRListModelWithRepo(cfg, client, km, detectedRepos)
	// The list decides how old a sibling instance's answer may be from the
	// cadence this app is actually polling at, which the app owns because both
	// the idle backoff and the budget throttle live here.
	a.list.pollIntervalFunc = a.pollInterval
	// The launch directory is a visit — and only it. Detection also returns the
	// other tmux panes, which is a good reason to *show* those tabs but not
	// evidence that anyone opened them: counting a wide window as eight visits
	// would let whatever happened to be split beside the work outrank the repo
	// ghx was actually started in.
	//
	// The write is held for Init: NewApp runs before the program starts, and
	// a failure reported from here would have nowhere to be shown.
	if len(detectedRepos) > 0 {
		a.startupSave = a.repoStore.record(detectedRepos[0], a.lastKeyAt)
	}
	// Startup detection is the baseline: these repos already have tabs, so the
	// first sweep must not announce them as newly appeared.
	a.workspace.seed(detectedRepos)
	// The state the app starts in is the baseline: only a change is written.
	a.lastUI = a.currentUIState()
	return a
}

// now reads the clock through nowFunc so tests can move it.
func (a *App) now() time.Time {
	if a.nowFunc == nil {
		return time.Now()
	}
	return a.nowFunc()
}

// idle reports whether enough time has passed with no keypress that the poll
// should back off.
func (a *App) idle() bool {
	after := a.cfg.IdleAfterDuration()
	if after <= 0 {
		return false
	}
	return a.now().Sub(a.lastKeyAt) >= after
}

// pollInterval is the cadence for the next background poll: the idle backoff,
// then stretched further when the account's GraphQL budget is running out.
//
// The budget guard exists because the idle backoff only governs *this* window.
// The budget is shared account-wide, so what actually empties it is several ghx
// instances plus whatever else is using the token — and the only signal any of
// them has about the others is how much allowance is left. Slowing down as it
// drains is what keeps the list from going blank entirely; a stale row is worth
// more to a reviewer than no rows.
func (a *App) pollInterval() time.Duration {
	base := a.cfg.PollDuration()
	if a.idle() {
		base = a.cfg.IdlePollDuration()
	}
	if a.unfocused {
		base = max(base, a.cfg.UnfocusedPollDuration())
	}
	return applyBudgetBackoff(base, a.budget())
}

// pollPaused reports that nothing polls on a timer: unfocused, with the
// unfocused cadence configured to "0".
func (a *App) pollPaused() bool {
	return a.unfocused && a.cfg.UnfocusedPollDuration() == 0
}

// budget is the last observed allowance, discarded once its window has closed.
//
// The throttle and the reading feed each other: a low reading stretches the
// interval, and a stretched interval is exactly what stops a fresh reading from
// arriving. At 4% remaining the poll is 100 minutes apart, so an hourly reset
// comes and goes while ghx still believes the budget is nearly gone — throttling
// on a number that expired, and telling the user "API 4%" when the account is
// back to full.
func (a *App) budget() gh.GraphQLBudget {
	b := a.client.GraphQLBudget()
	if b.Known && !b.ResetAt.IsZero() && !b.ResetAt.After(a.now()) {
		return gh.GraphQLBudget{}
	}
	return b
}

// budgetBackoff maps a remaining-budget fraction to a poll multiplier. Coarse
// steps rather than a curve: the point is to be obviously bounded, and a
// reviewer should be able to predict the cadence from the marker in the footer.
var budgetBackoff = []struct {
	below      float64
	multiplier int
}{
	{0.05, 20}, // ~5% left: 30s becomes 10m
	{0.15, 8},
	{0.30, 4},
	{0.50, 2},
}

// applyBudgetBackoff stretches interval according to how little budget is left.
// An unobserved budget reports a full fraction, so this is a no-op until a
// response has actually said otherwise.
func applyBudgetBackoff(interval time.Duration, b gh.GraphQLBudget) time.Duration {
	f := b.Fraction()
	for _, step := range budgetBackoff {
		if f < step.below {
			return interval * time.Duration(step.multiplier)
		}
	}
	return interval
}

// armPoll schedules the next poll at whichever cadence currently applies,
// stamping it with the current generation so a timer armed before a keypress
// does not also fire.
//
// An unfocused window arms at the unfocused cadence, through pollInterval.
// Only unfocused_poll_interval: "0" lets the chain lapse.
func (a *App) armPoll() tea.Cmd {
	if a.pollPaused() {
		return nil
	}
	return prListPollCmd(a.pollInterval(), a.pollGen)
}

// blur slows polling to the unfocused cadence.
//
// The generation is left alone, so the timer already pending still fires. That
// costs nothing: its poll accepts any answer younger than the now much wider
// share window, and this window fetched within the last active interval, so it
// reads its own entry from disk and re-arms at the slow cadence. Retiring the
// timer instead would need a new one armed here, and arming anywhere but Init
// and the settling prListMsg is how two timers of one generation end up
// running — see noteActivity.
//
// With unfocused_poll_interval: "0" the poll stops instead. Bumping the
// generation retires the pending timer — bubbletea cannot cancel a tea.Tick —
// so nothing fires while away.
//
// The workspace sweep deliberately keeps running. It spends filesystem stats
// rather than API budget, and it is the only thing that can notice a push made
// in the pane beside a window nobody is looking at — which is exactly when the
// suspended poll would otherwise leave the queue wrong until the user came
// back. What it triggers is narrow: only tabs pinned to the repository that was
// actually worked in. See workspace.go.
func (a *App) blur() {
	if a.unfocused {
		return
	}
	a.unfocused = true
	if a.pollPaused() {
		a.pollGen++
	}
}

// focus restores the active cadence and refreshes, because the rows on screen
// are up to an unfocused interval old.
//
// Like noteActivity it fetches without arming: the settling prListMsg arms the
// next tick, and arming here too would leave two timers of the same generation
// running, which is the cadence-doubling bug that shape exists to avoid.
func (a *App) focus() tea.Cmd {
	if !a.unfocused {
		return nil
	}
	a.unfocused = false
	var resumeChecks tea.Cmd
	if a.detail != nil && a.detail.checksPollLapsed {
		a.detail.checksPollLapsed = false
		resumeChecks = a.detail.handleChecksPoll()
	}
	// Returning to the window is attention, so the poll resumes at the active
	// cadence rather than inheriting an idle stretch that spanned the absence.
	a.lastKeyAt = a.now()
	a.pollGen++
	return tea.Batch(a.list.handlePollTick(), resumeChecks)
}

// noteActivity records a keypress. When it ends an idle stretch it refreshes
// immediately: the rows on screen are up to an idle interval old, and the
// person who just pressed a key is the one who would be reading them.
//
// It fetches but does not arm a timer. Exactly two places arm one — Init and
// the prListMsg that settles a fetch — which is what keeps a single chain
// alive. Arming here as well would leave two timers of the *current* generation
// running (the one armed here, and the one the woken fetch arms when it lands),
// and the generation check cannot tell them apart because both are current. The
// result was a poll cadence that doubled every time someone came back to a
// parked window, in a change whose whole purpose is polling less.
//
// Bumping the generation is still required: it retires the pending idle timer,
// so the slow tick does not fire alongside the newly active chain.
func (a *App) noteActivity() tea.Cmd {
	// A keypress proves someone is here even if the terminal never reported
	// focus coming back — some do not, and without this the poll would stay
	// suspended for a window actively being used.
	if a.unfocused {
		return a.focus()
	}
	wasIdle := a.idle()
	a.lastKeyAt = a.now()
	if !wasIdle {
		return nil
	}
	a.pollGen++
	// A nil here means a fetch is already in flight; its prListMsg re-arms at
	// the bumped generation, so the chain continues either way.
	return a.list.handlePollTick()
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
		a.armPoll(),
		a.armWorkspace(),
		a.startupSave,
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
	seq := a.toast.Seq()
	m, cmd := a.update(msg)
	// Every change to the persisted state starts from a message, so comparing
	// once here catches all of them without a save call at each mutation.
	if save := a.saveUIState(); save != nil {
		cmd = tea.Batch(cmd, save)
	}
	// Scheduled here rather than at every setToast call: there are a dozen of
	// those, and one that forgot to return the tick would leave its toast on
	// screen until some unrelated event happened to redraw.
	if a.toast.Seq() != seq {
		cmd = tea.Batch(cmd, a.toast.Expiry())
	}
	return m, cmd
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.list.resize(msg.Width, msg.Height)
		if a.detail != nil {
			a.detail.resize(msg.Width, msg.Height)
		}
		a.composer.resize(msg.Width, msg.Height)
		return a, nil

	case tea.FocusMsg:
		return a, a.focus()

	case tea.BlurMsg:
		a.blur()
		return a, nil

	case ToastExpiredMsg:
		a.toast.Expire(msg)
		return a, nil

	case tea.MouseMsg:
		return a, a.handleMouse(msg)

	case tea.KeyMsg:
		// A key between two clicks breaks the pair: click, move away and back
		// by keyboard, click again must not count as a double click.
		a.clicks = mouse.Clicks{}
		// A key is the acknowledgement an error toast waits for. Cleared before
		// the key is handled so that a toast the key itself raises survives.
		a.toast.Ack()
		// Recorded before the key is handled: the handler can quit, and a key that
		// ends an idle stretch should refresh even if it also does something else.
		wake := a.noteActivity()
		if cmd := a.handleKey(msg); cmd != nil {
			return a, tea.Batch(wake, cmd)
		}
		return a, wake

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
		a.setErrorToast(errorStyle.Render("error: ") + msg.err.Error())
		return a, nil

	case prListMsg:
		// Re-arm the poll whenever a fetch settles so the cadence holds without
		// ever running two fetches at once.
		return a, tea.Batch(
			a.list.handlePRListMsg(msg),
			a.armPoll(),
		)

	case workspaceTickMsg:
		return a, a.handleWorkspaceTick(msg)

	case workspaceScanMsg:
		return a, a.handleWorkspaceScan(msg)

	case prListTickMsg:
		// A tick from a superseded generation is a timer armed under the other
		// cadence. Dropping it rather than re-arming is what makes the switch a
		// switch instead of two overlapping schedules.
		if msg.gen != a.pollGen || a.pollPaused() {
			return a, nil
		}
		return a, a.list.handlePollTick()

	case searchSubmitMsg:
		a.list.applyQuery(msg.query)
		return a, nil

	case openDetailMsg:
		return a, a.openSelected()

	case listReturnMsg:
		a.state = viewPRList
		a.detail = nil
		// A PR acted on in the detail view leaves the queue behind it stale.
		// The rows stay on screen while this fetch runs — coming back to a
		// spinner is the flicker this whole path exists to avoid.
		if a.list != nil && a.list.currentDirty() {
			return a, a.list.refreshCurrent()
		}
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
		a.detail.checksFetchedAt = a.now()
		return a, a.detail.handleChecksMsg(msg)

	case checksPollTickMsg:
		if a.detail == nil {
			return a, nil
		}
		// Unfocused, the checks poll slows to the list's cadence: a pending-CI
		// PR left open in a parked window was a request every 15s for as long
		// as CI ran. The 15s tick keeps running — it is local and free — and
		// only fetches once the last answer is a full interval old, so focus
		// coming back is picked up within one tick without arming a second
		// chain. With the unfocused poll disabled it lapses, and focus
		// restarts it.
		if a.pollPaused() {
			a.detail.checksPollLapsed = true
			return a, nil
		}
		if a.unfocused && a.now().Sub(a.detail.checksFetchedAt) < a.pollInterval() {
			return a, a.detail.checksTick()
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
		a.endPending()
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
		a.endPending()
		if msg.failure != nil {
			a.showMergeFailure(msg.failure)
			return a, nil
		}
		if msg.err != nil {
			return a, errCmd(msg.err)
		}
		a.setToast(msg.label)
		// An action taken from the list changes what the row should say, so pull
		// the source again rather than leaving stale state on screen.
		if a.state == viewPRList && a.list != nil {
			return a, a.list.refreshCurrent()
		}
		if a.detail != nil && msg.branchMoved {
			// The commits behind every diff line are new; the metadata-only
			// refresh below would leave the old diff on screen.
			if a.list != nil {
				a.list.markAllDirty()
			}
			return a, a.detail.reload()
		}
		if a.detail != nil {
			// ready/draft, close/reopen, labels — all PR metadata. A checkout
			// changes nothing on GitHub at all, but refreshing the metadata is
			// cheap enough not to be worth special-casing.
			return a, a.detail.refreshDetail()
		}
		return a, nil

	case bulkActionDoneMsg:
		a.endPending()
		if a.list != nil {
			for _, key := range msg.completed {
				delete(a.list.selected, key)
			}
		}
		if msg.failure != nil {
			a.showMergeFailure(msg.failure)
		} else if msg.err == nil {
			a.setToast(msg.label)
		} else if len(msg.completed) > 0 {
			a.setErrorToast(fmt.Sprintf("%s; some failed: %v", msg.label, msg.err))
		} else {
			return a, errCmd(msg.err)
		}
		if a.state == viewPRList && a.list != nil && len(msg.completed) > 0 {
			return a, a.list.invalidateCachesAndRefresh()
		}
		if a.detail != nil && len(msg.completed) > 0 {
			// The list is showing the same PR one screen back. Flagging it here
			// is what makes returning to the queue reflect the merge; without
			// it the row stayed until something else happened to refetch.
			if a.list != nil {
				a.list.markAllDirty()
			}
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
			target, _ := a.currentTarget()
			a.showMergeFailure(&mergeFailure{kind: confirmMerge,
				items: []mergeFailureItem{newMergeFailureItem(confirmMerge, target, msg.err)}})
			return a, nil
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
		if msg.action != "" {
			return a, a.runByAction(msg.action)
		}
		return a, a.runPalette(msg.line)

	case quitMsg:
		return a, a.quit()

	case prefs.SaveTick:
		var cmd tea.Cmd
		a.saver, cmd = a.saver.Update(msg)
		return a, cmd

	case prefs.Saved:
		if msg.Final {
			return a, tea.Quit // the flushed state is on disk (or failed to be)
		}
		if msg.Err != nil {
			a.setToast("could not save the UI state: " + msg.Err.Error())
		}
		return a, nil
	}
	return a, nil
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
	// An action in flight animates too: its marker is a spinner, and the tick
	// that advances it only runs while something reports being busy.
	if a.pending != nil {
		return true
	}
	if a.list != nil && a.list.loading() {
		return true
	}
	if a.detail != nil && a.detail.loading() {
		return true
	}
	return false
}

// showMergeFailure opens the failure box and leaves a one-line summary in the
// footer for after it is dismissed.
func (a *App) showMergeFailure(f *mergeFailure) {
	a.mergeFailure = f
	a.setToast(f.toast())
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
	// Reopen on the tab the last PR was read in: a reviewer working through a
	// queue in the Diff tab should not have to press 3 on every PR.
	var tab tea.Cmd
	if t, ok := a.preferredDetailTab(); ok {
		tab = a.detail.setTab(t)
	}
	return tea.Batch(a.detail.load(), tab)
}

// --- actions ---

// submitReview approves or requests changes on the current PR.
// --- shared helpers ---

func joinVertical(parts ...string) string {
	return strings.Join(parts, "\n")
}

// truncateFooter clips a one-line footer to the terminal width, cell-accurately.
func truncateFooter(s string, w int) string {
	s = flattenLine(s)
	if w <= 0 {
		return s
	}
	out, _ := truncateExact(s, w)
	return out
}

// flattenLine folds a message onto a single line, collapsing runs of
// whitespace.
//
// The footer is the last line View joins, and truncation cannot save it:
// ansi.TruncateWc counts a newline as zero cells, so an embedded one survives
// any width and every following line is drawn below the frame — the body gets
// shoved off the top of the terminal. A gh failure carrying its whole argv
// (a multi-line GraphQL query) did exactly that to the PR list.
func flattenLine(s string) string {
	if !strings.ContainsAny(s, "\n\r\t\v\f") {
		return s
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t', '\v', '\f', ' ':
			if !space {
				b.WriteByte(' ')
				space = true
			}
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return strings.TrimSpace(b.String())
}

// ctx returns a background context; callers attach their own timeouts.
func ctx() context.Context { return context.Background() }

var _ = lipgloss.Width
