package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// Key handling for the detail view, split per tab. Each handler reports whether
// it knows the action; the binding table decides which handler is asked.

// runTab runs a binding of the active tab's context. Everything a tab does is
// named by an action (keymap.go); the tab handlers below switch on it.
func (d *prDetailModel) runTab(b keys.Binding, _ tea.KeyMsg) (tea.Cmd, bool) {
	switch b.Ctx {
	case ctxOverview:
		return d.updateOverview(b.Action)
	case ctxFiles:
		return d.updateFiles(b.Action)
	case ctxDiff:
		return d.updateDiff(b.Action)
	case ctxComments:
		return d.updateComments(b.Action)
	case ctxCommits:
		return d.updateCommits(b.Action)
	case ctxChecks:
		return d.updateChecks(b.Action)
	case ctxLogs:
		return d.updateLogs(b.Action)
	case ctxDetail:
		return d.runDetail(b)
	}
	return nil, false
}

// runDetail runs the bindings every tab shares that need only this model.
// The ones that open App-level prompts (merge, PR comment) are App's.
func (d *prDetailModel) runDetail(b keys.Binding) (tea.Cmd, bool) {
	switch b.Action {
	case "back":
		return func() tea.Msg { return listReturnMsg{} }, true
	case "prevtab":
		d.cycleTab(-1)
		return nil, true
	case "nexttab":
		d.cycleTab(1)
		return nil, true
	}
	return nil, false
}

// update reports whether the active tab itself consumes msg — not the
// detail-wide or PR bindings behind it — and runs it if so.
func (d *prDetailModel) update(msg tea.KeyMsg) (tea.Cmd, bool) {
	stack := d.stack()
	tab := stack[:slices.Index(stack, ctxDetail)]
	b, ok := d.km.Or(defaultKeymap).Resolve(tab, msg, d.keyOpts())
	if !ok {
		return nil, false
	}
	return d.runTab(b, msg)
}

// setTab switches tabs and starts the checks poll when entering that tab.
func (d *prDetailModel) setTab(t detailTabKind) tea.Cmd {
	d.activeTab = t
	if t == tabChecks && d.checks.hasPending() {
		return d.checksTick()
	}
	return nil
}

func (d *prDetailModel) cycleTab(delta int) {
	n := len(d.tabs)
	cur := 0
	for i, t := range d.tabs {
		if t == d.activeTab {
			cur = i
			break
		}
	}
	d.setTab(d.tabs[((cur+delta)%n+n)%n])
}

