package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
)

// Copying a PR's URL is the one action whose whole point is to leave ghx: the
// address goes into a Slack message, a ticket, or a browser that is not the one
// `o` would open. It works on the focused PR in either view, and on the whole
// multi-selection in the list — the same targets the other actions take.

// clipboardFunc writes text to the clipboard. It is a field on App so tests can
// assert that a copy was requested without touching the developer's real
// clipboard — and without needing pbcopy on PATH, which CI does not have.
type clipboardFunc func(ctx context.Context, command, text string) error

// copyTargets puts the targets' URLs on the clipboard, one per line.
//
// A row that never carried a URL needs an API round trip to resolve one, so the
// whole thing runs as a command rather than inline. Synthesizing the URL from
// the number alone is not an option: without owner/repo it produces
// github.com/pull/N, which is not a PR anywhere.
func (a *App) copyTargets(targets []actionTarget) tea.Cmd {
	if len(targets) == 0 {
		return nil
	}
	urls := make([]string, 0, len(targets))
	var clients []*gh.Client
	var numbers []int
	for _, t := range targets {
		if t.url != "" {
			urls = append(urls, t.url)
			continue
		}
		clients = append(clients, a.clientFor(t))
		numbers = append(numbers, t.number)
	}

	copyFn := a.copyClipboard
	if copyFn == nil {
		copyFn = copyToClipboard
	}
	command := a.cfg.ClipboardCommand()

	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for i, client := range clients {
			url, err := client.PRPermalink(c, numbers[i])
			if err != nil {
				return errMsg{err: fmt.Errorf("resolve the URL of #%d: %w", numbers[i], err)}
			}
			urls = append(urls, url)
		}
		if len(urls) == 0 {
			return errMsg{err: fmt.Errorf("no pull request URL to copy")}
		}
		if err := copyFn(c, command, strings.Join(urls, "\n")); err != nil {
			return errMsg{err: err}
		}
		if len(urls) == 1 {
			return toastMsg{text: "copied " + urls[0]}
		}
		return toastMsg{text: fmt.Sprintf("copied %d PR URLs", len(urls))}
	}
}

// copyToClipboard pipes text into the platform clipboard command.
//
// An external command rather than an OSC 52 escape sequence: OSC 52 has to be
// passed through by every layer between the app and the terminal emulator, and
// when one of them drops it the copy fails silently — leaving the user to paste
// whatever was on the clipboard before. A command that cannot run says so.
func copyToClipboard(ctx context.Context, command, text string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return fmt.Errorf("no clipboard command configured")
	}
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Stdin = strings.NewReader(text)
	// CombinedOutput rather than Run: a missing or misconfigured clipboard tool
	// explains itself on stderr, and that explanation is the only thing that
	// tells the user why nothing landed on their clipboard.
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("copy via %s: %w: %s", fields[0], err, msg)
		}
		return fmt.Errorf("copy via %s: %w", fields[0], err)
	}
	return nil
}
