package pr

import "strings"

// GitHub review comments can carry a suggested change: a fenced block tagged
// `suggestion`, whose contents replace the lines the comment is anchored to.
// Applying one in the web UI is a button; from a terminal it was a matter of
// reading the block, switching to an editor, and retyping it.
//
// Format, from GitHub's own documentation: the fence is ```suggestion, the body
// is the replacement text verbatim, and an empty body means "delete these
// lines". The comment may contain prose around the block, and more than one
// block — the web UI applies them together as one commit.

// Suggestion is one suggested replacement parsed out of a comment body.
type Suggestion struct {
	// Replacement is the lines the suggestion would write, without a trailing
	// newline. Empty means the anchored lines are to be deleted, which is a
	// legitimate suggestion and not a parse failure — callers must distinguish
	// it from "no suggestion here" by the ok/len of the containing slice.
	Replacement string
	// Deletion records that the block was empty, so a caller need not infer
	// intent from an empty string.
	Deletion bool
}

// ParseSuggestions extracts every suggestion block from a comment body.
//
// The fence is matched on its own line, allowing the leading whitespace GitHub
// preserves when a suggestion sits inside a list item, and allowing the info
// string to carry extra words (```suggestion is what the UI writes, but the
// fence grammar permits attributes after it).
//
// A block that is never closed is ignored rather than being read to the end of
// the comment: an unterminated fence usually means the author was writing about
// suggestions rather than making one, and applying the rest of their prose as
// code would be worse than doing nothing.
func ParseSuggestions(body string) []Suggestion {
	if !strings.Contains(body, "suggestion") {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	var out []Suggestion
	for i := 0; i < len(lines); i++ {
		fence, ok := suggestionFenceOpen(lines[i])
		if !ok {
			continue
		}
		body, end, closed := suggestionBody(lines, i+1, fence)
		if !closed {
			// Unterminated; skip the fence line and keep looking, in case a
			// well-formed block follows.
			continue
		}
		out = append(out, Suggestion{
			Replacement: body,
			Deletion:    body == "",
		})
		i = end
	}
	return out
}

// suggestionFenceOpen recognizes an opening ```suggestion line and returns the
// fence characters that must close it.
//
// The closing fence has to be at least as long as the opening one, per the
// CommonMark rule, which is what lets a suggestion contain a fenced block of
// its own — rare, but the failure mode is silently truncating someone's patch.
func suggestionFenceOpen(line string) (fence string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	marker := "`"
	if strings.HasPrefix(trimmed, "~~~") {
		marker = "~"
	} else if !strings.HasPrefix(trimmed, "```") {
		return "", false
	}
	n := 0
	for n < len(trimmed) && string(trimmed[n]) == marker {
		n++
	}
	info := strings.TrimSpace(trimmed[n:])
	// The info string starts with the language. GitHub writes exactly
	// "suggestion", but attributes may follow it.
	if word, _, _ := strings.Cut(info, " "); !strings.EqualFold(word, "suggestion") {
		return "", false
	}
	return strings.Repeat(marker, n), true
}

// suggestionBody collects lines until the closing fence, returning the body,
// the index of the closing line, and whether one was found.
func suggestionBody(lines []string, from int, fence string) (body string, end int, closed bool) {
	var collected []string
	for i := from; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if strings.HasPrefix(trimmed, fence) &&
			strings.TrimSpace(strings.TrimLeft(trimmed, string(fence[0]))) == "" {
			return strings.Join(collected, "\n"), i, true
		}
		collected = append(collected, lines[i])
	}
	return "", 0, false
}

// HasSuggestion reports whether a comment carries at least one suggestion, for
// the cheap check a render path needs on every row.
func HasSuggestion(body string) bool {
	return len(ParseSuggestions(body)) > 0
}
