package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// registerLinking registers the identity linking steps: an organization
// admin's link of two members, kept by name for the scenario's requests,
// and the sign-ins through each member's provider that prove it, from the
// scenario's browser or from another one.
func (s *ssoSteps) registerLinking(sc *godog.ScenarioContext) {
	sc.Step(`^the home organization's live members are counted$`, s.countMembers)
	sc.Step(`^the home organization has (\d+) more live members? than counted$`, s.moreMembers)
	sc.Step(`^the member "([^"]+)" is kept as "(\w+)"$`, s.keepMember)
	sc.Step(`^the member "([^"]+)" also holds the identity "([^"]+)" at the provider "(\w+)" as "([^"]*)"$`,
		s.holdsIdentity)
	sc.Step(`^the answer's link is kept as "(\w+)"$`, s.keepLink)
	sc.Step(`^(the browser|another browser) signs in through the provider "(\w+)" at ([AB]) for the link "(\w+)"$`,
		s.prove)
	sc.Step(`^the sign-in sends the browser back to the link "(\w+)"$`, s.backToLink)
	sc.Step(`^the link "(\w+)" expires$`, s.expireLink)
}

// liveMembers counts the home organization's members that take a seat.
func (s *ssoSteps) liveMembers(ctx context.Context) (int, error) {
	return s.w.Number(ctx, fmt.Sprintf(`SELECT count(*) FROM members
		WHERE organization_id = '%s' AND status <> 'deactivated'`, harness.UUIDOf(harness.Home.Org)))
}

func (s *ssoSteps) countMembers(ctx context.Context) error {
	n, err := s.liveMembers(ctx)
	s.counted = n
	return err
}

func (s *ssoSteps) moreMembers(ctx context.Context, more int) error {
	n, err := s.liveMembers(ctx)
	if err != nil {
		return err
	}
	if n != s.counted+more {
		return fmt.Errorf("%w: %d live members, %d counted", harness.ErrStep, n, s.counted)
	}
	return nil
}

// memberID is the ID of the home organization's member login.
func (s *ssoSteps) memberID(ctx context.Context, login string) (kernel.ULID, error) {
	var id kernel.ULID
	err := s.w.Row(ctx, `SELECT id FROM members WHERE organization_id = $1 AND login = $2`,
		[]any{harness.UUIDOf(harness.Home.Org), login}, &id)
	return id, err //nolint:wrapcheck // names the query
}

func (s *ssoSteps) keepMember(ctx context.Context, login, name string) error {
	id, err := s.memberID(ctx, login)
	if err != nil {
		return err
	}
	s.w.IDs[name] = id.String()
	return nil
}

// holdsIdentity records an identity of the member at the provider, as a
// merge would have moved it to them.
func (s *ssoSteps) holdsIdentity(ctx context.Context, login, subject, name, asserted string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	_, err = s.w.Query(ctx, fmt.Sprintf(`INSERT INTO member_identities (organization_id, member_id,
			identity_provider_id, subject, login)
		SELECT organization_id, id, '%s', %s, %s FROM members WHERE organization_id = '%s' AND login = %s`,
		harness.UUIDOf(p.id), harness.Literal(subject), harness.Literal(asserted),
		harness.UUIDOf(harness.Home.Org), harness.Literal(login)))
	return err //nolint:wrapcheck // names the query
}

// keepLink keeps the link the last answer created, whose URL must be its
// page on the public URL.
func (s *ssoSteps) keepLink(name string) error {
	r := s.w.Last()
	var l struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if r.Status != http.StatusCreated || json.Unmarshal(r.Body, &l) != nil ||
		l.URL != harness.PublicURL+"/auth/link/"+l.ID {
		return fmt.Errorf("%w: the link answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	s.w.IDs[name] = l.ID
	return nil
}

// prove opens the link's page at replica in the browser, follows its
// provider's link to the provider, and brings the provider's answer back
// to replica. A page that refuses the link is the last answer, as a
// refused callback is.
func (s *ssoSteps) prove(ctx context.Context, who, name, replica, link string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	jar := s.w.Jar
	if who == "another browser" {
		jar = harness.NewJar()
	}
	path := "/auth/link/" + s.w.IDs[link]
	page, err := s.browse(ctx, jar, replica, path)
	if err != nil || page.Status != http.StatusOK {
		return err
	}
	start := path + "/" + p.id
	if !strings.Contains(string(page.Body), `href="`+start+`"`) {
		return fmt.Errorf("%w: the link's page offers no %s: %s", harness.ErrStep, name, page.Body)
	}
	s.start, err = s.browse(ctx, jar, replica, start)
	if err != nil {
		return err
	}
	loc := s.start.Header.Get("Location")
	if s.start.Status != http.StatusFound || !strings.HasPrefix(loc, p.issuer) {
		return fmt.Errorf("%w: the start answered %d to %q: %s", harness.ErrStep, s.start.Status, loc, s.start.Body)
	}
	client := s.mock.Client()
	if p.local {
		client = s.local.Client()
	}
	back, err := deps.SignIn(ctx, client, loc, p.next)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	if !strings.HasPrefix(back.String(), ssoCallback+"?") {
		return fmt.Errorf("%w: the provider sent the browser to %s", harness.ErrStep, back)
	}
	s.callback = back
	_, err = s.browse(ctx, jar, replica, back.RequestURI())
	return err
}

func (s *ssoSteps) backToLink(link string) error {
	return s.sentTo("/auth/link/" + s.w.IDs[link])
}

// expireLink moves the link's hour into the past.
func (s *ssoSteps) expireLink(ctx context.Context, link string) error {
	_, err := s.w.Query(ctx, fmt.Sprintf(`UPDATE member_links SET created_at = now() - interval '2 hours',
		expires_at = now() - interval '1 hour' WHERE id = '%s'`, harness.UUIDOf(s.w.IDs[link])))
	return err //nolint:wrapcheck // names the query
}
