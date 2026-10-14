package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
)

// Key dispatch. Every key goes through the binding table (keymap.go): the
// modal on top resolves in its own context, otherwise the view's stack does,
// and the handler switches on the action name — never on the key — so a
// rebound key, the footer, `?` and the palette all agree with what runs.

// defaultKeymap backs a model built without one (tests that construct a view
// directly); the zero Keymap would bind nothing.
var defaultKeymap = DefaultKeymap()

func (a *App) keymap() keys.Keymap { return a.km.Or(defaultKeymap) }

// activeStack is the view's context stack, innermost first. Modals are not
// part of it: each owns the keyboard alone (handleKey).
func (a *App) activeStack() []keys.Ctx {
	if a.state == viewPRDetail && a.detail != nil {
		return a.detail.stack()
	}
	return stackList
}

// keyOpts is the view's state as the table needs it: what to offer in the
// footer and which label a two-meaning key shows.
func (a *App) keyOpts() keys.Opts {
	if a.state == viewPRDetail && a.detail != nil {
		return a.detail.keyOpts()
	}
	return keys.Opts{}
}

// resolveIn resolves msg in a modal's own stack. Text inputs skip the CJK
// rewrite so a typed jamo stays a jamo.
func (a *App) resolveIn(stack []keys.Ctx, msg tea.KeyMsg, text bool) (string, bool) {
	var b keys.Binding
	var ok bool
	if text {
		b, ok = a.keymap().ResolveRaw(stack, msg, keys.Opts{})
	} else {
		b, ok = a.keymap().Resolve(stack, msg, keys.Opts{})
	}
	return b.Action, ok
}

// handleKey enforces the modal order: failure box → composer → prompts →
// pickers → search → palette → help → the view. Whoever is on top owns the
// keyboard.
func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	// ctrl+c quits from anywhere, ahead of the composer and every prompt, and
	// is not rebindable: it is the exit that does not depend on any table. A
	// half-written review comment is discarded, which is what ctrl+c means —
	// nothing has been posted.
	if msg.Type == tea.KeyCtrlC {
		return a.quit()
	}

	// The failure box arrives on its own, possibly over a prompt opened while
	// the merge ran; it is drawn on top, so it takes the keys first.
	if a.mergeFailure != nil {
		return a.handleMergeFailureKey(msg)
	}
	if cmd, handled := a.composerKey(msg); handled {
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
	if a.repos != nil {
		return a.handleRepoPickerKey(msg)
	}
	if a.mergePrompt != nil {
		return a.handleMergePromptKey(msg)
	}
	if a.search.active {
		action, _ := a.resolveIn(stackSearch, msg, true)
		cmd, _ := a.search.update(action, msg)
		return cmd
	}
	if a.palette.active {
		action, _ := a.resolveIn(stackPalette, msg, true)
		cmd, _ := a.palette.update(action, msg)
		return cmd
	}
	if a.helpOpen {
		// Help swallows everything so no key does double duty behind it.
		action, _ := a.resolveIn(stackHelp, msg, false)
		return a.runHelpKey(action)
	}

	b, ok := a.keymap().Resolve(a.activeStack(), msg, a.keyOpts())
	if !ok {
		return nil
	}
	cmd, _ := a.runAction(b, msg)
	return cmd
}

// runAction runs a view binding. handled is false only for an action the
// dispatcher does not know, which the reachability test turns into a failure:
// a binding in the table with no handler is a key the footer offers that does
// nothing.
func (a *App) runAction(b keys.Binding, msg tea.KeyMsg) (tea.Cmd, bool) {
	switch b.Ctx {
	case ctxGlobal:
		switch b.Action {
		case "quit":
			return a.quit(), true
		case "help":
			a.openHelp()
			return nil, true
		case "palette":
			a.openPalette()
			return nil, true
		}
	case ctxPR:
		return a.prAction(b.Action)
	case ctxList:
		return a.listAction(b, msg)
	case ctxDetail:
		return a.detailAction(b, msg)
	default:
		if a.detail != nil {
			return a.detail.runTab(b, msg)
		}
		return nil, true
	}
	return nil, false
}

// listAction runs the list's bindings. The pickers live on App; the rest is
// the list model's own state.
func (a *App) listAction(b keys.Binding, msg tea.KeyMsg) (tea.Cmd, bool) {
	switch b.Action {
	case "search":
		a.search.open(a.list.query)
		return nil, true
	case "status":
		return a.openStatusFilter(), true
	case "repo":
		return a.openRepoPicker(), true
	case "open":
		return func() tea.Msg { return openDetailMsg{} }, true
	}
	return a.list.runAction(b, msg)
}

