package authn

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/federation"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// FederationSigner signs an OIDC federation token's claims with the
// active federation key; *token.FederationService is one.
type FederationSigner interface {
	Sign(ctx context.Context, claims any) (string, error)
}

// errNoFederation is a federation token asked of a service constructed
// without a signer or a subject reader.
var errNoFederation = errors.New("no OIDC federation signer")

// FederationToken is an OIDC federation token for r's run: the claims
// federation.NewToken builds from the account's slug, its subject
// template and the ancestry of r's space, which Subjects reads in the
// transaction ctx carries, with a fresh ULID as its jti, signed by the
// active federation key. It has no session row and nothing records it.
// A parse run, an incomplete request and a rendered subject over its
// bound are token.ErrRequest; a space the account lacks is the store's
// port.ErrNotFound.
//
// ref: decision 0006 § "OIDC federation token"
func (s *Service) FederationToken(ctx context.Context, r accessapi.FederationRequest) (accessapi.Token, error) {
	if s.d.Federation == nil || s.d.Subjects == nil {
		return accessapi.Token{}, errNoFederation
	}
	if r.Account.IsZero() || r.Space.IsZero() {
		return accessapi.Token{}, fmt.Errorf("%w: %+v", token.ErrRequest, r)
	}
	subject, err := s.d.Subjects.FederationSubject(ctx, r.Account, r.Space)
	if err != nil {
		return accessapi.Token{}, fmt.Errorf("federation token for run %s: %w", r.Run, err)
	}
	claims, err := federation.NewToken(federation.Issue{
		Issuer:     s.d.FederationIssuer,
		Audience:   s.d.FederationAudience,
		Account:    subject.Account,
		Template:   subject.Template,
		Spaces:     subject.Spaces,
		CallerType: r.CallerType,
		CallerID:   r.Caller,
		RunType:    r.RunType,
		Run:        r.Run,
		Phase:      r.Phase,
		Autodeploy: r.Autodeploy,
		ID:         kernel.NewULID(),
		Now:        s.d.Now(),
		TTL:        s.d.FederationTTL,
	})
	if err != nil {
		return accessapi.Token{}, fmt.Errorf("%w: federation token for run %s: %w", token.ErrRequest, r.Run, err)
	}
	raw, err := s.d.Federation.Sign(ctx, claims)
	if err != nil {
		return accessapi.Token{}, fmt.Errorf("sign the federation token for run %s: %w", r.Run, err)
	}
	return accessapi.Token{Raw: raw, Expires: claims.Expires()}, nil
}
