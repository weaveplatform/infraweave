package http

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// Server is the workers domain's partial of the API's strict server. The
// route table has decided each operation's action on the pool's space,
// or on the space a create names, before its handler runs, and answered
// 404 for a pool the caller cannot read.
type Server struct {
	svc    *pool.Service
	authz  accessapi.Authorizer
	spaces accessapi.Spaces
}

// New returns the partial over the service, deciding allowedActions with
// authz and naming spaces through spaces.
func New(svc *pool.Service, authz accessapi.Authorizer, spaces accessapi.Spaces) Server {
	return Server{svc: svc, authz: authz, spaces: spaces}
}

// WorkerPools lists the pools of the caller's account in the spaces it
// may read, by name.
func (s Server) WorkerPools(
	ctx context.Context,
	req api.WorkerPoolsRequestObject,
) (api.WorkerPoolsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := search.Input{First: req.Params.Limit}
	if req.Params.Cursor != nil {
		in.After = *req.Params.Cursor
	}
	res, err := s.svc.Search(ctx, p, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a bad cursor is a problem
	}
	all := make([]pool.Pool, 0, len(res.Edges))
	for _, e := range res.Edges {
		all = append(all, e.Node)
	}
	views, err := s.views(ctx, p, all...)
	if err != nil {
		return nil, err
	}
	out := api.WorkerPoolPage{Items: views}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return api.WorkerPools200JSONResponse{Body: out}, nil
}

// SearchWorkerPools searches the pools of the caller's account in the
// spaces it may read.
func (s Server) SearchWorkerPools(
	ctx context.Context,
	req api.SearchWorkerPoolsRequestObject,
) (api.SearchWorkerPoolsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Search(ctx, p, apisearch.Input(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchWorkerPoolsOutput{
		Edges:    make([]api.WorkerPoolConnection, 0, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.WorkerPoolConnection{
			Cursor: e.Cursor, Node: api.WorkerPoolRef{Id: e.Node.ID.String()},
		})
	}
	return api.SearchWorkerPools200JSONResponse{Body: out}, nil
}

// SearchWorkerPoolsSuggestions lists the values the pools of the
// caller's account in the spaces it may read hold.
func (s Server) SearchWorkerPoolsSuggestions(
	ctx context.Context,
	req api.SearchWorkerPoolsSuggestionsRequestObject,
) (api.SearchWorkerPoolsSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.svc.Suggest(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.SearchWorkerPoolsSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
}

// WorkerPool answers one pool of the caller's account.
func (s Server) WorkerPool(
	ctx context.Context,
	req api.WorkerPoolRequestObject,
) (api.WorkerPoolResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	wp, err := s.svc.Read(ctx, p.AccountID, req.WorkerPoolId)
	if err != nil {
		return nil, poolError(err)
	}
	v, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	return api.WorkerPool200JSONResponse{
		Body: v, Headers: api.WorkerPool200ResponseHeaders{ETag: etag(wp)},
	}, nil
}

// WorkerPoolCreate creates a pool in the space its input names, from a
// certificate signing request for the pool's key, and answers it with
// its pool token.
func (s Server) WorkerPoolCreate(
	ctx context.Context,
	req api.WorkerPoolCreateRequestObject,
) (api.WorkerPoolCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	wp, err := s.svc.Create(ctx, p, pool.Input{
		Name: b.Name, Space: deref(b.Space), Description: deref(b.Description),
		Labels: deref(b.Labels), PreparingTimeoutSeconds: b.PreparingTimeoutSeconds,
		DriftDetectionRunLimit: b.DriftDetectionRunLimit,
	}, b.CertificateSigningRequest)
	if err != nil {
		return nil, poolError(err)
	}
	v, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolCreate201JSONResponse{Body: v}, nil
}

// WorkerPoolUpdate replaces a pool's settings with its input's.
func (s Server) WorkerPoolUpdate(
	ctx context.Context,
	req api.WorkerPoolUpdateRequestObject,
) (api.WorkerPoolUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	wp, err := s.update(ctx, p, req.WorkerPoolId, strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, poolError(err)
	}
	v, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolUpdate200JSONResponse{
		Body: v, Headers: api.WorkerPoolUpdate200ResponseHeaders{ETag: etag(wp)},
	}, nil
}

func (s Server) update(
	ctx context.Context,
	p kernel.Principal,
	id string,
	expect kernel.Expect,
	b api.WorkerPoolUpdateRequest,
) (pool.Pool, error) {
	wp, err := s.svc.Update(ctx, p, id, expect, pool.Input{
		Name: b.Name, Space: deref(b.Space), Description: deref(b.Description),
		Labels: deref(b.Labels), PreparingTimeoutSeconds: b.PreparingTimeoutSeconds,
		DriftDetectionRunLimit: b.DriftDetectionRunLimit,
	})
	return wp, err //nolint:wrapcheck // answered by poolError
}

// WorkerPoolPatch changes the settings its merge patch names and keeps
// the rest, writing the result as WorkerPoolUpdate writes its body.
func (s Server) WorkerPoolPatch(
	ctx context.Context,
	req api.WorkerPoolPatchRequestObject,
) (api.WorkerPoolPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.WorkerPoolUpdateRequest, int64, error) {
		wp, err := s.svc.Read(ctx, p.AccountID, req.WorkerPoolId)
		if err != nil {
			return api.WorkerPoolUpdateRequest{}, 0, err //nolint:wrapcheck // answered by poolError
		}
		slugs, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.WorkerPoolUpdateRequest{}, 0, err //nolint:wrapcheck // the access domain names it
		}
		return inputView(wp, slugs[wp.Space]), wp.Version, nil
	}
	write := func(b api.WorkerPoolUpdateRequest, expect kernel.Expect) (pool.Pool, error) {
		return s.update(ctx, p, req.WorkerPoolId, expect, b)
	}
	wp, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, poolError(err)
	}
	v, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolPatch200JSONResponse{
		Body: v, Headers: api.WorkerPoolPatch200ResponseHeaders{ETag: etag(wp)},
	}, nil
}

