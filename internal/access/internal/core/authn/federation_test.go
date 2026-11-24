package authn_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/federation"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// signer records the claims it signs and answers a fixed token, or err.
type signer struct {
	claims []federation.Token
	err    error
}

func (s *signer) Sign(_ context.Context, claims any) (string, error) {
	s.claims = append(s.claims, claims.(federation.Token))
	return "signed", s.err
}

// subjects answers subject for every space, or err.
type subjects struct {
	subject port.FederationSubject
	err     error
	asked   []kernel.ULID
}

func (s *subjects) FederationSubject(_ context.Context, _ kernel.AccountID, space kernel.ULID) (port.FederationSubject, error) {
	s.asked = append(s.asked, space)
	return s.subject, s.err
}

// TestUnitFederationToken checks that a federation token carries the
// claims the account's subject, the request and the settings give, with a
// fresh jti each time and no session row, and that a missing signer, an
// incomplete request, a parse run, an unknown space and a failed signing
// are each refused.
func TestUnitFederationToken(t *testing.T) {
	ctx := t.Context()
	sg := &signer{}
	sub := &subjects{subject: port.FederationSubject{
		Account: "acme", Spaces: []string{"acme", "legacy"},
	}}
	f := newWorld(t).f
	s := authn.New(authn.Deps{
		Sessions: f, Tokens: f, Accounts: f, Federation: sg, Subjects: sub,
		FederationIssuer: "https://infraweave.example.com", FederationTTL: time.Hour,
		Now: func() time.Time { return t0 },
	})
	space := kernel.NewULID()
	req := accessapi.FederationRequest{
		Account: kernel.NewAccountID(), Run: kernel.NewULID(), RunType: kernel.RunTypeTracked,
		CallerType: accessapi.FederationStack, Caller: "infra", Space: space,
		Phase: kernel.RunStatePreparingApply,
	}
	tok, err := s.FederationToken(ctx, req)
	if err != nil || tok.Raw != "signed" || !tok.Expires.Equal(t0.Add(time.Hour)) {
		t.Fatalf("token = %+v, %v", tok, err)
	}
	if _, err := s.FederationToken(ctx, req); err != nil {
		t.Fatal(err)
	}
	c := sg.claims[0]
	if c.Issuer != "https://infraweave.example.com" || c.Audience != "infraweave.example.com/acme" ||
		c.Subject != "space:legacy:stack:infra:run_type:TRACKED:scope:write" || c.SpaceID != "legacy" ||
		c.SpacePath != "/acme/legacy" || c.RunID != req.Run.String() || c.Scope != federation.ScopeWrite ||
		c.IssuedAt != t0.Unix() || c.NotBefore != c.IssuedAt || c.Expiry != t0.Add(time.Hour).Unix() {
		t.Errorf("claims = %+v", c)
	}
	if c.ID == "" || c.ID == sg.claims[1].ID {
		t.Errorf("jtis %q and %q", c.ID, sg.claims[1].ID)
	}
	if len(f.sessions) != 0 || len(sub.asked) != 2 || sub.asked[0] != space {
		t.Errorf("sessions %v, spaces asked %v", f.sessions, sub.asked)
	}

	sub.subject.Template, sub.subject.Account = "{callerType}|{callerId}|{scope}", "acme-dev"
	req.CallerType, req.Caller, req.RunType = accessapi.FederationModule, "network", kernel.RunTypeTesting
	if _, err := s.FederationToken(ctx, req); err != nil {
		t.Fatal(err)
	}
	if c := sg.claims[2]; c.Subject != "module|network|write" || c.Audience != "infraweave.example.com/acme-dev" {
		t.Errorf("a module's claims = %+v", c)
	}

	parse := req
	parse.RunType = kernel.RunTypeParse
	noSpace := req
	noSpace.Space = kernel.ULID{}
	for name, r := range map[string]accessapi.FederationRequest{"parse run": parse, "no space": noSpace} {
		if _, err := s.FederationToken(ctx, r); !errors.Is(err, token.ErrRequest) {
			t.Errorf("%s = %v", name, err)
		}
	}
	sub.err = port.ErrNotFound
	if _, err := s.FederationToken(ctx, req); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown space = %v", err)
	}
	sub.err, sg.err = nil, errDown
	if _, err := s.FederationToken(ctx, req); !errors.Is(err, errDown) {
		t.Errorf("signing down = %v", err)
	}
	if _, err := newWorld(t).s.FederationToken(ctx, req); err == nil {
		t.Error("a service with no signer minted a federation token")
	}
}
