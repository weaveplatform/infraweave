package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// TokenLife is how long a run's minted token lives: a day, so a run that
// never ends still loses its token by the next day.
//
// ref: decision 0012 § "Source access for runs"
const TokenLife = 24 * time.Hour

// ErrNoCredential reports a provider that neither mints a run's token nor
// lends the integration's own, where the integration may not mint one.
var ErrNoCredential = errors.New("vcs: the provider hands a run no credential")

// Revoke is the job that revokes a run's minted tokens once the run ends.
// Its unique key is the run, so a run released twice revokes once. It is
// housekeeping that no caller waits on, so it runs on the maintenance
// queue beside the periodic tasks.
var Revoke = queue.Kind{Name: "vcs.revoke", Queue: queue.Maintenance, Policy: queue.Ignore}

// Token is a token minted for a run, as the Tokens keep it: the
// provider's ID of it and what revoking it needs, never the token.
type Token struct {
	Account     kernel.AccountID
	Run         kernel.ULID
	ID          string
	Stack       kernel.ULID
	Integration kernel.ULID
	Provider    string
	Namespace   string
	Repository  string
	ExpiresAt   time.Time
}

// Tokens keeps the tokens minted for runs until they are revoked.
type Tokens interface {
	Add(ctx context.Context, t Token) error
	// List lists acct's run's tokens.
	List(ctx context.Context, acct kernel.AccountID, run kernel.ULID) ([]Token, error)
	// Remove forgets acct's run's token id.
	Remove(ctx context.Context, acct kernel.AccountID, run kernel.ULID, id string) error
}

// Enqueuer enqueues a job in the transaction ctx carries.
type Enqueuer interface {
	EnqueueTx(ctx context.Context, k queue.Kind, payload any, opts ...queue.Option) (int64, error)
}

// revocation is a Revoke job's payload.
type revocation struct {
	Account kernel.ULID `json:"account"`
	Run     kernel.ULID `json:"run"`
}

// Credential implements vcsapi.Sources: a token minted for run that
// reads the stack's repository alone, where its provider is a
// port.Minter and the integration may mint one, kept so Release can
// revoke it; otherwise the integration's own token, where its provider
// is a port.Lender. A repository reached through no integration needs
// none.
func (s *Service) Credential(
	ctx context.Context,
	acct kernel.AccountID,
	stack, run kernel.ULID,
) (*vcsapi.Credential, error) {
	src, repo, err := s.source(ctx, acct, stack)
	if err != nil {
		return nil, err
	}
	if repo.Integration == nil {
		return nil, nil //nolint:nilnil // a repository reached without a credential
	}
	if m, ok := src.(port.Minter); ok {
		c, err := s.mint(ctx, m, acct, stack, run, repo)
		if !errors.Is(err, port.ErrCannotMint) {
			return c, err
		}
	}
	l, ok := src.(port.Lender)
	if !ok {
		return nil, fmt.Errorf("%w: %s for stack %s", ErrNoCredential,
			repo.Integration.Provider, stack)
	}
	c := l.Lend(*repo.Integration)
	s.log.InfoContext(ctx, "a run's credential is its integration's own token, which reads "+
		"every repository the integration reaches",
		slog.String("run", run.String()), slog.String("stack", stack.String()),
		slog.String("provider", repo.Integration.Provider))
	return &vcsapi.Credential{Username: c.Username, Token: c.Token}, nil
}

// mint mints run's token on repo through m and keeps one that Revoke
// must revoke; a token it cannot keep is revoked at once, so no token
// outlives what the Tokens know of.
func (s *Service) mint(
	ctx context.Context,
	m port.Minter,
	acct kernel.AccountID,
	stack, run kernel.ULID,
	repo port.Repository,
) (*vcsapi.Credential, error) {
	c, err := m.Mint(ctx, repo, "infraweave-run-"+run.String(), s.now().Add(TokenLife))
	if err != nil {
		return nil, fmt.Errorf("vcs: mint run %s's token: %w", run, err)
	}
	if c.ID != "" {
		err := s.tokens.Add(ctx, Token{
			Account: acct, Run: run, ID: c.ID, Stack: stack, Integration: repo.Integration.ID,
			Provider: repo.Integration.Provider, Namespace: repo.Namespace,
			Repository: repo.Name, ExpiresAt: c.ExpiresAt,
		})
		if err != nil {
			return nil, errors.Join(err, m.Revoke(ctx, repo, c.ID))
		}
	}
	return &vcsapi.Credential{
		Username: c.Username, Token: c.Token, ExpiresAt: &c.ExpiresAt, Narrowed: true,
	}, nil
}

// Release implements vcsapi.Sources.
func (s *Service) Release(ctx context.Context, acct kernel.AccountID, run kernel.ULID) error {
	_, err := s.jobs.EnqueueTx(ctx, Revoke, revocation{Account: acct.ULID, Run: run},
		queue.UniqueKey(run.String()), queue.Account(acct.ULID))
	return err //nolint:wrapcheck // the queue names the kind
}

// Revoked is the Revoke job's handler: it revokes each of the run's
// tokens through the integration that minted it and forgets it. A token
// already gone revokes as nil, so a job that runs twice revokes nothing
// twice; one whose integration was deleted is forgotten and expires
// alone, as no credential is left to revoke it with.
func (s *Service) Revoked(ctx context.Context, j queue.Job) error {
	var r revocation
	if err := json.Unmarshal(j.Payload, &r); err != nil {
		//nolint:wrapcheck // marks it
		return queue.Permanent(fmt.Errorf("vcs: %s: %w", Revoke.Name, err))
	}
	acct := kernel.AccountID{ULID: r.Account}
	ts, err := s.tokens.List(ctx, acct, r.Run)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for _, t := range ts {
		if err := s.revoke(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// revoke revokes t and forgets it.
func (s *Service) revoke(ctx context.Context, t Token) error {
	id := t.Integration
	in, ok, err := s.integrations.Use(ctx, t.Account, t.Provider, &id)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	m, minter := s.providers[t.Provider].(port.Minter)
	if ok && minter {
		repo := port.Repository{Namespace: t.Namespace, Name: t.Repository, Integration: &in}
		if err := m.Revoke(ctx, repo, t.ID); err != nil {
			return fmt.Errorf("vcs: revoke run %s's token %s: %w", t.Run, t.ID, err)
		}
	}
	//nolint:wrapcheck // the store names the write
	return s.tokens.Remove(ctx, t.Account, t.Run, t.ID)
}
