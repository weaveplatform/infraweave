package handover

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// federation puts in sec the OIDC federation token of the phase r's
// handover starts, minted in the handover's transaction beside the in-run
// API token, when r's account has featuresapi.OIDCFederation: a stack's token, whose scope
// the access domain picks from r's type, its state and st's autodeploy,
// so a tracked run that waits for confirmation plans with a read token
// and receives a new write token at PREPARING_APPLY. A run of an account
// without the feature, or a parse run, receives none and goes on.
//
// ref: decision 0006 § "OIDC federation token"
// ref: decision 0008 § "The run specification and its secrets"
func (s *Service) federation(ctx context.Context, r lifecycle.Run, st stacksapi.Settings, sec *secrets) error {
	if s.features == nil || s.org == nil || r.Type == kernel.RunTypeParse {
		return nil
	}
	err := s.federated(ctx, r)
	if _, refused := errors.AsType[*featuresapi.Refusal](err); refused {
		return nil
	}
	if err != nil {
		return err
	}
	tok, err := s.tokens.FederationToken(ctx, federationRequest(r, st))
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the mint
	}
	sec.OIDCToken, sec.OIDCTokenExpiresAt = tok.Raw, tok.Expires.Unix()
	return nil
}

// federated is nil when r's account may use featuresapi.OIDCFederation,
// and the features domain's *featuresapi.Refusal when its tier lacks it,
// the operator switched it off or the service has no features bound.
func (s *Service) federated(ctx context.Context, r lifecycle.Run) error {
	if s.features == nil || s.org == nil {
		return &featuresapi.Refusal{Feature: featuresapi.OIDCFederation}
	}
	org, err := s.org(ctx, r.Account)
	if err != nil {
		return fmt.Errorf("runs: the organization of account %s: %w", r.Account, err)
	}
	//nolint:wrapcheck // the features domain names the read, and its refusal is answered as it is
	return s.features.AllowAccount(ctx, org, r.Account, featuresapi.OIDCFederation)
}

// federationRequest is the federation token request of r, a run of the
// stack st, in its current state: a handover names the preparing state
// that starts the phase, a reissue the phase's own state, and the access
// domain's scope reads the two alike.
func federationRequest(r lifecycle.Run, st stacksapi.Settings) accessapi.FederationRequest {
	return accessapi.FederationRequest{
		Account: r.Account, Run: r.ID, RunType: r.Type,
		CallerType: accessapi.FederationStack, Caller: st.Slug, Space: st.Space,
		Phase: r.State, Autodeploy: st.Autodeploy,
	}
}

// phases are the states a run on a worker executes a phase in, whose
// federation token a reissue renews. A preparing state precedes its
// phase's handover, so no runner holds a token in it yet.
var phases = map[kernel.RunState]bool{
	kernel.RunStateInitializing: true,
	kernel.RunStatePlanning:     true,
	kernel.RunStateApplying:     true,
	kernel.RunStatePerforming:   true,
	kernel.RunStateDestroying:   true,
}

// FederationToken reissues the OIDC federation token of the phase run id
// is in, as workersapi.Scheduler says, minted for its stack as the
// handover's was, so the access domain renders the account's subject
// template as it reads now.
//
// ref: decision 0006 § "OIDC federation token"
func (s *Service) FederationToken(ctx context.Context, id kernel.ULID) (workersapi.FederationToken, error) {
	if !tx.In(ctx) {
		return workersapi.FederationToken{}, lifecycle.ErrNoTransaction
	}
	r, err := s.store.Handed(ctx, id)
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return workersapi.FederationToken{}, fmt.Errorf("%w: %s", workersapi.ErrNotOnWorker, id)
	case err != nil:
		return workersapi.FederationToken{}, err //nolint:wrapcheck // the store names the read
	case r.ExecutionPool() == nil || !r.State.OnWorker():
		return workersapi.FederationToken{}, fmt.Errorf("%w: run %s is %s", workersapi.ErrNotOnWorker,
			id, r.State)
	case !phases[r.State] || r.Type == kernel.RunTypeParse:
		return workersapi.FederationToken{}, fmt.Errorf("%w: %s run %s is %s", workersapi.ErrNotInPhase,
			r.Type, id, r.State)
	}
	if err := s.federated(ctx, r); err != nil {
		return workersapi.FederationToken{}, err
	}
	st, err := s.stacks.Settings(ctx, r.Account, r.Stack)
	if err != nil {
		return workersapi.FederationToken{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	tok, err := s.tokens.FederationToken(ctx, federationRequest(r, st))
	if err != nil {
		return workersapi.FederationToken{}, err //nolint:wrapcheck // the access domain names the mint
	}
	return workersapi.FederationToken{Raw: tok.Raw, Expires: tok.Expires}, nil
}
