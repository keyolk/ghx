package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// Key handling for the detail view, split per tab. Each handler reports whether
// it consumed the key so app.go can fall through to its own bindings.

// update routes keys to the active tab. Returns a cmd plus whether the key was
// consumed, so app.go can fall through for keys the tab ignores.
func (d *prDetailModel) update(msg tea.KeyMsg) (tea.Cmd, bool) {
	key, _ := translateNav(msg)

	// Tab switching and leaving the view apply everywhere.
	switch key {
	case "esc":
		// In the checks tab, esc first closes the log pane rather than the view.
		if d.activeTab == tabChecks && d.checks.showingLogs() {
			d.checks.closeLogs()
			return nil, true
		}
		return func() tea.Msg { return listReturnMsg{} }, true
	case "1", "2", "3", "4", "5", "6":
		idx := int(key[0] - '1')
		if idx < len(d.tabs) {
			d.setTab(d.tabs[idx])
		}
		return nil, true
	}

	switch d.activeTab {
	case tabOverview:
		return d.updateOverview(key)
	case tabFiles:
		return d.updateFiles(key)
	case tabDiff:
		return d.updateDiff(key)
	case tabComments:
		return d.updateComments(key)
	case tabCommits:
		return d.updateCommits(key)
	case tabChecks:
		return d.updateChecks(key)
	}
	return nil, false
}

// setTab switches tabs and starts the checks poll when entering that tab.
func (d *prDetailModel) setTab(t detailTabKind) tea.Cmd {
	d.activeTab = t
	if t == tabChecks && d.checks.hasPending() {
		return tea.Tick(15*time.Second, func(tm time.Time) tea.Msg {
			return checksPollTickMsg(tm)
		})
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

func (d *prDetailModel) updateOverview(key string) (tea.Cmd, bool) {
	switch key {
	case "down":
		d.overviewOff++
		return nil, true
	case "up":
		d.overviewOff = max(d.overviewOff-1, 0)
		return nil, true
	case "pgdown":
		d.overviewOff += d.contentHeight()
		return nil, true
	case "pgup":
		d.overviewOff = max(d.overviewOff-d.contentHeight(), 0)
		return nil, true
	case "home":
		d.overviewOff = 0
		return nil, true
	case "left":
		d.cycleTab(-1)
		return nil, true
	case "right":
		d.cycleTab(1)
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateFiles(key string) (tea.Cmd, bool) {
	n := 0
	if d.detail != nil {
		n = len(d.detail.Files)
	}
	switch key {
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
	case "home":
		d.filesCursor = 0
		return nil, true
	case "end":
		d.filesCursor = max(n-1, 0)
		return nil, true
	case "enter", "right":
		// Jump into the diff at this file so Files acts as a table of contents.
		if d.detail != nil && d.filesCursor < len(d.detail.Files) {
			path := d.detail.Files[d.filesCursor].Path
			d.setTab(tabDiff)
			d.diff.jumpTo(path, 0)
		}
		return nil, true
	case "left":
		d.cycleTab(-1)
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateDiff(key string) (tea.Cmd, bool) {
	switch key {
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
			if d.diff.setFold(path, true) {
				return nil, true
			}
			// Already folded: fall through so h keeps cycling tabs, rather than
			// swallowing a keypress that did nothing.
			return nil, false
		}
		// In the paired layout the columns are two halves of one screen row, so
		// h/l switch between them. Falling through when there is nothing to
		// switch to keeps h available for cycling tabs.
		if d.diff.sideBySide && d.diff.focusSide(sideLeft) {
			return nil, true
		}
		return nil, false
	case "right":
		if path, ok := d.diff.foldPathForCursor(); ok {
			if d.diff.setFold(path, false) {
				return nil, true
			}
			return nil, false
		}
		if d.diff.sideBySide && d.diff.focusSide(sideRight) {
			return nil, true
		}
		return nil, false
	case "pgdown":
		d.diff.page(1, d.contentHeight())
		return nil, true
	case "pgup":
		d.diff.page(-1, d.contentHeight())
		return nil, true
	case "home":
		d.diff.toGutter(true)
		return nil, true
	case "end":
		d.diff.toGutter(false)
		return nil, true
	case "J", "K":
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
		if key == "K" {
			delta = -1
		}
		d.diff.jumpHunk(delta)
		return nil, true
	case "H", "L", "{", "}":
		// File-level jumps, for the same reason and with the same restriction.
		// H/L pairs with J/K — shifted for coarse movement, one letter apart for
		// hunk versus file. { and } stay as aliases for the paragraph-motion
		// habit they come from.
		if d.diff.visual {
			return nil, true
		}
		delta := 1
		if key == "H" || key == "{" {
			delta = -1
		}
		d.diff.jumpFile(delta)
		return nil, true
	case "o":
		d.diff.toggleFold()
		return nil, true
	case "s":
		// Toggle unified / side-by-side. The cursor keeps pointing at the same
		// diff row, so the comment target survives the switch; the focused column
		// is derived from that row rather than left at whatever it was.
		d.diff.sideBySide = !d.diff.sideBySide
		d.diff.syncSideFocus()
		return nil, true
	case "v":
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
	case "A":
		// Apply the suggestion under the cursor. A is free here: the list's A
		// (select all visible) has no meaning with a PR open.
		return d.requestApplySuggestion(), true
	case "c":
		return d.composeInline(), true
	case "enter":
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
		if t.ID != threadID || len(t.Comments) == 0 {
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

func (d *prDetailModel) updateComments(key string) (tea.Cmd, bool) {
	switch key {
	case "down":
		d.comments.moveCursor(1)
		return nil, true
	case "up":
		d.comments.moveCursor(-1)
		return nil, true
	case "enter":
		d.comments.toggleExpand()
		return nil, true
	case "t":
		d.comments.toggleResolvedFilter()
		return nil, true
	case "X":
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
	case "d":
		// Jump to this thread's anchor in the diff.
		if t, ok := d.comments.selected(); ok {
			d.setTab(tabDiff)
			d.diff.jumpTo(t.Path, threadLine(t))
		}
		return nil, true
	case "c":
		if t, ok := d.comments.selected(); ok && len(t.Comments) > 0 {
			return d.composeReply(t.ID), true
		}
		return nil, true
	case "left":
		d.cycleTab(-1)
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateCommits(key string) (tea.Cmd, bool) {
	switch key {
	case "down":
		d.commitsOff++
		return nil, true
	case "up":
		d.commitsOff = max(d.commitsOff-1, 0)
		return nil, true
	case "left":
		d.cycleTab(-1)
		return nil, true
	case "right":
		d.cycleTab(1)
		return nil, true
	}
	return nil, false
}

func (d *prDetailModel) updateChecks(key string) (tea.Cmd, bool) {
	if d.checks.showingLogs() {
		switch key {
		case "down":
			d.checks.scrollLogs(1)
			return nil, true
		case "up":
			d.checks.scrollLogs(-1)
			return nil, true
		case "pgdown":
			d.checks.scrollLogs(d.contentHeight())
			return nil, true
		case "pgup":
			d.checks.scrollLogs(-d.contentHeight())
			return nil, true
		}
		return nil, false
	}
	switch key {
	case "down":
		d.checks.moveCursor(1)
		return nil, true
	case "up":
		d.checks.moveCursor(-1)
		return nil, true
	case "enter":
		return d.fetchSelectedLogs(), true
	case "left":
		d.cycleTab(-1)
		return nil, true
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
