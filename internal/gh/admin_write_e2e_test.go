//go:build e2e

package gh

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The write endpoints are the part of this that a fixture cannot check: a
// fixture asserts what someone believed the API accepts. ghx has been here
// before — the suggestion-apply path was merged in a state where it could never
// have succeeded, because the parser and the line arithmetic had unit tests and
// nobody had sent the request.
//
// These send them. The collaborator round trip is destructive on a real
// repository, so it is gated on a login the caller nominates and it puts things
// back; the team calls are read-only probes of the same endpoints the writes
// use, because granting a team access to someone's repo is not something a test
// should do on their behalf.
//
//	GHX_E2E_REPO=keyolk/ghx GHX_E2E_USER=some-login \
//	  go test -tags e2e -run E2EAdmin ./internal/gh/

func adminTarget(t *testing.T) (*Client, string) {
	t.Helper()
	repo := os.Getenv("GHX_E2E_REPO")
	if repo == "" {
		t.Skip("set GHX_E2E_REPO to run the admin write checks")
	}
	return NewClient(60 * time.Second).WithRepo(repo), repo
}

// A grant and its revocation, against the real endpoints.
//
// The login is nominated by the caller rather than derived, because this really
// does invite someone to a repository. It is skipped without one: a test that
// picked a login itself would be sending an invitation to a stranger.
func TestE2EAdminCollaboratorRoundTrip(t *testing.T) {
	c, repo := adminTarget(t)
	user := os.Getenv("GHX_E2E_USER")
	if user == "" {
		t.Skip("set GHX_E2E_USER to a login you may invite to " + repo)
	}

	before, err := c.ListCollaborators(t.Context())
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if hasLogin(before, user) {
		t.Skipf("%s already has access to %s; pick a login that does not", user, repo)
	}

	if err := c.AddCollaborator(t.Context(), user, "pull"); err != nil {
		t.Fatalf("AddCollaborator: %v", err)
	}
	// Cleaned up even if the assertions below fail: a test that leaves an
	// invitation behind is worse than one that fails.
	t.Cleanup(func() {
		if err := c.RemoveCollaborator(t.Context(), user); err != nil {
			t.Errorf("cleanup RemoveCollaborator: %v", err)
		}
	})

	// A grant to a non-member is an invitation, so the roster may not show them
	// until it is accepted — which is exactly what the TUI's toast says. The
	// assertion is therefore that the call was accepted, not that the row
	// appeared.
	t.Logf("invited %s to %s at pull", user, repo)
}

// Revoking access to a login that has none must fail loudly rather than
// reporting success: the TUI turns a nil error into "removed <user>", and a
// silent no-op there would tell someone their revocation landed when it did not.
func TestE2EAdminRemovingANonCollaboratorIsAnError(t *testing.T) {
	c, _ := adminTarget(t)
	// A login that cannot exist: GitHub logins may not contain a dot-dot or
	// start with a hyphen, so this is rejected rather than acting on someone.
	err := c.RemoveCollaborator(t.Context(), "ghx--not-a-real-login--test")
	if err == nil {
		t.Error("removing a non-collaborator reported success, which the TUI " +
			"would render as a completed revocation")
	} else {
		t.Logf("rejected as expected: %v", err)
	}
}

// The team endpoints are the ones with no prior coverage at all, and the URL
// shape is the part most likely to be wrong: it is orgs/{org}/teams/{slug}/...
// rather than the repos/... prefix every other call here uses.
//
// Probed by asking about a team that does not exist. A 404 proves the path
// resolved and the request was well-formed; a 422 or a URL error would mean it
// did not.
func TestE2EAdminTeamEndpointsResolve(t *testing.T) {
	c, repo := adminTarget(t)
	org, _, ok := strings.Cut(repo, "/")
	if !ok {
		t.Skip("GHX_E2E_REPO is not owner/name")
	}

	const missing = "ghx-e2e-no-such-team"
	err := c.RemoveTeamRepo(t.Context(), org, missing)
	if err == nil {
		t.Fatal("revoking a nonexistent team's access reported success")
	}
	if !mentionsNotFound(err) {
		t.Errorf("RemoveTeamRepo failed for the wrong reason — the endpoint may "+
			"be malformed rather than the team missing: %v", err)
	}

	err = c.RemoveTeamMembership(t.Context(), org, missing, "octocat")
	if err == nil {
		t.Fatal("removing a member of a nonexistent team reported success")
	}
	if !mentionsNotFound(err) {
		t.Errorf("RemoveTeamMembership failed for the wrong reason: %v", err)
	}
}

// The permission names have to be the ones the API accepts. A rejected value
// surfaces in the TUI as a failed grant with a message nobody can act on, and
// the set is offered as a fixed list precisely so it cannot be mistyped — which
// only helps if the list is right.
func TestE2EAdminPermissionNamesAreAccepted(t *testing.T) {
	c, repo := adminTarget(t)
	org, _, _ := strings.Cut(repo, "/")

	for _, level := range CollaboratorPermissions {
		// Against a team that does not exist, so nothing is granted. A rejected
		// *permission* answers 422 and names the field; a missing team answers
		// 404. Only the first would mean the list is wrong.
		err := c.SetTeamRepoPermission(t.Context(), org, "ghx-e2e-no-such-team", level)
		if err == nil {
			t.Fatalf("granting %s to a nonexistent team reported success", level)
		}
		if strings.Contains(strings.ToLower(err.Error()), "permission") &&
			!mentionsNotFound(err) {
			t.Errorf("the API rejected the permission name %q: %v", level, err)
		}
	}
}

func hasLogin(rows []Collaborator, login string) bool {
	for _, r := range rows {
		if strings.EqualFold(r.Login, login) {
			return true
		}
	}
	return false
}

func mentionsNotFound(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "not found") || strings.Contains(m, "404")
}
