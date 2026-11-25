package app

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Counter sets the counts of the kinds of integration its domain owns on
// into, for acct's integrationsCount, and leaves the others alone.
type Counter func(ctx context.Context, acct kernel.AccountID, into *api.IntegrationsCount) error

// counters are the domains' Counters: the cloud domain's counts aws and
// azure, the vcs domain's github, gitlab, bitbucketCloud,
// bitbucketDatacenter and azureDevOps. A kind no domain counts answers
// 0. A nil domain, in a test that constructs none, counts nothing.
func counters(d Domains) []Counter {
	var out []Counter
	if d.Cloud != nil {
		out = append(out, d.Cloud.Count)
	}
	if d.VCS != nil {
		out = append(out, d.VCS.Count)
	}
	return out
}

// IntegrationsCount answers how many integrations of each kind the
// caller's account has, composed from the domains that own them. The
// route has decided SPACE_READ on the account's top space.
func (s apiServer) IntegrationsCount(ctx context.Context, _ api.IntegrationsCountRequestObject) (api.IntegrationsCountResponseObject, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Unauthenticated.Error("The request carries no valid credential.")
	}
	var body api.IntegrationsCount
	for _, c := range s.counters {
		if err := c(ctx, p.AccountID, &body); err != nil {
			return nil, err
		}
	}
	return api.IntegrationsCount200JSONResponse{Body: body}, nil
}
