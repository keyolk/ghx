package pr

import "testing"

func TestParseSuggestionExtractsTheReplacement(t *testing.T) {
	body := "This reads better the other way round:\n\n" +
		"```suggestion\n" +
		"\tif err != nil {\n" +
		"\t\treturn fmt.Errorf(\"open: %w\", err)\n" +
		"\t}\n" +
		"```\n"
	got := ParseSuggestions(body)
	if len(got) != 1 {
		t.Fatalf("parsed %d suggestions, want 1", len(got))
	}
	want := "\tif err != nil {\n\t\treturn fmt.Errorf(\"open: %w\", err)\n\t}"
	if got[0].Replacement != want {
		t.Errorf("replacement = %q, want %q", got[0].Replacement, want)
	}
	if got[0].Deletion {
		t.Error("a non-empty suggestion was read as a deletion")
	}
}

// An empty block means "delete these lines" — a real suggestion, not a parse
// failure. Treating it as no-suggestion would silently drop a reviewer's
// request to remove code.
func TestParseSuggestionRecognizesADeletion(t *testing.T) {
	got := ParseSuggestions("Drop this:\n\n```suggestion\n```\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d suggestions, want 1", len(got))
	}
	if !got[0].Deletion || got[0].Replacement != "" {
		t.Errorf("got %+v, want an empty deletion", got[0])
	}
}

// The web UI applies every block in a comment as one change, so all of them
// have to be found.
func TestParseSuggestionFindsSeveralInOneComment(t *testing.T) {
	body := "First:\n```suggestion\na\n```\nand second:\n```suggestion\nb\n```\n"
	got := ParseSuggestions(body)
	if len(got) != 2 {
		t.Fatalf("parsed %d suggestions, want 2", len(got))
	}
	if got[0].Replacement != "a" || got[1].Replacement != "b" {
		t.Errorf("got %q and %q, want a and b", got[0].Replacement, got[1].Replacement)
	}
}

// GitHub preserves the indentation of a suggestion written inside a list item.
func TestParseSuggestionAcceptsAnIndentedFence(t *testing.T) {
	body := "- like so:\n  ```suggestion\n  indented\n  ```\n"
	got := ParseSuggestions(body)
	if len(got) != 1 {
		t.Fatalf("parsed %d suggestions, want 1", len(got))
	}
	// The body is taken verbatim: GitHub applies exactly these bytes, and
	// trimming what looks like list indentation would silently change the patch.
	if got[0].Replacement != "  indented" {
		t.Errorf("replacement = %q, want the line verbatim", got[0].Replacement)
	}
}

// A longer fence lets a suggestion contain a fenced block of its own. Closing
// on the first ``` would truncate the patch without saying so.
func TestParseSuggestionHonoursALongerFence(t *testing.T) {
	body := "````suggestion\n```go\ncode\n```\n````\n"
	got := ParseSuggestions(body)
	if len(got) != 1 {
		t.Fatalf("parsed %d suggestions, want 1", len(got))
	}
	if got[0].Replacement != "```go\ncode\n```" {
		t.Errorf("replacement = %q — the inner fence was mishandled", got[0].Replacement)
	}
}

// A comment explaining suggestions is not a suggestion. Reading an unterminated
// fence to the end of the body would turn prose into a patch.
func TestParseSuggestionIgnoresAnUnterminatedFence(t *testing.T) {
	if got := ParseSuggestions("you could write ```suggestion and then some\nmore prose"); len(got) != 0 {
		t.Errorf("parsed %d suggestions from prose, want 0: %+v", len(got), got)
	}
}

// Other fenced code is not a suggestion.
func TestParseSuggestionIgnoresOtherLanguages(t *testing.T) {
	if got := ParseSuggestions("```go\nfmt.Println()\n```\n"); len(got) != 0 {
		t.Errorf("a go block was read as a suggestion: %+v", got)
	}
	// "suggestions" is a different word; matching on a prefix would take it.
	if got := ParseSuggestions("```suggestions\nx\n```\n"); len(got) != 0 {
		t.Errorf("```suggestions was read as a suggestion: %+v", got)
	}
}

// GitHub serves comment bodies with CRLF line endings; a stray \r inside the
// replacement would be written into the file.
func TestParseSuggestionNormalizesCRLF(t *testing.T) {
	got := ParseSuggestions("```suggestion\r\nline\r\n```\r\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d suggestions, want 1", len(got))
	}
	if got[0].Replacement != "line" {
		t.Errorf("replacement = %q — a carriage return survived", got[0].Replacement)
	}
}

func TestHasSuggestion(t *testing.T) {
	if !HasSuggestion("```suggestion\nx\n```") {
		t.Error("a suggestion was not detected")
	}
	if HasSuggestion("just a comment about a suggestion") {
		t.Error("prose mentioning suggestions was detected as one")
	}
}
