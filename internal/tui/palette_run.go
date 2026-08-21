package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Palette command dispatch. Separated from app.go because the command set grows
// independently of the update loop.

// runPalette dispatches a submitted command line.
func (a *App) runPalette(line string) tea.Cmd {
	head, arg := parsePalette(line)
	switch head {
	case "quit", "q":
		return tea.Quit
	case "help", "?":
		a.helpOpen = true
		return nil
	case "refresh":
		if a.state == viewPRDetail && a.detail != nil {
			return a.detail.reload()
		}
		return a.list.refreshCurrent()
	case "approve":
		if a.detail == nil {
			return a.paletteNeedsPR()
		}
		return a.submitReview("approve", arg)
	case "request-changes":
		if a.detail == nil {
			return a.paletteNeedsPR()
		}
		if arg != "" {
			return a.submitReview("request-changes", arg)
		}
		return func() tea.Msg {
			return openComposerMsg{target: composerTarget{issue: true, review: "request-changes"}}
		}
	case "comment":
		if a.detail == nil {
			return a.paletteNeedsPR()
		}
		if arg != "" {
			n, client := a.detail.number, a.detail.client
			return func() tea.Msg {
				c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return commentPostedMsg{err: client.IssueComment(c, n, arg)}
			}
		}
		return func() tea.Msg { return openComposerMsg{target: composerTarget{issue: true}} }
	case "checkout":
		if a.detail == nil {
			return a.paletteNeedsPR()
		}
		n, client := a.detail.number, a.detail.client
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			return actionDoneMsg{label: "checkout", err: client.Checkout(c, n)}
		}
	case "ready":
		targets, ok := a.actionTargets()
		if !ok {
			return a.paletteNeedsPR()
		}
		return a.askConfirmTargets(confirmReady, targets)
	case "merge":
		if a.detail == nil {
			return a.paletteNeedsPR()
		}
		return a.startMerge()
	case "open":
		// Shares the `o` key's path so the palette opens the same PRs — and so a
		// list row's real URL is used. Building one from the number alone dropped
		// owner/repo and produced github.com/pull/N, which is not a PR anywhere.
		targets, ok := a.actionTargets()
		if !ok {
			return a.paletteNeedsPR()
		}
		return a.openTargetsInBrowser(targets)
	case "copy":
		// Shares the `y` key's path for the same reason `open` shares `o`'s: the
		// row's own URL is what belongs on the clipboard.
		targets, ok := a.actionTargets()
		if !ok {
			return a.paletteNeedsPR()
		}
		return a.copyTargets(targets)
	case "source":
		if !a.list.selectSourceByName(arg) {
			return errCmd(fmt.Errorf("no source named %q", arg))
		}
		a.state = viewPRList
		return nil
	case "filter":
		a.list.applyQuery(arg)
		a.state = viewPRList
		return nil
	}
	return errCmd(unknownCommand(head))
}

func (a *App) paletteNeedsPR() tea.Cmd {
	return errCmd(fmt.Errorf("open a PR first"))
}
