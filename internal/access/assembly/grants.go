package assembly

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Grants explains what reaches a space for actors another domain keeps.
func (d *Domain) Grants() accessapi.Grants { return grants{d.authz} }

// grants is accessapi.Grants over the evaluator.
type grants struct{ svc *authz.Service }

// Stacks explains each stack as a run of it: its own bindings and its
// implicit read of the space it lives in.
func (g grants) Stacks(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
	stacks []accessapi.StackHome,
) ([][]accessapi.StackGrant, error) {
	whos := make([]authz.Who, len(stacks))
	for i, st := range stacks {
		whos[i] = authz.Who{Kind: kernel.PrincipalRun, Stack: st.Stack, StackHome: st.Space}
	}
	explained, err := g.svc.ExplainEach(ctx, acct, space, whos)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	out := make([][]accessapi.StackGrant, len(explained))
	for i, gs := range explained {
		for _, gr := range gs {
			out[i] = append(out[i], accessapi.StackGrant{
				Source: string(gr.Source), Binding: gr.Binding, Role: gr.Role, Space: gr.Space,
			})
		}
	}
	return out, nil
}
