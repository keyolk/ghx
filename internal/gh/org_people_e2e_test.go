//go:build e2e

package gh

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The roster's two halves are only worth anything joined, and whether the SSO
// half is readable depends on who is asking — an owner gets emails, a member
// gets null. This reads a real org and finds a person by email, the lookup the
// picker exists for.
//
//	GHX_E2E_ORG=sendbird GHX_E2E_REPO=sendbird/ops-k8s GHX_E2E_EMAIL=moon.park@sendbird.com GHX_E2E_LOGIN=crescent-bird \
//	  go test -tags e2e -run OrgPeople ./internal/gh/
func TestE2EOrgPeopleFindsALoginByEmail(t *testing.T) {
	org, email, login := os.Getenv("GHX_E2E_ORG"), os.Getenv("GHX_E2E_EMAIL"), os.Getenv("GHX_E2E_LOGIN")
	if org == "" {
		t.Skip("GHX_E2E_ORG not set")
	}
	// HOME is not isolated: gh's own login lives under it. refresh skips the
	// cache read, so the answer is the network's. GHX_E2E_REPO picks the
	// account the way `ghx admin` does — by a repository of that org — since
	// the active gh login may be one that is not a member at all (it then sees
	// only the public members, and no SSO links).
	c := NewClient(60 * time.Second)
	if repo := os.Getenv("GHX_E2E_REPO"); repo != "" {
		c = c.WithRepo(repo)
	}
	people, err := c.ListOrgPeople(t.Context(), org, true)
	if err != nil {
		t.Fatalf("ListOrgPeople: %v", err)
	}
	named, emailed := 0, 0
	for _, p := range people {
		if p.Name != "" {
			named++
		}
		if p.Email != "" {
			emailed++
		}
	}
	t.Logf("%s: %d people, %d with a name, %d with an SSO email", org, len(people), named, emailed)
	if email == "" {
		return
	}
	for _, p := range people {
		if strings.EqualFold(p.Email, email) {
			if login != "" && !strings.EqualFold(p.Login, login) {
				t.Errorf("%s maps to %s, want %s", email, p.Login, login)
			}
			return
		}
	}
	t.Errorf("no member has SSO email %s", email)
}
