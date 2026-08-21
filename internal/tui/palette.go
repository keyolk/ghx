package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A vim-style `:` command line rather than a fuzzy finder: the action set is
// small and stable, and typing `:approve` is faster than fuzzy-matching it.

// paletteCommand is one invocable action.
type paletteCommand struct {
	name    string
	arg     string // argument name shown in help, "" if none
	summary string
}

// paletteCommands is the canonical list, also used to render help.
var paletteCommands = []paletteCommand{
	{"approve", "", "approve this PR"},
	{"request-changes", "", "request changes"},
	{"comment", "", "PR-level comment"},
	{"checkout", "", "gh pr checkout"},
	{"ready", "", "toggle ready for review"},
	{"merge", "", "merge with confirmation"},
	{"refresh", "", "reload the current view"},
	{"open", "", "open in browser"},
	{"copy", "", "copy the PR URL to the clipboard"},
	{"source", "<name>", "switch PR list source"},
	{"filter", "<query>", "filter the PR list"},
	{"help", "", "show help"},
	{"quit", "", "exit ghx"},
}

// palette holds the command line's state.
type palette struct {
	active bool
	input  string
	result string // last acknowledgement or error, shown until next open
}

func (p *palette) open() {
	p.active = true
	p.input = ""
}

func (p *palette) close() {
	p.active = false
	p.input = ""
}

// update handles palette keys. handled=false means the key wasn't consumed.
func (p *palette) update(msg tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	if !p.active {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		p.close()
		return nil, true
	case tea.KeyEnter:
		line := strings.TrimSpace(p.input)
		p.close()
		if line == "" {
			return nil, true
		}
		return func() tea.Msg { return paletteRunMsg{line: line} }, true
	case tea.KeyBackspace:
		// Trim a whole rune so multi-byte input deletes cleanly.
		if r := []rune(p.input); len(r) > 0 {
			p.input = string(r[:len(r)-1])
		}
		return nil, true
	case tea.KeyRunes, tea.KeySpace:
		p.input += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			p.input += " "
		}
		return nil, true
	}
	return nil, true
}

// render draws the command line plus a hint of available commands.
func (p *palette) render(width, height int) string {
	if !p.active {
		return ""
	}
	prompt := helpKeyStyle.Render(":") + p.input + blockCursor()
	var names []string
	for _, c := range paletteCommands {
		names = append(names, c.name)
	}
	hint := dimStyle.Render(wrapCommas(names, max(width-4, 20)))
	body := prompt + "\n" + hint
	if p.result != "" {
		body += "\n" + dimStyle.Render(p.result)
	}
	h := 5
	if p.result != "" {
		h = 6
	}
	return decoratedPane("command", body, width, h, true)
}

func blockCursor() string { return diffCursorStyle.Render(" ") }

// wrapCommas joins names, wrapping at width so the hint never overflows.
func wrapCommas(names []string, width int) string {
	var lines []string
	cur := ""
	for _, n := range names {
		cand := n
		if cur != "" {
			cand = cur + ", " + n
		}
		if lipglossWidth(cand) > width && cur != "" {
			lines = append(lines, cur)
			cur = n
			continue
		}
		cur = cand
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	// Keep the hint to two lines; the full list lives in `?` help.
	if len(lines) > 2 {
		lines = append(lines[:2], "…")
	}
	return strings.Join(lines, "\n")
}

// parsePalette splits a command line into head and argument.
func parsePalette(line string) (head, arg string) {
	fields := strings.SplitN(strings.TrimSpace(line), " ", 2)
	head = fields[0]
	if len(fields) == 2 {
		arg = strings.TrimSpace(fields[1])
	}
	return head, arg
}

// unknownCommand formats the error for an unrecognized head.
func unknownCommand(head string) error {
	return fmt.Errorf("unknown command %q — press ? for the list", head)
}
