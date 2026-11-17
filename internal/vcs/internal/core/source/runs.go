package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Runs is a Service as the runs domain's runsapi.RunSources: a run's
// source at its handover, and the release of its credentials at its end.
type Runs struct{ *Service }

// File reads the immutable commit with the stack's integration, without
// minting a worker credential. Provider failures retain their cause.
func (r Runs) File(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, commit, path string) ([]byte, bool, error) {
	src, repo, err := r.source(ctx, acct, stack)
	if err != nil {
		return nil, false, err
	}
	data, err := src.ReadFile(ctx, repo, commit, path)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return nil, false, nil
	case errors.Is(err, port.ErrTooLarge), err == nil && len(data) > runsapi.SourceFileLimit:
		return nil, false, fmt.Errorf("%w: %s", runsapi.ErrFileTooLarge, path)
	case err != nil:
		return nil, false, fmt.Errorf("vcs: read %s at %s: %w", path, commit, err)
	}
	return data, true, nil
}

// Source is the URL acct's run fetches commit of stack from and the
// credential it fetches with, as SourceURL and Credential answer them,
// minting the run's token where the provider narrows one.
//
// ref: decision 0012 § "Source access for runs"
func (r Runs) Source(ctx context.Context, acct kernel.AccountID, stack, run kernel.ULID, commit string) (runsapi.Source, error) {
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
