package tui

import "testing"

// The Navigation section listed h / l twice with different meanings — "fold /
// unfold" and "cycle tabs" — so it contradicted itself. Within a section a key
// gets one row; behaviour that depends on the tab is described where that tab
// is (the diff's h / l meanings sit on its s and 3 Diff rows).
func TestHelpSectionListsEachKeyOnce(t *testing.T) {
	for _, sec := range helpSections() {
		seen := map[string]bool{}
		for _, row := range sec.rows {
			if seen[row[0]] {
				t.Errorf("%s lists %q twice", sec.title, row[0])
			}
			seen[row[0]] = true
		}
	}
}
