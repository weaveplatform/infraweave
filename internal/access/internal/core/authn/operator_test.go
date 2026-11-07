package authn_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

const operatorAudience = "http://infraweave.test/operator/v1"

// operatorWorld is newWorld's stores with one operator key, opKey, whose
// secret is "op-secret".
func operatorWorld(t *testing.T, operatorAud string) (*world, *authn.Service, kernel.ULID) {
	t.Helper()
	w := newWorld(t)
	opKey := kernel.NewULID()
	f := w.f
	s := authn.New(authn.Deps{
		Keys:     f,
		Sessions: f,
		Tokens:   f,
		Audience: audience,
		TTL:      10*time.Hour + 500*time.Millisecond,
		Operators: map[kernel.ULID][sha256.Size]byte{
			opKey: sha256.Sum256([]byte("op-secret")),
		},
		OperatorAudience: operatorAud,
		Now:              func() time.Time { return f.now },
	})
	return w, s, opKey
}

// TestUnitExchangeOperator checks that an operator key exchanges for an
// operator token of the operator API's audience, whose session belongs to
// no organization, which AuthenticateOperator accepts and Authenticate
// does not; and that an API token is not an operator credential.
func TestUnitExchangeOperator(t *testing.T) {
	w, s, opKey := operatorWorld(t, operatorAudience)
	g, err := s.Exchange(t.Context(), opKey.String(), "op-secret", "192.0.2.7")
	if err != nil {
		t.Fatal(err)
	}
	if g.Claims.Kind != token.KindOperator || g.Claims.Audience != operatorAudience ||
		g.Claims.Subject != authn.OperatorSubject+opKey.String() ||
		g.Claims.Expiry.Sub(g.Claims.IssuedAt) != 10*time.Hour ||
		g.Session.ActorKind != kernel.PrincipalOperator || g.Session.ActorID != opKey ||
		!g.Session.OrganizationID.IsZero() || !g.Session.AccountID.IsZero() ||
		g.Session.IPAddress != "192.0.2.7" {
		t.Errorf("Exchange = %+v", g)
	}
	p, _, err := s.AuthenticateOperator(t.Context(), g.Token)
	if err != nil || p.Kind != kernel.PrincipalOperator ||
		p.Caller() != "operator:"+opKey.String() {
		t.Errorf("AuthenticateOperator = %+v, %v", p, err)
	}
	if _, _, err := s.Authenticate(
		t.Context(),
		g.Token,
		false,
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("an operator token on an account route: %v", err)
	}
	api, err := s.Exchange(t.Context(), w.key.String(), w.secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateOperator(
		t.Context(),
		api.Token,
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("an API token on the operator API: %v", err)
	}
	// A session of another actor kind, under a token that names the
	// operator API, is no operator credential either.
	forged := string(token.KindAPIKey) + " " + api.Session.ID.String() + " " + operatorAudience
	if _, _, err := s.AuthenticateOperator(
		t.Context(),
		forged,
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("an account session on the operator API: %v", err)
	}
}

// TestUnitExchangeOperatorRefused checks a wrong secret, an operator API
// with no audience configured, and a token store that cannot sign.
func TestUnitExchangeOperatorRefused(t *testing.T) {
	_, s, opKey := operatorWorld(t, operatorAudience)
	if _, err := s.Exchange(
		t.Context(),
		opKey.String(),
		"op-secret ",
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("wrong secret: %v", err)
	}
	_, none, opKey := operatorWorld(t, "")
	if _, err := none.Exchange(
		t.Context(),
		opKey.String(),
		"op-secret",
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("no operator audience: %v", err)
	}
	if _, _, err := none.AuthenticateOperator(
		t.Context(),
		"operator x y",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("no operator audience, authenticate: %v", err)
	}
	w, s, opKey := operatorWorld(t, operatorAudience)
	w.f.fail = "Issue"
	if _, err := s.Exchange(t.Context(), opKey.String(), "op-secret", ""); err == nil ||
		errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("Issue down: %v", err)
	}
	w.f.fail = "CreateSession"
	if _, err := s.Exchange(t.Context(), opKey.String(), "op-secret", ""); err == nil ||
		errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("CreateSession down: %v", err)
	}
}