func (d *prDetailModel) updateOverview(action string) (tea.Cmd, bool) {
	switch action {
	case "down":
		d.overviewOff++
		return nil, true
	case "up":
		d.overviewOff = max(d.overviewOff-1, 0)
		return nil, true
	case "pagedown":
		d.overviewOff += d.contentHeight()
		return nil, true
	case "pageup":
		d.overviewOff = max(d.overviewOff-d.contentHeight(), 0)
		return nil, true
	case "top":
		d.overviewOff = 0
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateFiles(action string) (tea.Cmd, bool) {
	n := 0
	if d.detail != nil {
		n = len(d.detail.Files)
	}
	switch action {
	case "down":
		if n > 0 {
			d.filesCursor = clamp(d.filesCursor+1, 0, n-1)
		}
		return nil, true
	case "up":
		if n > 0 {
			d.filesCursor = clamp(d.filesCursor-1, 0, n-1)
		}
		return nil, true
	case "top":
		d.filesCursor = 0
		return nil, true
	case "bottom":
		d.filesCursor = max(n-1, 0)
		return nil, true
	case "openfile":
		// Jump into the diff at this file so Files acts as a table of contents.
		if d.detail != nil && d.filesCursor < len(d.detail.Files) {
			path := d.detail.Files[d.filesCursor].Path
			d.setTab(tabDiff)
			d.diff.jumpTo(path, 0)
		}
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateDiff(action string) (tea.Cmd, bool) {
	switch action {
	case "down":
		d.diff.moveDown(1)
		return nil, true
	case "up":
		d.diff.moveDown(-1)
		return nil, true
	case "left":
		// On a file header h folds that file. The header has no columns to
		// switch between — focusSide already refuses there — so this costs
		// nothing the paired layout was using.
		if path, ok := d.diff.foldPathForCursor(); ok {
			// An already-folded file stays folded. h has no other meaning in
			// the diff's stack, so there is nothing for the key to fall to.
			d.diff.setFold(path, true)
			return nil, true
		}
		// In the paired layout the columns are two halves of one screen row, so
		// h/l switch between them.
		if d.diff.sideBySide {
			d.diff.focusSide(sideLeft)
		}
		return nil, true
	case "right":
		if path, ok := d.diff.foldPathForCursor(); ok {
			d.diff.setFold(path, false)
			return nil, true
		}
		if d.diff.sideBySide {
			d.diff.focusSide(sideRight)
		}
		return nil, true
	case "pagedown":
		d.diff.page(1, d.contentHeight())
		return nil, true
	case "pageup":
		d.diff.page(-1, d.contentHeight())
		return nil, true
	case "top":
		d.diff.toGutter(true)
		return nil, true
	case "bottom":
		d.diff.toGutter(false)
		return nil, true
	case "nexthunk", "prevhunk":
		// Coarse navigation: one hunk per press rather than one line. Shifted so
		// j/k keep their meaning — reading a hunk and finding the next one are
		// different jobs and both need a key.
		//
		// Ignored while a visual range is open: a jump crosses a hunk boundary by
		// definition, and a range that does is not one GitHub accepts, so the
		// selection would silently collapse to a single line. The key is still
		// consumed so nothing else acts on it mid-selection.
		if d.diff.visual {
			return nil, true
		}
		delta := 1
		if action == "prevhunk" {
			delta = -1
		}
		d.diff.jumpHunk(delta)
		return nil, true
	case "prevfile", "nextfile":
		// File-level jumps, for the same reason and with the same restriction.
		// H/L pairs with J/K — shifted for coarse movement, one letter apart for
		// hunk versus file. { and } stay as aliases for the paragraph-motion
		// habit they come from.
		if d.diff.visual {
			return nil, true
		}
		delta := 1
		if action == "prevfile" {
			delta = -1
		}
		d.diff.jumpFile(delta)
		return nil, true
	case "fold":
		d.diff.toggleFold()
		return nil, true
	case "layout":
		// Toggle unified / side-by-side. The cursor keeps pointing at the same
		// diff row, so the comment target survives the switch; the focused column
		// is derived from that row rather than left at whatever it was.
		d.diff.sideBySide = !d.diff.sideBySide
		d.diff.syncSideFocus()
		return nil, true
	case "visual":
		// Toggle visual range selection for a multi-line comment.
		if d.diff.visual {
			d.diff.visual = false
		} else {
			d.diff.visual = true
			d.diff.visualStart = d.diff.cursor
			// Anchor the range to the column the cursor is really in. Starting a
			// selection with the focus on the other side would make every extension
			// mix LEFT and RIGHT lines, which is not a range GitHub accepts.
			d.diff.syncSideFocus()
		}
		return nil, true
	case "apply":
		// Apply the suggestion under the cursor. A is free here: the list's A
		// (select all visible) has no meaning with a PR open.
		return d.requestApplySuggestion(), true
	case "comment":
		return d.composeInline(), true
	case "thread":
		// On a thread with replies, enter reveals them; a single-comment thread
		// has nothing to expand, so enter goes straight to replying.
		if d.diff.threadHasReplies() {
			d.diff.toggleThread()
			return nil, true
		}
		if id, ok := d.diff.threadUnderCursor(); ok {
			return d.composeReply(id), true
		}
		return nil, true
	}
	return nil, false
}

// composeInline asks app.go to open the composer for the cursor's line.
// composeInline opens the composer for whatever the cursor is on: a diff line
// starts a new comment, a thread row replies to it. Refusing on a thread row
// would be a dead end — the obvious thing to do there is answer the comment.
func (d *prDetailModel) composeInline() tea.Cmd {
	if id, ok := d.diff.threadUnderCursor(); ok {
		return d.composeReply(id)
	}
	path, side, line, startLine, ok := d.diff.commentTarget()
	if !ok {
		return errCmd(fmt.Errorf("put the cursor on a diff line to comment"))
	}
	// Leaving visual mode on submit avoids a stale range on the next comment.
	d.diff.visual = false
	t := composerTarget{path: path, line: line, side: side, startLine: startLine}
	return func() tea.Msg { return openComposerMsg{target: t} }
}

func (d *prDetailModel) composeReply(threadID string) tea.Cmd {
	for _, t := range d.comments.threads {
		if threadIdentity(t) != threadID || len(t.Comments) == 0 {
			continue
		}
		// The reply goes to the parent comment, and via REST — which wants the
		// numeric database id. Passing the GraphQL node id here is rejected with
		// "Parent comment not found".
		first := t.Comments[0]
		if first.DatabaseID == 0 {
			return errCmd(fmt.Errorf(
				"thread %s has no numeric comment id to reply to", t.Path))
		}
		parent := strconv.FormatInt(first.DatabaseID, 10)
		return func() tea.Msg {
			return openComposerMsg{target: composerTarget{
				path: t.Path, line: threadLine(t), side: t.DiffSide,
				replyTo: parent,
			}}
		}
	}
	return errCmd(fmt.Errorf("could not find the thread to reply to"))
}

// resolveThread persists a thread's resolved state and reloads the detail view
// to reconcile. The local list was already flipped optimistically by the caller.
func (d *prDetailModel) resolveThread(thread pr.ReviewThread, resolve bool) tea.Cmd {
	threadID := thread.ID
	client := d.client
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var err error
		if resolve {
			err = client.ResolveThread(c, threadID)
		} else {
			err = client.UnresolveThread(c, threadID)
		}
		if err != nil {
			// Revert the optimistic flip so the marker reflects reality. The
			// user also needs to see why the resolve failed.
			for i := range d.comments.threads {
				if d.comments.threads[i].ID == threadID {
					d.comments.threads[i].IsResolved = !resolve
					break
				}
			}
			return errMsg{err: fmt.Errorf("resolve thread: %w", err)}
		}
		// Reload so the unresolved-conversation count in the list stays accurate.
		return threadResolvedMsg{}
	}
}

func (d *prDetailModel) updateComments(action string) (tea.Cmd, bool) {
	switch action {
	case "down":
		d.comments.moveCursor(1)
		return nil, true
	case "up":
		d.comments.moveCursor(-1)
		return nil, true
	case "expand":
		d.comments.toggleExpand()
		return nil, true
	case "resolvedfilter":
		d.comments.toggleResolvedFilter()
		return nil, true
	case "resolve", "jumpdiff":
		// The thread-only actions. A cursor on a PR-level comment has no thread
		// to act on, and staying silent would read as the key being broken —
		// which is worse than the key not applying, because it is the same
		// nothing a bug produces.
		if _, onConv := d.comments.selectedConversation(); onConv {
			return errCmd(fmt.Errorf(
				"that is a PR comment, not a review thread — it has no line to " +
					"jump to and nothing to resolve")), true
		}
		if action == "jumpdiff" {
			// Jump to this thread's anchor in the diff.
			if t, ok := d.comments.selected(); ok {
				d.setTab(tabDiff)
				d.diff.jumpTo(t.Path, threadLine(t))
			}
			return nil, true
		}
		// Resolve or unresolve the selected thread. Optimistic on the local
		// list so the marker flips immediately; the reload reconciles on failure.
		thread, resolve, ok := d.comments.toggleThreadResolved()
		if !ok {
			// A thread recovered over REST has no GraphQL node to resolve, and
			// resolveReviewThread is GraphQL-only. Say why rather than letting the
			// key look broken.
			if t, found := d.comments.selected(); found && !t.ResolutionKnown {
				return errCmd(fmt.Errorf(
					"resolving a thread needs GraphQL access, which this token or " +
						"rate limit did not allow — the thread list came from REST")), true
			}
			return nil, true
		}
		return d.resolveThread(thread, resolve), true
	case "reply":
		if t, ok := d.comments.selected(); ok && len(t.Comments) > 0 {
			return d.composeReply(t.ID), true
		}
		// On a PR-level comment, c opens the PR-level composer. Replying to the
		// PR is what "reply" means where the cursor is, and it is the only reply
		// GitHub has for these — an issue comment carries no thread to reply into.
		if _, onConv := d.comments.selectedConversation(); onConv {
			return func() tea.Msg {
				return openComposerMsg{target: composerTarget{issue: true}}
			}, true
		}
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateCommits(action string) (tea.Cmd, bool) {
	switch action {
	case "down":
		d.commitsOff++
		return nil, true
	case "up":
		d.commitsOff = max(d.commitsOff-1, 0)
		return nil, true
	}
	return nil, false
}

// updateLogs runs the log pane's bindings; its context replaces the checks
// list's while a log is open, so the list's keys never act behind it.
func (d *prDetailModel) updateLogs(action string) (tea.Cmd, bool) {
	switch action {
	case "down":
		d.checks.scrollLogs(1)
		return nil, true
	case "up":
		d.checks.scrollLogs(-1)
		return nil, true
	case "pagedown":
		d.checks.scrollLogs(d.contentHeight())
		return nil, true
	case "pageup":
		d.checks.scrollLogs(-d.contentHeight())
		return nil, true
	case "closelogs":
		d.checks.closeLogs()
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateChecks(action string) (tea.Cmd, bool) {
	switch action {
	case "down":
		d.checks.moveCursor(1)
		return nil, true
	case "up":
		d.checks.moveCursor(-1)
		return nil, true
	case "logs":
		return d.fetchSelectedLogs(), true
	}
	return nil, false
}

// fetchSelectedLogs resolves the run/job from the check link and fetches logs.
func (d *prDetailModel) fetchSelectedLogs() tea.Cmd {
	ck, ok := d.checks.selected()
	if !ok {
		return nil
	}
	runID, jobID, ok := gh.ParseRunURL(ck.Link)
	if !ok {
		return errCmd(fmt.Errorf("check %q has no workflow run to fetch logs from", ck.Name))
	}
	d.checks.logBusy = true
	d.checks.logCheck = ck.Name
	client := d.client
	name := ck.Name
	return func() tea.Msg {
		// Log fetches are slow; give them well past the normal timeout.
		c, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if jobID == "" {
			// The check link often omits the job id, so match the job by name.
			jobs, err := client.RunJobs(c, runID)
			if err != nil {
				return runLogsMsg{checkName: name, err: fmt.Errorf(
					"list jobs for run %s: %w", runID, err)}
			}
			for _, j := range jobs {
				if strings.Contains(name, j.Name) || strings.Contains(j.Name, name) {
					jobID = fmt.Sprint(j.DatabaseID)
					break
				}
			}
		}
		logs, err := client.RunLogs(c, runID, jobID)
		if err != nil {
			return runLogsMsg{checkName: name, err: err}
		}
		// An empty body is not a successful fetch as far as the reviewer is
		// concerned: GitHub drops logs for expired runs and returns 200 with
		// nothing, and "No log output" alone gives no way to tell that apart
		// from a bug here.
		if strings.TrimSpace(logs) == "" {
			return runLogsMsg{checkName: name, err: fmt.Errorf(
				"run %s returned no logs — they may have expired or the token lacks actions:read", runID)}
		}
		return runLogsMsg{checkName: name, logs: logs}
	}
}