// inputView is wp's settings as an update gives them, which a merge
// patch changes; a setting left to its default is null.
func inputView(wp pool.Pool, space string) api.WorkerPoolUpdateRequest {
	out := api.WorkerPoolUpdateRequest{
		Name: wp.Name, Space: &space, Labels: &wp.Labels,
		DriftDetectionRunLimit: wp.DriftDetectionRunLimit,
	}
	if wp.Description != "" {
		out.Description = &wp.Description
	}
	if wp.PreparingTimeout > 0 {
		out.PreparingTimeoutSeconds = seconds(wp.PreparingTimeout.Seconds())
	}
	return out
}

// WorkerPoolDelete deletes a pool with its workers and answers it as it
// was, marked deleted.
func (s Server) WorkerPoolDelete(
	ctx context.Context,
	req api.WorkerPoolDeleteRequestObject,
) (api.WorkerPoolDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	// The view reads the caller's actions in the pool's space, which the
	// delete leaves, so it is read first.
	wp, err := s.svc.Read(ctx, p.AccountID, req.WorkerPoolId)
	if err != nil {
		return nil, poolError(err)
	}
	v, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.Delete(
		ctx,
		p,
		req.WorkerPoolId,
		strict.IfMatch(req.Params.IfMatch),
	); err != nil {
		return nil, poolError(err)
	}
	v.Deleted = true
	return api.WorkerPoolDelete200JSONResponse{Body: v}, nil
}

// WorkerPoolReset signs a new certificate for the pool from a
// certificate signing request and answers the pool with its new pool
// token; registrations under the old one are refused from then on.
func (s Server) WorkerPoolReset(
	ctx context.Context,
	req api.WorkerPoolResetRequestObject,
) (api.WorkerPoolResetResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	wp, err := s.svc.Reset(ctx, p, req.WorkerPoolId, req.Body.CertificateSigningRequest)
	if err != nil {
		return nil, poolError(err)
	}
	v, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolReset200JSONResponse{Body: v}, nil
}

