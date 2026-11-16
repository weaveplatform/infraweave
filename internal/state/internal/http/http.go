package http

import (
	"context"
	"errors"
	"fmt"
	"math"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// State is what the partial asks of the state domain's core.
type State interface {
	SearchWorkspaces(ctx context.Context, p kernel.Principal, in search.Input) (search.Result[core.Workspace], error)
	SuggestWorkspaces(ctx context.Context, p kernel.Principal, in search.SuggestionsInput) (search.Suggestions, error)
	UnlockWorkspace(ctx context.Context, p kernel.Principal, remoteIP, raw string) (core.Workspace, error)
	UnlockStack(ctx context.Context, p kernel.Principal, remoteIP, slug string) ([]core.Workspace, error)
	Import(ctx context.Context, p kernel.Principal, remoteIP, slug, object string) error
}

// Server is the state domain's partial of the API's strict server. The
// route table has decided each operation's action, and answered 404 for
// a workspace or stack the caller cannot read, before its handler runs.
type Server struct{ state State }

// New returns the partial over the core.
func New(st State) Server { return Server{state: st} }

// SearchTofuWorkspaces searches the workspaces of the stacks of the
// caller's account in the spaces it may read.
func (s Server) SearchTofuWorkspaces(ctx context.Context, req api.SearchTofuWorkspacesRequestObject) (api.SearchTofuWorkspacesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.state.SearchWorkspaces(ctx, p, apisearch.Input(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchTofuWorkspacesOutput{
		Edges:    make([]api.TofuWorkspacesConnection, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for i, e := range res.Edges {
		out.Edges[i] = api.TofuWorkspacesConnection{Cursor: e.Cursor, Node: view(e.Node)}
	}
	return api.SearchTofuWorkspaces200JSONResponse{Body: out}, nil
}

// SearchTofuWorkspacesSuggestions lists the names and stacks the
// workspaces of the stacks of the caller's account in the spaces it may
// read hold.
func (s Server) SearchTofuWorkspacesSuggestions(
	ctx context.Context,
	req api.SearchTofuWorkspacesSuggestionsRequestObject,
) (api.SearchTofuWorkspacesSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.state.SuggestWorkspaces(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	return api.SearchTofuWorkspacesSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
}

// TofuWorkspaceUnlock releases every lock held on the workspace, recording
// the caller's address with the release. The route has decided
// STACK_WORKSPACE_UNLOCK on its stack's space.
func (s Server) TofuWorkspaceUnlock(ctx context.Context, req api.TofuWorkspaceUnlockRequestObject) (api.TofuWorkspaceUnlockResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.state.UnlockWorkspace(ctx, p, accessapi.ClientAddr(ctx), req.TofuWorkspaceId)
	if err != nil {
		return nil, refusal(err, req.TofuWorkspaceId)
	}
	return api.TofuWorkspaceUnlock200JSONResponse{Body: view(w)}, nil
}

// TofuWorkspaceUnlockAll releases every lock held on each workspace of
// the stack, recording the caller's address with each release. The route
// has decided STACK_WORKSPACE_UNLOCK on the stack's space.
func (s Server) TofuWorkspaceUnlockAll(ctx context.Context, req api.TofuWorkspaceUnlockAllRequestObject) (api.TofuWorkspaceUnlockAllResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := s.state.UnlockStack(ctx, p, accessapi.ClientAddr(ctx), req.StackId)
	if err != nil {
		return nil, refusal(err, req.StackId)
	}
	out := api.TofuWorkspaceUnlockAllResult{UnlockedWorkspaces: make([]api.TofuWorkspace, len(ws))}
	for i, w := range ws {
		out.UnlockedWorkspaces[i] = view(w)
	}
	return api.TofuWorkspaceUnlockAll200JSONResponse{Body: out}, nil
}

// StackManagedStateImport imports the state file the caller uploaded
// under the body's object ID into the stack's default workspace,
// recording the caller's address with the import, and answers true. A
// stack whose state, lock or runs refuse it answers 409 invalid-state,
// naming what blocks it. The route has decided STACK_MANAGED_STATE_IMPORT
// on the stack's space.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (s Server) StackManagedStateImport(
	ctx context.Context,
	req api.StackManagedStateImportRequestObject,
) (api.StackManagedStateImportResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	err = s.state.Import(ctx, p, accessapi.ClientAddr(ctx), req.StackId, req.Body.State)
	if b, ok := errors.AsType[*core.Blocked](err); ok {
		return nil, problem.InvalidState.Error(b.Detail) //nolint:wrapcheck // a problem, answered as it is
	}
	if err != nil {
		return nil, refusal(err, req.StackId)
	}
	return api.StackManagedStateImport200JSONResponse{Body: true}, nil
}

// view is w as the API answers it: the default workspace named default,
// and lockedBy who triggered the run whose lock holds it, absent while it
// is free.
//
// ref: decision 0010 § "Storage"
func view(w core.Workspace) api.TofuWorkspace {
	return api.TofuWorkspace{
		Id: w.ID.String(), Name: w.DisplayName(), CreatedAt: unix(w.CreatedAt.Unix()),
		IsLocked: w.Locked, LockedBy: w.LockedBy(),
	}
}

// unix is s as the API's int32 Unix time, which holds every time up to
// 2038.
func unix(s int64) int32 { return int32(min(max(s, 0), math.MaxInt32)) } //nolint:gosec // clamped

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(
			"The request carries no valid credential.")
	}
	return p, nil
}

// refusal answers a workspace or stack named id that the account lacks,
// which the route's check and the handler's read can race, as 404; any
// other error, a *kernel.Invalid among them, passes as it is.
func refusal(err error, id string) error {
	switch {
	case errors.Is(err, core.ErrNotFound):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No tofu workspace %q in this account.", id))
	case errors.Is(err, stacksapi.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No stack %q in this account.", id))
	}
	return err
}
