package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// BindRuns makes runs the reader of an in-run API token's run, before the
// service authenticates any request. Until it is bound, every in-run API
// token is refused.
func (s *Service) BindRuns(runs accessapi.Runs) { s.runs = runs }

// WorkerToken is a worker session token for worker: aud infraweave-worker,
// sub the worker, auth.tokens.worker.ttl, and no session row, so only its
// expiry ends it.
func (s *Service) WorkerToken(ctx context.Context, worker kernel.ULID) (accessapi.Token, error) {
	return s.bare(ctx, token.KindWorker, accessapi.WorkerAudience, worker, s.d.WorkerTTL)
}

// VerifyWorker is the worker a worker session token names.
func (s *Service) VerifyWorker(ctx context.Context, raw string) (accessapi.Bearer, error) {
	return s.verifyBare(ctx, raw, token.KindWorker, accessapi.WorkerAudience)
}

// AuthenticateWorker is the principal a worker session token acts as, a
// worker with no account or session, and the token's claims, of which the
// worker API reads the expiry; any other token is ErrUnauthenticated.
//
// ref: decision 0006 § "Token kinds"
func (s *Service) AuthenticateWorker(
	ctx context.Context,
	raw string,
) (kernel.Principal, token.Claims, error) {
	b, err := s.VerifyWorker(ctx, raw)
	if errors.Is(err, accessapi.ErrInvalidToken) {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	if err != nil {
		return kernel.Principal{}, token.Claims{}, err
	}
	return kernel.Principal{Kind: kernel.PrincipalWorker, ActorID: b.ID},
		token.Claims{Kind: token.KindWorker, Subject: b.ID.String(), Expiry: b.Expires}, nil
}

// RunToken is a run token for run: aud infraweave-runner, sub the run,
// auth.tokens.run.ttl, and no session row. The worker protocol accepts it
// only while the run is in a worker state, which bounds it more tightly
// than its expiry.
func (s *Service) RunToken(ctx context.Context, run kernel.ULID) (accessapi.Token, error) {
	return s.bare(ctx, token.KindRunner, accessapi.RunnerAudience, run, s.d.RunTTL)
}

// VerifyRun is the run a run token names.
func (s *Service) VerifyRun(ctx context.Context, raw string) (accessapi.Bearer, error) {
	return s.verifyBare(ctx, raw, token.KindRunner, accessapi.RunnerAudience)
}

// bare issues a token of kind for audience naming id, with no session
// row: its jti is a fresh ULID that nothing records.
func (s *Service) bare(
	ctx context.Context,
	kind token.Kind,
	audience string,
	id kernel.ULID,
	ttl time.Duration,
) (accessapi.Token, error) {
	raw, c, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     kind,
		Subject:  id.String(),
		Session:  kernel.NewULID(),
		Audience: audience,
		TTL:      ttl,
	})
	if err != nil {
		return accessapi.Token{}, fmt.Errorf("issue %s token for %s: %w", kind, id, err)
	}
	return accessapi.Token{Raw: raw, Expires: c.Expiry}, nil
}

// verifyBare is the ID a bare token of kind for audience names and its
// expiry; any other token is accessapi.ErrInvalidToken.
func (s *Service) verifyBare(
	ctx context.Context,
	raw string,
	kind token.Kind,
	audience string,
) (accessapi.Bearer, error) {
	c, err := s.d.Tokens.Verify(ctx, raw, audience)
	if errors.Is(err, token.ErrToken) {
		return accessapi.Bearer{}, accessapi.ErrInvalidToken
	}
	if err != nil {
		return accessapi.Bearer{}, err //nolint:wrapcheck // the store names the read
	}
	id, err := kernel.ParseULID(c.Subject)
	if c.Kind != kind || err != nil {
		return accessapi.Bearer{}, accessapi.ErrInvalidToken
	}
	return accessapi.Bearer{ID: id, Expires: c.Expiry}, nil
}

// InRunToken is an in-run API token for r's run: an API token whose sub is
// <slug>/<run> and whose session has actor kind run in r's account,
// living auth.tokens.run_api.ttl. Revoking the session revokes it, and it
// authenticates only while the bound Runs finds the run live.
//
// ref: decision 0006 § "In-run API token"
func (s *Service) InRunToken(
	ctx context.Context,
	r accessapi.InRunRequest,
) (accessapi.Token, error) {
	if r.Slug == "" || r.Run.IsZero() || r.Account.IsZero() {
		return accessapi.Token{}, fmt.Errorf("%w: %+v", token.ErrRequest, r)
	}
	acct, err := s.d.Accounts.Account(ctx, r.Account)
	if err != nil {
		return accessapi.Token{}, fmt.Errorf("in-run token for run %s: %w", r.Run, err)
	}
	sid := kernel.NewULID()
	raw, c, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindRun,
		Subject:  r.Slug + "/" + r.Run.String(),
		Session:  sid,
		Audience: s.d.Audience,
		TTL:      s.d.RunAPITTL,
	})
	if err != nil {
		return accessapi.Token{}, fmt.Errorf("in-run token for run %s: %w", r.Run, err)
	}
	// The token is unusable until its session exists, so a failure here
	// leaves nothing that authenticates.
	if err := s.d.Sessions.CreateSession(ctx, session.Session{
		ID:             sid,
		OrganizationID: acct.OrganizationID,
		AccountID:      r.Account,
		Channel:        session.ChannelUnknown,
		ActorKind:      kernel.PrincipalRun,
		ActorID:        r.Run,
		Login:          c.Subject,
		CreatedAt:      c.IssuedAt,
		ExpiresAt:      c.Expiry,
	}); err != nil {
		return accessapi.Token{}, err //nolint:wrapcheck // the store names the write
	}
	return accessapi.Token{Raw: raw, Expires: c.Expiry}, nil
}

// runPrincipal completes a run session's principal with its run's stack
// and the stack's space, refusing a run the bound Runs does not find live.
func (s *Service) runPrincipal(ctx context.Context, p kernel.Principal) (kernel.Principal, error) {
	if s.runs == nil {
		return kernel.Principal{}, ErrUnauthenticated
	}
	ra, err := s.runs.RunAccess(ctx, p.AccountID, p.ActorID)
	if errors.Is(err, accessapi.ErrNoSubject) || errors.Is(err, port.ErrNotFound) {
		return kernel.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return kernel.Principal{}, fmt.Errorf("read run %s: %w", p.ActorID, err)
	}
	if !ra.Live {
		return kernel.Principal{}, ErrUnauthenticated
	}
	p.StackID, p.StackSpaceID = ra.Stack, ra.Space
	return p, nil
}
