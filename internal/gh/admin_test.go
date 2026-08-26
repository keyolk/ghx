package gh

import (
	"context"
	"testing"
)

// The collaborators endpoint flattens team membership into a list of people and
// says nothing about which grant is which. On an org repository that is almost
// the whole roster: 150 rows, 149 of them team-derived, none of which can be
// revoked from the repository. ListCollaborators asks twice — the full roster
// and the directly-granted subset — so a row can say where its access comes
// from.

func TestListCollaboratorsMarksDirectGrants(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *"affiliation=direct"*)
    echo '[{"login":"owner-person","role_name":"admin"}]' ;;
  *"affiliation=all"*)
    echo '[{"login":"owner-person","role_name":"admin"},{"login":"team-person","role_name":"write"},{"login":"other","role_name":"read"}]' ;;
esac
`)
	c := NewClient(0).WithRepo("acme/ops")
	got, err := c.ListCollaborators(context.Background())
	if err != nil {
		t.Fatalf("ListCollaborators: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d collaborators, want the full roster of 3", len(got))
	}

	direct := map[string]bool{}
	for _, c := range got {
		direct[c.Login] = c.Direct
	}
	if !direct["owner-person"] {
		t.Error("a directly-granted collaborator is not marked direct")
	}
	if direct["team-person"] || direct["other"] {
		t.Error("a team-derived collaborator is marked as a direct grant")
	}
}

// The marker is a nicety; the roster is the answer. Losing the second call must
// not lose the first — showing 150 people without the marker beats showing an
// error instead of the list.
func TestListCollaboratorsSurvivesADirectLookupFailure(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *"affiliation=direct"*)
    echo "HTTP 403: Forbidden" >&2; exit 1 ;;
  *"affiliation=all"*)
    echo '[{"login":"someone","role_name":"write"}]' ;;
esac
`)
	c := NewClient(0).WithRepo("acme/ops")
	got, err := c.ListCollaborators(context.Background())
	if err != nil {
		t.Fatalf("a failed direct lookup should not fail the roster: %v", err)
	}
	if len(got) != 1 || got[0].Login != "someone" {
		t.Fatalf("roster = %+v, want the one collaborator", got)
	}
	if got[0].Direct {
		t.Error("Direct should be false when it could not be determined")
	}
}

// The matching is case-insensitive: GitHub is inconsistent about login casing
// between endpoints, and a case mismatch would mark every direct grant as
// team-derived.
func TestListCollaboratorsMatchesLoginsCaseInsensitively(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *"affiliation=direct"*) echo '[{"login":"GavinJeong","role_name":"admin"}]' ;;
  *"affiliation=all"*)    echo '[{"login":"gavinjeong","role_name":"admin"}]' ;;
esac
`)
	c := NewClient(0).WithRepo("acme/ops")
	got, err := c.ListCollaborators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Direct {
		t.Errorf("case-differing logins did not match: %+v", got)
	}
}

// The structure the collaborators endpoint throws away.
func TestListTeamsReturnsPermissionsAndNesting(t *testing.T) {
	fakeGH(t, `echo '[{"name":"Core Platform","slug":"team_core_platform","permission":"admin","parent":null},{"name":"FinOps","slug":"infra-finops","permission":"push","parent":{"slug":"infrastructure"}}]'`)
	c := NewClient(0).WithRepo("acme/ops")
	got, err := c.ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d teams, want 2", len(got))
	}
	if got[0].Slug != "team_core_platform" || got[0].Permission != "admin" {
		t.Errorf("first team = %+v, want the admin core-platform team", got[0])
	}
	if got[0].Parent != nil {
		t.Error("a top-level team should have no parent")
	}
	if got[1].Parent == nil || got[1].Parent.Slug != "infrastructure" {
		t.Errorf("a nested team lost its parent: %+v", got[1])
	}
}

// Team members come from the org, not the repository, so the org has to be
// derived from the repo slug rather than assumed.
func TestListTeamMembersQueriesTheOrgEndpoint(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *"orgs/acme/teams/team_core_platform/members"*)
    echo '[{"login":"one"},{"login":"two"}]' ;;
  *) echo "unexpected endpoint: $*" >&2; exit 1 ;;
esac
`)
	c := NewClient(0).WithRepo("acme/ops")
	got, err := c.ListTeamMembers(context.Background(), "acme", "team_core_platform")
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	if len(got) != 2 || got[0].Login != "one" {
		t.Errorf("members = %+v, want two members", got)
	}
}