// detailAction runs what every detail tab shares.
func (a *App) detailAction(b keys.Binding, msg tea.KeyMsg) (tea.Cmd, bool) {
	switch b.Action {
	case "detailtab":
		if i := b.KeyIndex(msg); i >= 0 && i < len(a.detail.tabs) {
			return a.detail.setTab(a.detail.tabs[i]), true
		}
		return nil, true
	case "prcomment":
		return func() tea.Msg {
			return openComposerMsg{target: composerTarget{issue: true}}
		}, true
	case "merge":
		return a.startMerge(), true
	case "refresh":
		return a.detail.reload(), true
	}
	return a.detail.runDetail(b)
}

// prAction runs the actions that work on whichever PR is in focus — the open
// one in the detail view, or the selected rows in the list. Acting from the
// list is the point: triaging a queue should not require opening every PR.
//
// The irreversible-ish ones (approve, close, ready, merge) go through a
// confirmation, because in the list the cursor moves under the same fingers
// that press them.
func (a *App) prAction(action string) (tea.Cmd, bool) {
	switch action {
	case "approve", "close", "merge", "labels", "ready", "browser", "copy":
	case "request":
		t, ok := a.currentTarget()
		if !ok {
			return errCmd(fmt.Errorf("no pull request selected")), true
		}
		// Requesting changes without saying why isn't useful, so this opens the
		// composer and submits the review with its body.
		return func() tea.Msg {
			return openComposerMsg{target: composerTarget{
				issue: true, review: "request-changes",
				prNumber: t.number, repo: t.repo, credentialRepo: t.credentialRepo,
			}}
		}, true
	default:
		return nil, false
	}

	targets, ok := a.actionTargets()
	if !ok {
		return errCmd(fmt.Errorf("no pull request selected")), true
	}
	switch action {
	case "approve":
		return a.askConfirmTargets(confirmApprove, targets), true
	case "close":
		// Visible marks, not every mark: an off-screen selection resolved to the
		// focused row above, and that row belongs on the single-PR path.
		if a.state == viewPRList && a.list != nil && a.list.visibleSelectedCount() > 0 {
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
	case "merge":
		return a.askConfirmTargets(confirmMerge, targets), true
	case "labels":
		return a.openLabelPickerTargets(targets), true
	case "ready":
		return a.askConfirmTargets(confirmReady, targets), true
	case "browser":
		return a.openTargetsInBrowser(targets), true
	default: // copy
		return a.copyTargets(targets), true
	}
}

// runByAction runs the binding for action in the view's stack, as if its key
// had been pressed. The palette uses it, so a palette pick and the key share
// one code path.
func (a *App) runByAction(action string) tea.Cmd {
	stack := a.activeStack()
	bs := a.keymap().Bindings()
	for _, ctx := range stack {
		for _, b := range bs {
			if b.Ctx == ctx && b.Action == action && len(b.Keys) > 0 {
				cmd, _ := a.runAction(b, keyMsgFor(b.Keys[0]))
				return cmd
			}
		}
	}
	return errCmd(fmt.Errorf("%s is not available here", action))
}

// keyMsgFor rebuilds the message for a key string, so actions that read the
// pressed key (the 1-9 jumps) behave the same from the palette.
func keyMsgFor(key string) tea.KeyMsg {
	if r := []rune(key); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}
	}
	return tea.KeyMsg{}
}

// --- help overlay ---------------------------------------------------------

func (a *App) openHelp() {
	a.helpOpen = true
	a.helpOff = 0
	// The overlay documents the view behind it; remember which, since the
	// overlay's own stack is just "help".
	a.helpStack = a.activeStack()
	a.helpOpts = a.keyOpts()
}

func (a *App) runHelpKey(action string) tea.Cmd {
	switch action {
	case "close":
		a.helpOpen = false
	case "down":
		a.helpOff = a.clampHelp(a.helpOff + 1)
	case "up":
		a.helpOff = max(a.helpOff-1, 0)
	}
	return nil
}

// --- quitting ---------------------------------------------------------------

// quit writes the UI state and quits once it is on disk (prefs.Saved{Final}).
// A second quit while the write is in flight quits at once: the write is
// best-effort, and a stuck disk must not trap the user.
func (a *App) quit() tea.Cmd {
	if a.quitting {
		return tea.Quit
	}
	a.quitting = true
	var cmd tea.Cmd
	a.saver, cmd = a.saver.Flush()
	return cmd
}

// composerKey routes a key to the composer when it is open. It is a text
// field: the jamo of a Korean comment is input, not a shortcut.
func (a *App) composerKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if a.composer == nil || !a.composer.active {
		return nil, false
	}
	action, _ := a.resolveIn(stackComposer, msg, true)
	return a.composer.update(action, msg)
}

// openPalette opens the `:` line over the view's bindings.
func (a *App) openPalette() {
	a.palette.open()
	a.palette.entries = a.keymap().Entries(a.activeStack(), a.keyOpts())
}
