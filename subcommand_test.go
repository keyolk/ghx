package main

import (
	"strings"
	"testing"
)

// gh's own convention accepts both forms, and a dropped flag does not fail
// loudly — it falls through to detection and quietly targets whatever repository
// the window happens to be in.
func TestParseRepoFlagAcceptsBothForms(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRepo string
		wantRest string
	}{
		{"joined", []string{"--repo=acme/widget"}, "acme/widget", ""},
		{"separated", []string{"--repo", "acme/widget"}, "acme/widget", ""},
		{"separated among other args", []string{"runs", "--repo", "acme/widget", "--limit", "5"},
			"acme/widget", "runs,--limit,5"},
		{"joined among other args", []string{"runs", "--repo=acme/widget"}, "acme/widget", "runs"},
		{"absent", []string{"runs"}, "", "runs"},
		{"none at all", nil, "", ""},
		// A trailing --repo has no value to take; it must not consume past the end.
		{"dangling", []string{"runs", "--repo"}, "", "runs"},
		// The last flag wins, as with any repeated CLI flag.
		{"repeated", []string{"--repo", "a/one", "--repo=b/two"}, "b/two", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, rest := parseRepoFlag(c.args)
			if repo != c.wantRepo {
				t.Errorf("repo = %q, want %q", repo, c.wantRepo)
			}
			if got := strings.Join(rest, ","); got != c.wantRest {
				t.Errorf("rest = %q, want %q", got, c.wantRest)
			}
		})
	}
}