func (s Server) view(
	ctx context.Context,
	p kernel.Principal,
	wp pool.Pool,
) (api.WorkerPool, error) {
	vs, err := s.views(ctx, p, wp)
	if err != nil {
		return api.WorkerPool{}, err
	}
	return vs[0], nil
}

// views are all as p sees them: the actions p holds in each one's space
// and its space by slug. Workers register over the worker API, so a pool
// that has none answers empty lists and zero counts.
func (s Server) views(
	ctx context.Context,
	p kernel.Principal,
	all ...pool.Pool,
) ([]api.WorkerPool, error) {
	out := make([]api.WorkerPool, 0, len(all))
	if len(all) == 0 {
		return out, nil
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	held := map[kernel.ULID][]api.Action{}
	for _, wp := range all {
		if _, ok := held[wp.Space]; ok {
			continue
		}
		as, err := s.authz.Actions(ctx, p, wp.Space)
		if err != nil {
			return nil, err //nolint:wrapcheck // the snapshot's read names itself
		}
		held[wp.Space] = make([]api.Action, 0, len(as))
		for _, a := range as {
			held[wp.Space] = append(held[wp.Space], api.Action(a))
		}
	}
	for _, wp := range all {
		v := api.WorkerPool{
			Id:                      wp.ID.String(),
			Kind:                    api.WorkerPoolKindWorkerPool,
			Name:                    wp.Name,
			WorkerPool:              api.WorkerPoolRef{Id: wp.ID.String()},
			Space:                   slugs[wp.Space],
			SpaceDetails:            api.SpaceRef{Id: slugs[wp.Space]},
			Labels:                  wp.Labels,
			AllowedActions:          held[wp.Space],
			Config:                  s.svc.Token(wp),
			DriftDetectionRunLimit:  wp.DriftDetectionRunLimit,
			PreparingTimeoutSeconds: seconds(s.svc.PreparingTimeout(wp).Seconds()),
			CreatedAt: unix(
				wp.CreatedAt.Unix(),
			),
			UpdatedAt:      unix(wp.UpdatedAt.Unix()),
			IntentProjects: []api.IntentProjectReference{},
			Scans:          []api.WorkerPoolScanReference{},
			Users:          []api.WorkerPoolUser{},
			Workers:        []api.Worker{},
		}
		if wp.Description != "" {
			v.Description = &wp.Description
		}
		out = append(out, v)
	}
	return out, nil
}

// seconds is a whole number of seconds as the API's int32 carries it.
func seconds(s float64) *int32 {
	v := int32(s)
	return &v
}

// unix is seconds as the API's int32 timestamps carry them, which hold
// every time before 2038.
func unix(s int64) int32 { return int32(s) } //nolint:gosec // as above

// etag is wp's entity tag, which an If-Match names to change it.
func etag(wp pool.Pool) *string {
	t := strict.ETag(wp.Version)
	return &t
}

func deref[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}
	return *v
}

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(
			"The request carries no valid credential.")
	}
	return p, nil
}

// poolError answers the service's refusals. A *kernel.Invalid and a
// stale If-Match are answered as they are.
func poolError(err error) error {
	if d, ok := errors.AsType[*pool.Denied](err); ok {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", d.Action))
		p.Action = string(d.Action)
		return &problem.Error{Problem: p}
	}
	if m, ok := errors.AsType[*pool.Missing](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No %s %q in this account.", m.Kind, m.ID))
	}
	if errors.Is(err, pool.ErrNameTaken) {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.AlreadyExists.Error("Another worker pool of this account has this name.")
	}
	if errors.Is(err, pool.ErrNotFound) {
		//nolint:wrapcheck // as above
		return problem.NotFound.Error("No such worker pool in this account.")
	}
	return err
}
