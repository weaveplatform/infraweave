package source

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// Runs is a Service as the runs domain's runsapi.RunSources: a run's
// source at its handover, and the release of its credentials at its end.
type Runs struct{ *Service }

// Source is the URL acct's run fetches commit of stack from and the
// credential it fetches with, as SourceURL and Credential answer them,
// minting the run's token where the provider narrows one.
//
// ref: decision 0012 § "Source access for runs"
func (r Runs) Source(
	ctx context.Context,
	acct kernel.AccountID,
	stack, run kernel.ULID,
	commit string,
) (runsapi.Source, error) {
	url, err := r.SourceURL(ctx, acct, stack, commit)
	if err != nil {
		return runsapi.Source{}, err
	}
	c, err := r.Credential(ctx, acct, stack, run)
	if err != nil || c == nil {
		return runsapi.Source{URL: url}, err
	}
	return runsapi.Source{URL: url, Credential: &runsapi.SourceCredential{
		Username: c.Username, Token: c.Token, Narrowed: c.Narrowed,
	}}, nil
}
