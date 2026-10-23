package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
)

// A vim-style `:` command line that is also a fuzzy finder: `:approve` still
// runs as typed, and anything else ranks the commands and the view's key
// bindings so the highlighted one runs on enter.

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
	{"rebase", "", "rebase the PR branch onto its base (on GitHub)"},
	{"refresh", "", "reload the current view"},
	{"open", "", "open in browser"},
	{"copy", "", "copy the PR URL to the clipboard"},
	{"source", "<name>", "switch PR list source"},
	{"repo", "<owner/name>", "open a repository's PRs as a tab"},
	{"filter", "<query>", "filter the PR list"},
	{"help", "", "show help"},
	{"quit", "", "exit ghx"},
}

// palette holds the command line's state.
//
// Typing filters two lists at once: the commands above (which take arguments)
// and every key binding the view behind the palette offers, from the same
// table as dispatch (keys.Entries). Enter runs an exactly typed command as
// before; otherwise it runs the highlighted match, so a key that did not fit
// the footer is one fuzzy word away.
type palette struct {
	active bool
	input  string
	result string // last acknowledgement or error, shown until next open

	// entries are the bindings offered, captured when the palette opened;
	// cursor is the highlighted match.
	entries []keys.Entry
	cursor  int
}

// paletteMaxMatches is how many matches the box lists.
const paletteMaxMatches = 6

func (p *palette) open() {
	p.active = true
	p.input = ""
	p.cursor = 0
}

func (p *palette) close() {
	p.active = false
	p.input = ""
	p.cursor = 0
}

// commandEntries presents the commands in the same shape as the bindings, so
// one fuzzy filter ranks both. The "cmd:" prefix keeps a command from being
// mistaken for a binding of the same name.
func commandEntries() []keys.Entry {
	out := make([]keys.Entry, 0, len(paletteCommands))
	for _, c := range paletteCommands {
		help := c.summary
		if c.arg != "" {
			help += " " + c.arg
		}
		out = append(out, keys.Entry{Action: "cmd:" + c.name, Help: help, Group: "Commands"})
	}
	return out
}

// matches ranks the bindings and commands against the head of the input.
// Bindings come first so an equal score picks the key's action — it works on
// the view as it is (the selected rows in the list), where the command of the
// same name may need an open PR.
func (p *palette) matches() []keys.Entry {
	head, _ := parsePalette(p.input)
	all := append(append([]keys.Entry(nil), p.entries...), commandEntries()...)
	return keys.Filter(all, head)
}

// isCommand reports whether head names a command outright.
func isCommand(head string) bool {
	switch head {
	case "q", "?":
		return true
	}
	for _, c := range paletteCommands {
		if c.name == head {
			return true
		}
	}
	return false
}

// update handles palette keys; action is the key's binding in the palette
// context, or "" for a typed character. handled=false means the palette is not
// open.
func (p *palette) update(action string, msg tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	if !p.active {
		return nil, false
	}
	switch action {
	case "cancel":
		p.close()
		return nil, true
	case "run":
		line := strings.TrimSpace(p.input)
		head, arg := parsePalette(line)
		ms := p.matches()
		pick := keys.Entry{}
		if p.cursor < len(ms) {
			pick = ms[p.cursor]
		}
		p.close()
		switch {
		case line == "":
			return nil, true
		case isCommand(head), pick.Action == "":
			// An exact command, or nothing matched: run the line as typed, so
			// an unknown head still says it is unknown.
			return func() tea.Msg { return paletteRunMsg{line: line} }, true
		case strings.HasPrefix(pick.Action, "cmd:"):
			line = strings.TrimSpace(strings.TrimPrefix(pick.Action, "cmd:") + " " + arg)
			return func() tea.Msg { return paletteRunMsg{line: line} }, true
		default:
			action := pick.Action
			return func() tea.Msg { return paletteRunMsg{action: action} }, true
		}
	case "down":
		p.cursor = min(p.cursor+1, max(len(p.matches())-1, 0))
		return nil, true
	case "up":
		p.cursor = max(p.cursor-1, 0)
		return nil, true
	case "erase":
		// Trim a whole rune so multi-byte input deletes cleanly.
		if r := []rune(p.input); len(r) > 0 {
			p.input = string(r[:len(r)-1])
		}
		p.cursor = 0
		return nil, true
	}
	switch msg.Type {
	case tea.KeyRunes:
		p.input += string(msg.Runes)
		p.cursor = 0
	case tea.KeySpace:
		p.input += " "
		p.cursor = 0
	}
	return nil, true
}

// render draws the command line and the best matches. Its keys are in the
// footer, which the palette owns while open.
func (p *palette) render(width, height int) string {
	if !p.active {
		return ""
	}
	prompt := helpKeyStyle.Render(":") + p.input + blockCursor()
	lines := []string{prompt}
	inner := max(width-4, 10)
	ms := p.matches()
	for i, e := range ms[:min(len(ms), paletteMaxMatches)] {
		key := ""
		if len(e.Keys) > 0 {
			key = keyNames.Join(e.Keys[:1])
		} else {
			key = ":" + strings.TrimPrefix(e.Action, "cmd:")
		}
		line := fitCell(padCell(key, 12)+" "+e.Help, inner)
		if i == p.cursor {
			line = selectedRowStyle.Render(padCell(line, inner))
		}
		lines = append(lines, line)
	}
	if len(ms) > paletteMaxMatches {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("… %d more", len(ms)-paletteMaxMatches)))
	}
	if len(ms) == 0 {
		lines = append(lines, dimStyle.Render("no match — enter runs the line as typed"))
	}
	if p.result != "" {
		lines = append(lines, dimStyle.Render(p.result))
	}
	return decoratedPane("command", strings.Join(lines, "\n"), width, min(len(lines)+2, max(height, 3)), true)
}

func blockCursor() string { return diffCursorStyle.Render(" ") }

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
