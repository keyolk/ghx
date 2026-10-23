package gh

import (
	"context"
	"strings"
	"testing"
)

// The roster is two GraphQL connections joined by login: profile names from
// membersWithRole, company emails from the SAML identities. Each is paginated,
// and `gh api --paginate` prints one document per page, so a reader that only
// decodes the first document silently drops everyone past the hundredth.
const orgPeopleScript = `
case "$*" in
  *samlIdentityProvider*)
    echo '{"data":{"organization":{"samlIdentityProvider":{"externalIdentities":{"nodes":[{"samlIdentity":{"nameId":"moon.park@acme.com"},"user":{"login":"crescent-bird","name":"moon.Park"}},{"samlIdentity":{"nameId":"ghost@acme.com"},"user":null}]}}}}}' ;;
  *membersWithRole*)
    echo '{"data":{"organization":{"membersWithRole":{"nodes":[{"login":"crescent-bird","name":"moon.Park"},{"login":"alice","name":""}]}}}}'
    echo '{"data":{"organization":{"membersWithRole":{"nodes":[{"login":"zed","name":"Zed Kim"}]}}}}' ;;
esac
`

// requests counts gh invocations. The log is one line per argv, but the
// GraphQL document spans lines, so only the lines that start a call count.
func requests(log []string) int {
	n := 0
	for _, l := range log {
		if strings.HasPrefix(l, "api ") {
			n++
		}
	}
	return n
}

func TestListOrgPeopleJoinsNamesAndSSOEmails(t *testing.T) {
	calls := countingGH(t, orgPeopleScript)
	c := NewClient(0)
	got, err := c.ListOrgPeople(context.Background(), "acme", false)
	if err != nil {
		t.Fatalf("ListOrgPeople: %v", err)
	}
	by := map[string]OrgPerson{}
	for _, p := range got {
		by[p.Login] = p
	}
	if len(got) != 3 {
		t.Fatalf("got %d people %+v, want 3 — the second page or the join was lost", len(got), got)
	}
	if p := by["crescent-bird"]; p.Email != "moon.park@acme.com" || p.Name != "moon.Park" {
		t.Errorf("crescent-bird = %+v, want the profile name and the SSO email", p)
	}
	if _, ok := by["zed"]; !ok {
		t.Error("the second page of members was not read")
	}
	if n := requests(calls()); n != 2 {
		t.Errorf("a cold roster cost %d gh calls, want 2 (members + SSO)", n)
	}
}

// The roster is shared on disk: a second picker — this window or another — is
// answered without a request, and only ctrl+r asks again.
func TestListOrgPeopleIsServedFromTheSharedCache(t *testing.T) {
	calls := countingGH(t, orgPeopleScript)
	ctx := context.Background()
	if _, err := NewClient(0).ListOrgPeople(ctx, "acme", false); err != nil {
		t.Fatal(err)
	}
	cold := requests(calls())
	got, err := NewClient(0).ListOrgPeople(ctx, "acme", false)
	if err != nil || len(got) != 3 {
		t.Fatalf("cached roster = %d people, err %v", len(got), err)
	}
	if n := requests(calls()) - cold; n != 0 {
		t.Errorf("a cached roster cost %d gh calls, want 0", n)
	}
	if _, err := NewClient(0).ListOrgPeople(ctx, "acme", true); err != nil {
		t.Fatal(err)
	}
	if n := requests(calls()) - cold; n != 2 {
		t.Errorf("a refresh cost %d gh calls, want 2", n)
	}
}

// Only an org owner can read the SAML identities; for anyone else GitHub
// answers the provider as null. That loses the email column, not the roster.
func TestListOrgPeopleWithoutSSOAccessKeepsTheMembers(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *samlIdentityProvider*)
    echo '{"data":{"organization":{"samlIdentityProvider":null}}}' ;;
  *membersWithRole*)
    echo '{"data":{"organization":{"membersWithRole":{"nodes":[{"login":"crescent-bird","name":"moon.Park"}]}}}}' ;;
esac
`)
	got, err := NewClient(0).ListOrgPeople(context.Background(), "acme", false)
	if err != nil || len(got) != 1 || got[0].Email != "" || got[0].Name != "moon.Park" {
		t.Fatalf("got %+v, err %v; want the member with a name and no email", got, err)
	}
}

// A GraphQL refusal falls back to the REST member list: logins without names,
// which is still a list to pick from.
func TestListOrgPeopleFallsBackToRESTLogins(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *graphql*)
    echo "GraphQL: API rate limit exceeded" >&2; exit 1 ;;
  *orgs/acme/members*)
    echo '[{"login":"alice"}]'
    echo '[{"login":"bob"}]' ;;
esac
`)
	got, err := NewClient(0).ListOrgPeople(context.Background(), "acme", false)
	if err != nil {
		t.Fatalf("ListOrgPeople: %v", err)
	}
	var logins []string
	for _, p := range got {
		logins = append(logins, p.Login)
	}
	if strings.Join(logins, ",") != "alice,bob" {
		t.Errorf("got %v, want both REST pages", logins)
	}
}
