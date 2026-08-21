package gh

import "strings"

// GitHub's GraphQL API is a separate resource from REST with its own, much
// smaller budget — 5,000 points/hour against REST's 5,000 requests, and search
// is metered separately again. A fine-grained PAT can also be issued without
// GraphQL access at all. Either way the same reviewer, on the same repository,
// with a token that REST still honours, watches every PR list and status marker
// go blank.
//
// Almost everything ghx reads goes through GraphQL, and not by choice: `gh pr
// list`, `gh pr view`, `gh pr checks`, and `gh search prs` are all GraphQL
// under the hood (only `gh pr diff` is REST), plus ghx's own `gh api graphql`
// calls for statuses and review threads. So the fallbacks live here: when a
// call fails *because of GraphQL specifically*, the same question is asked
// again over REST.
//
// What REST cannot answer is not faked. Thread resolution has no REST
// representation at all, so a fallback thread reports its resolution as
// unknown rather than guessing "unresolved" — a guess would either hide
// feedback or invent it.

// isGraphQLUnavailable reports whether err is GraphQL refusing or throttling
// the request, as opposed to a failure REST would hit too.
//
// This distinction is the whole point: retrying a missing repository or a
// severed network over REST just makes the real error take twice as long to
// appear. gh surfaces API errors on stderr and the exec wrappers fold that text
// into the error, so matching it is what is available — the exit status is the
// same 1 for every kind of failure.
func isGraphQLUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// A 404/422 from a REST endpoint can also mention "graphql" if the caller
	// named it, so require a signal that GraphQL itself refused.
	switch {
	case strings.Contains(msg, "graphql_rate_limit"):
		return true
	case strings.Contains(msg, "api rate limit exceeded"):
		return true
	case strings.Contains(msg, "was submitted too quickly"):
		return true
	case strings.Contains(msg, "resource not accessible by personal access token"):
		return true
	case strings.Contains(msg, "resource not accessible by integration"):
		return true
	}
	// Anything else must name GraphQL *and* look like a refusal, so an ordinary
	// "no commits between A and B" reply from a GraphQL call is not mistaken for
	// one the REST path could answer.
	if !strings.Contains(msg, "graphql") {
		return false
	}
	return strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "not accessible") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "http 403") ||
		strings.Contains(msg, "insufficient")
}
