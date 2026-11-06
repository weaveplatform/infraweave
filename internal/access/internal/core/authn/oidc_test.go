package authn_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// issuer is port.OIDC in memory: a token is the key into claims, and a
// token it does not know fails verification.
type issuer struct {
	claims   map[string]map[string]any
	issuer   string
	audience string
}

func (i *issuer) Verify(_ context.Context, iss, aud, raw string) (map[string]any, error) {
	c, ok := i.claims[raw]
	if !ok {
		return nil, errors.New("refused")
	}
	i.issuer, i.audience = iss, aud
	return c, nil
}

// TestUnitExchangeOIDC exchanges an OIDC key's JWT: the issuer verifies it
// for the key's client ID, its sub must match the anchored subject
// expression, its mapped groups add teams, and the token's sub names the
// client and the key. A token the issuer refuses, a sub outside the
// expression and an issuer no longer allowed are all unauthenticated.
func TestUnitExchangeOIDC(t *testing.T) {
	w := newWorld(t)
	id := kernel.NewULID()
	w.f.keys[id] = apikey.Key{
		OrganizationID: kernel.NewOrganizationID(), AccountID: w.acct, ID: id, Name: "gha",
		Type: apikey.TypeOIDC, Teams: []string{"ci"},
		OIDC: apikey.OIDC{
			Issuer: "https://idp.test", ClientID: "infraweave", SubjectExpression: "repo:main",
			ClaimMapping: []apikey.Mapping{{Name: "roles", Value: apikey.TeamsClaim}},
		},
	}
	idp := &issuer{claims: map[string]map[string]any{
		"good":   {"sub": "repo:main", "roles": []any{"deployers"}},
		"evil":   {"sub": "x-repo:main-evil"},
		"no sub": {"roles": "deployers"},
	}}
	allowed := true
	s := authn.New(authn.Deps{
		Keys: w.f, Sessions: w.f, Tokens: w.f, OIDC: idp,
		IssuerAllowed: func(string) bool { return allowed },
		Audience:      audience, TTL: time.Hour,
		Now: func() time.Time { return w.f.now },
	})
	g, err := s.Exchange(t.Context(), id.String(), "good", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Claims.Subject != "api::oidc::infraweave::"+id.String() ||
		!slices.Equal(g.Session.Teams, []string{"ci", "deployers"}) ||
		idp.issuer != "https://idp.test" || idp.audience != "infraweave" {
		t.Errorf(
			"grant = %+v, %+v; verified for %s, %s",
			g.Claims,
			g.Session,
			idp.issuer,
			idp.audience,
		)
	}
	for _, raw := range []string{"evil", "no sub", "unknown", w.secret} {
		if _, err := s.Exchange(
			t.Context(),
			id.String(),
			raw,
			"",
		); !errors.Is(
			err,
			authn.ErrUnauthenticated,
		) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	allowed = false
	if _, err := s.Exchange(
		t.Context(),
		id.String(),
		"good",
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("an issuer no longer allowed: %v", err)
	}
	allowed = true
	k := w.f.keys[id]
	k.DisabledAt = t0
	w.f.keys[id] = k
	if _, err := s.Exchange(
		t.Context(),
		id.String(),
		"good",
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("a disabled OIDC key: %v", err)
	}
	k.DisabledAt, k.OIDC.SubjectExpression = time.Time{}, "("
	w.f.keys[id] = k
	if _, err := s.Exchange(
		t.Context(),
		id.String(),
		"good",
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("a stored expression that does not compile: %v", err)
	}
}
