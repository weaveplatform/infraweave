package http

import (
	"context"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
)

// changesList is the list runChangesV2's cursors belong to. A cursor's
// key is the address of the page's last change and its ID the run as the
// request named it.
const changesList = "run-changes"

// RunChangesV2 answers a page of the entities one run of the caller's
// account changed. The route has decided SPACE_READ on its stack's space.
func (s Server) RunChangesV2(
	ctx context.Context,
	req api.RunChangesV2RequestObject,
) (api.RunChangesV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Params.Input
	var after *string
	if in.After != nil {
		c, err := page.Decode[string](changesList, *in.After)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		if !strings.EqualFold(c.ID, req.RunId) {
			//nolint:wrapcheck // as above
			return nil, problem.InvalidCursor.Error("The cursor belongs to another run's changes.")
		}
		after = &c.Key
	}
	set, err := s.changes.Entities(ctx, p, req.RunId, query(in.ChangePhase, in.ChangeType))
	if err != nil {
		return nil, runError(err, "", req.RunId)
	}
	from := 0
	if after != nil {
		i := slices.IndexFunc(set.Changes, func(c plan.Change) bool { return c.Address == *after })
		if i < 0 {
			//nolint:wrapcheck // as above
			return nil, problem.InvalidCursor.Error("The cursor's change is not among the " +
				"run's changes this input selects.")
		}
		from = i + 1
	}
	end := min(from+int(kernel.PageLimit(in.First)), len(set.Changes))
	out := api.EntityChangesV2{
		Edges: make([]api.EntityChangeConnectionV2, 0, end-from),
		PageInfo: api.PageInfo{
			HasNextPage: end < len(set.Changes), HasPreviousPage: from > 0,
		},
	}
	for _, c := range set.Changes[from:end] {
		// A string key and ID, so never an error.
		cursor, _ := page.Encode(changesList, kernel.Cursor[string]{Key: c.Address, ID: req.RunId})
		out.Edges = append(out.Edges, api.EntityChangeConnectionV2{
			Cursor: cursor, Node: entityChange(set.Phase, c),
		})
	}
	if n := len(out.Edges); n > 0 {
		out.PageInfo.StartCursor, out.PageInfo.EndCursor = out.Edges[0].Cursor, out.Edges[n-1].Cursor
	}
	return api.RunChangesV2200JSONResponse{Body: out}, nil
}

// RunChangesV3 answers the resources one run of the caller's account
// changed in one phase, as one set, or none before the phase records
// any. The route has decided SPACE_READ on its stack's space.
func (s Server) RunChangesV3(
	ctx context.Context,
	req api.RunChangesV3RequestObject,
) (api.RunChangesV3ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Params.Input
	set, err := s.changes.Resources(ctx, p, req.RunId, query(in.ChangePhase, in.ChangeType))
	if err != nil {
		return nil, runError(err, "", req.RunId)
	}
	body := []api.ResourceChange{}
	if set.Phase != "" {
		rc := api.ResourceChange{Resources: make([]api.EntityChangeV3, len(set.Changes))}
		for i, c := range set.Changes {
			rc.Resources[i] = api.EntityChangeV3{
				Address: c.Address, AttributeChanges: []api.EntityAttributeChange{},
				Metadata: api.EntityChangeMetadata{
					Moved: c.Metadata.Moved, Result: api.EntityChangeResult(c.Metadata.Result),
					Type: api.EntityChangeType(c.Metadata.Type),
				},
				PreviousAddress: deref(c.PreviousAddress),
			}
		}
		body = append(body, rc)
	}
	return api.RunChangesV3200JSONResponse{Body: body}, nil
}

// query is the changes a request's input selects.
func query(phase *api.EntityChangePhase, typ *api.EntityChangeType) changes.Query {
	return changes.Query{Phase: changes.Phase(deref(phase)), Type: plan.Type(deref(typ))}
}

// entityChange is c, a change of phase, as changes-v2 answers it: the
// entity's ID is its address, and its address, name, parent and type are
// the address's parts. An address that does not split is its own name.
func entityChange(phase changes.Phase, c plan.Change) api.EntityChangeV2 {
	entity := api.ManagedEntityV2{Id: c.Address, Address: c.Address, Name: c.Address}
	if e, ok := plan.EntityOf(c.Address); ok {
		entity.Address, entity.Name, entity.Parent, entity.Type = e.Local(), e.Name, e.Module,
			e.Type
	}
	return api.EntityChangeV2{
		Entity: entity, Phase: api.EntityChangePhase(phase),
		Type: api.EntityChangeType(c.Metadata.Type),
	}
}
