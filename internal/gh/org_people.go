package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/keyolk/ghx/internal/cachefile"
)

// The admin panel's "add" prompts ask for a GitHub login, and a login is the
// one thing nobody knows about a colleague. They know "moon.park" — the
// company email — and the GitHub account is "crescent-bird". Finding it meant
// leaving the terminal for the org's people page.
//
// The org already holds the mapping, in two places:
//
//   - membersWithRole carries each member's profile name. Measured on
//     sendbird: 74 of 149 members have one; the rest left it blank.
//   - The SAML identity provider links every SSO identity to its account, and
//     its nameId is the company email. Measured: 160 identities, 150 linked to
//     a user, including moon.park@sendbird.com → crescent-bird. Only an org
//     owner can read it; for anyone else GitHub answers samlIdentityProvider
//     as null (not an error), so the roster degrades to profile names.
//
// Both are GraphQL, one point per 100 rows — four points for the whole sendbird
// roster. The roster changes on hires and departures, not between keystrokes,
// so it is cached on disk for orgPeopleTTL and shared across ghx instances.

// OrgPerson is one member of an organization, with what a reader would search
// them by.
type OrgPerson struct {
	Login string `json:"login"`
	// Name is the GitHub profile name, often blank.
	Name string `json:"name,omitempty"`
	// Email is the SAML nameId — in an SSO org, the company email. Empty when
	// the org has no SAML provider or the viewer is not an owner.
	Email string `json:"email,omitempty"`
}

// orgPeopleTTL is how long a roster answers without re-asking.
//
// Unlike the PR caches there is no updatedAt to prove an entry current, so this
// one is a real staleness bound. A day is short enough that yesterday's hire
// shows up today, and the picker never blocks typing a login it does not list —
// someone missing from a stale roster is a refresh away (ctrl+r), not
// unreachable.
const orgPeopleTTL = 24 * time.Hour

// orgPeopleEntry is a roster as it sits on disk. StartedAt is when the request
// went out, not when it was saved: "is this answer newer than X" is a question
// about the request.
type orgPeopleEntry struct {
	StartedAt time.Time   `json:"started_at"`
	People    []OrgPerson `json:"people"`
}

// ListOrgPeople returns the organization's members with their names and SSO
// emails, served from the shared disk cache unless refresh is set.
//
// The check, fetch and write happen under one lock, so instances that all open
// the picker at once send one roster request between them.
func (c *Client) ListOrgPeople(ctx context.Context, org string, refresh bool) ([]OrgPerson, error) {
	dir := cachefile.Dir("org-people")
	path := ""
	if dir != "" {
		path = filepath.Join(dir, cachefile.Key(org)+".json")
	}
	release := func() {}
	if path != "" {
		release = cachefile.Lock(ctx, filepath.Join(dir, "locks", cachefile.Key(org)+".lock"))
	}
	defer release()

	if path != "" && !refresh {
		var e orgPeopleEntry
		if cachefile.ReadJSON(path, &e) == nil && time.Since(e.StartedAt) < orgPeopleTTL {
			return e.People, nil
		}
	}

	started := time.Now()
	people, err := c.fetchOrgPeople(ctx, org)
	if err != nil {
		return nil, err
	}
	if path != "" {
		// Best-effort: a failed write costs the next picker a fetch, nothing more.
		_ = cachefile.WriteJSON(path, orgPeopleEntry{StartedAt: started, People: people})
	}
	return people, nil
}

const orgMembersQuery = `query($org:String!,$endCursor:String){
  organization(login:$org){
    membersWithRole(first:100,after:$endCursor){
      pageInfo{hasNextPage endCursor}
      nodes{login name}
    }
  }
}`

const orgSAMLQuery = `query($org:String!,$endCursor:String){
  organization(login:$org){
    samlIdentityProvider{
      externalIdentities(first:100,after:$endCursor){
        pageInfo{hasNextPage endCursor}
        nodes{samlIdentity{nameId} user{login name}}
      }
    }
  }
}`

// fetchOrgPeople asks for the member list and the SSO links and joins them.
func (c *Client) fetchOrgPeople(ctx context.Context, org string) ([]OrgPerson, error) {
	byLogin := map[string]*OrgPerson{}
	add := func(login, name, email string) {
		if login == "" {
			return
		}
		key := strings.ToLower(login)
		p, ok := byLogin[key]
		if !ok {
			p = &OrgPerson{Login: login}
			byLogin[key] = p
		}
		if p.Name == "" {
			p.Name = strings.TrimSpace(name)
		}
		if p.Email == "" {
			p.Email = strings.TrimSpace(email)
		}
	}

	members, err := c.execRaw(ctx, "api", "graphql", "--paginate",
		"-f", "query="+orgMembersQuery, "-f", "org="+org)
	switch {
	case err == nil:
		var page struct {
			Data struct {
				Organization *struct {
					MembersWithRole struct {
						Nodes []struct{ Login, Name string }
					}
				}
			}
		}
		if err := eachJSON(members, &page, func() {
			if page.Data.Organization == nil {
				return
			}
			for _, n := range page.Data.Organization.MembersWithRole.Nodes {
				add(n.Login, n.Name, "")
			}
		}); err != nil {
			return nil, err
		}
	case isGraphQLUnavailable(err):
		// REST has the logins but not the names, which still beats an empty
		// picker: a login prefix is often enough to recognize someone.
		rest, rerr := c.execRaw(ctx, "api", "--paginate",
			fmt.Sprintf("orgs/%s/members?per_page=100", org))
		if rerr != nil {
			return nil, rerr
		}
		var page []struct{ Login string }
		if err := eachJSON(rest, &page, func() {
			for _, m := range page {
				add(m.Login, "", "")
			}
		}); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	// The SSO links are the half that answers "moon.park"; losing them loses
	// the email column, not the roster. Owner-only, so a refusal here is the
	// ordinary case for most viewers.
	if saml, err := c.execRaw(ctx, "api", "graphql", "--paginate",
		"-f", "query="+orgSAMLQuery, "-f", "org="+org); err == nil {
		var page struct {
			Data struct {
				Organization *struct {
					SamlIdentityProvider *struct {
						ExternalIdentities struct {
							Nodes []struct {
								SamlIdentity struct {
									NameID string `json:"nameId"`
								}
								User *struct{ Login, Name string }
							}
						}
					}
				}
			}
		}
		if err := eachJSON(saml, &page, func() {
			org := page.Data.Organization
			if org == nil || org.SamlIdentityProvider == nil {
				return
			}
			for _, n := range org.SamlIdentityProvider.ExternalIdentities.Nodes {
				// An identity with no user is an SSO account never linked to
				// GitHub — there is no login to add.
				if n.User != nil {
					add(n.User.Login, n.User.Name, n.SamlIdentity.NameID)
				}
			}
		}); err != nil {
			return nil, fmt.Errorf("decode organization SAML identities: %w", err)
		}
	}

	out := make([]OrgPerson, 0, len(byLogin))
	for _, p := range byLogin {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Login) < strings.ToLower(out[j].Login)
	})
	return out, nil
}

// eachJSON decodes a stream of JSON documents into v, calling fn after each.
// `gh api --paginate` prints one document per page back to back rather than
// merging them, so a single Unmarshal would read only the first page.
func eachJSON(data []byte, v any, fn func()) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		if err := dec.Decode(v); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		fn()
	}
}
