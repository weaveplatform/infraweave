package handover

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
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
	org, err := s.org(ctx, r.Account)
	if err != nil {
		return fmt.Errorf("runs: the organization of account %s: %w", r.Account, err)
	}
	err = s.features.AllowAccount(ctx, org, r.Account, featuresapi.OIDCFederation)
	if _, refused := errors.AsType[*featuresapi.Refusal](err); refused {
		return nil
	}
	if err != nil {
		return err //nolint:wrapcheck // the features domain names the read
	}
	tok, err := s.tokens.FederationToken(ctx, accessapi.FederationRequest{
		Account: r.Account, Run: r.ID, RunType: r.Type,
		CallerType: accessapi.FederationStack, Caller: st.Slug, Space: st.Space,
		Phase: r.State, Autodeploy: st.Autodeploy,
	})
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the mint
	}
	sec.OIDCToken, sec.OIDCTokenExpiresAt = tok.Raw, tok.Expires.Unix()
	return nil
}
