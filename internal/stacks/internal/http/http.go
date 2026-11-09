package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// Server is the stacks domain's partial of the API's strict server. The
// route table has decided each operation's action on the stack's space,
// or on the space a create names, before its handler runs, and answered
// 404 for a stack the caller cannot read.
type Server struct {
	stacks *stacks.Service
	authz  accessapi.Authorizer
	spaces accessapi.Spaces
}

// New returns the partial over the service, deciding allowedActions with
// authz and naming spaces through spaces.
func New(svc *stacks.Service, authz accessapi.Authorizer, spaces accessapi.Spaces) Server {
	return Server{stacks: svc, authz: authz, spaces: spaces}
}

// Stacks lists the stacks of the caller's account in the spaces it may
// read, by slug.
func (s Server) Stacks(
	ctx context.Context,
	req api.StacksRequestObject,
) (api.StacksResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := search.Input{First: req.Params.Limit}
	if req.Params.Cursor != nil {
		in.After = *req.Params.Cursor
	}
	res, err := s.stacks.List(ctx, p, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a bad cursor is a problem
	}
	all := make([]stacks.Stack, 0, len(res.Edges))
	for _, e := range res.Edges {
		all = append(all, e.Node)
	}
	views, err := s.views(ctx, p, all...)
	if err != nil {
		return nil, err
	}
	out := api.StackPage{Items: views}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return api.Stacks200JSONResponse{Body: out}, nil
}

// Stack answers one stack of the caller's account.
func (s Server) Stack(
	ctx context.Context,
	req api.StackRequestObject,
) (api.StackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.stacks.Stack(ctx, p.AccountID, req.StackId)
	if err != nil {
		return nil, stackError(err, req.StackId)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	return api.Stack200JSONResponse{Body: v}, nil
}

// StackCreate creates a stack in the space its input names.
func (s Server) StackCreate(
	ctx context.Context,
	req api.StackCreateRequestObject,
) (api.StackCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := inputOf(req.Body.Input)
	if err != nil {
		return nil, err
	}
	slug := ""
	if req.Body.Slug != nil {
		slug = *req.Body.Slug
	}
	st, err := s.stacks.Create(ctx, p, slug, req.Body.ManageState, in)
	if err != nil {
		return nil, stackError(err, slug)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	return api.StackCreate201JSONResponse{Body: v}, nil
}

// StackUpdate replaces a stack's settings with its input's.
func (s Server) StackUpdate(
	ctx context.Context,
	req api.StackUpdateRequestObject,
) (api.StackUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := inputOf(*req.Body)
	if err != nil {
		return nil, err
	}
	st, err := s.stacks.Update(ctx, p, req.StackId, in)
	if err != nil {
		return nil, stackError(err, req.StackId)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	return api.StackUpdate200JSONResponse{Body: v}, nil
}

// StackDelete deletes a stack at once, since it manages no resources, and
// answers it as it was, with the caller's access to it then.
func (s Server) StackDelete(
	ctx context.Context,
	req api.StackDeleteRequestObject,
) (api.StackDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.stacks.Delete(ctx, p, req.StackId)
	if err != nil {
		return nil, stackError(err, req.StackId)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	v.Deleted = true
	return api.StackDelete200JSONResponse{Body: v}, nil
}

// StackReslug gives a stack the slug its name derives.
func (s Server) StackReslug(
	ctx context.Context,
	req api.StackReslugRequestObject,
) (api.StackReslugResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (stacks.Stack, error) {
		return s.stacks.Reslug(ctx, p, req.StackId)
	})
	if err != nil {
		return nil, err
	}
	return api.StackReslug200JSONResponse{Body: st}, nil
}

// StackDisable disables a stack.
func (s Server) StackDisable(
	ctx context.Context,
	req api.StackDisableRequestObject,
) (api.StackDisableResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (stacks.Stack, error) {
		return s.stacks.SetDisabled(ctx, p, req.StackId, true)
	})
	if err != nil {
		return nil, err
	}
	return api.StackDisable200JSONResponse{Body: st}, nil
}

// StackEnable enables a stack.
func (s Server) StackEnable(
	ctx context.Context,
	req api.StackEnableRequestObject,
) (api.StackEnableResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (stacks.Stack, error) {
		return s.stacks.SetDisabled(ctx, p, req.StackId, false)
	})
	if err != nil {
		return nil, err
	}
	return api.StackEnable200JSONResponse{Body: st}, nil
}

// StackLock locks a stack for the caller.
func (s Server) StackLock(
	ctx context.Context,
	req api.StackLockRequestObject,
) (api.StackLockResponseObject, error) {
	note := ""
	if req.Body != nil && req.Body.Note != nil {
		note = *req.Body.Note
	}
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (stacks.Stack, error) {
		return s.stacks.Lock(ctx, p, req.StackId, note)
	})
	if err != nil {
		return nil, err
	}
	return api.StackLock200JSONResponse{Body: st}, nil
}

// StackUnlock unlocks a stack.
func (s Server) StackUnlock(
	ctx context.Context,
	req api.StackUnlockRequestObject,
) (api.StackUnlockResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (stacks.Stack, error) {
		return s.stacks.Unlock(ctx, p, req.StackId)
	})
	if err != nil {
		return nil, err
	}
	return api.StackUnlock200JSONResponse{Body: st}, nil
}

// StarSet stars or unstars a stack for the caller alone.
func (s Server) StarSet(
	ctx context.Context,
	req api.StarSetRequestObject,
) (api.StarSetResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (stacks.Stack, error) {
		return s.stacks.SetStar(ctx, p, req.StackId, req.Body.Star)
	})
	if err != nil {
		return nil, err
	}
	return api.StarSet200JSONResponse{Body: st}, nil
}

// change runs one of the service's changes to the stack slug for the
// caller and answers the stack after it.
func (s Server) change(
	ctx context.Context,
	slug string,
	f func(p kernel.Principal) (stacks.Stack, error),
) (api.Stack, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.Stack{}, err
	}
	st, err := f(p)
	if err != nil {
		return api.Stack{}, stackError(err, slug)
	}
	return s.view(ctx, p, st)
}

func (s Server) view(ctx context.Context, p kernel.Principal, st stacks.Stack) (api.Stack, error) {
	vs, err := s.views(ctx, p, st)
	if err != nil {
		return api.Stack{}, err
	}
	return vs[0], nil
}

// views are all as p sees them: the actions p holds in each one's space,
// whether p has starred it, and its space by slug.
func (s Server) views(
	ctx context.Context,
	p kernel.Principal,
	all ...stacks.Stack,
) ([]api.Stack, error) {
	out := make([]api.Stack, 0, len(all))
	if len(all) == 0 {
		return out, nil
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	starred, err := s.stacks.Starred(ctx, p, all)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	held := map[kernel.ULID][]accessapi.Action{}
	for _, st := range all {
		if _, ok := held[st.Space]; ok {
			continue
		}
		if held[st.Space], err = s.authz.Actions(ctx, p, st.Space); err != nil {
			return nil, err //nolint:wrapcheck // the snapshot's read names itself
		}
	}
	for _, st := range all {
		v, err := viewOf(st, slugs[st.Space], held[st.Space], starred[st.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
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

// stackError answers the service's refusals; slug is the stack a not
// found error names. A *kernel.Invalid is answered as it is.
func stackError(err error, slug string) error {
	if d, ok := errors.AsType[*stacks.Denied](err); ok {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", d.Action))
		p.Action = string(d.Action)
		return &problem.Error{Problem: p}
	}
	if l, ok := errors.AsType[*stacks.Locked](err); ok {
		p := problem.ResourceLocked.New(fmt.Sprintf("The stack is locked by %s.", l.By))
		p.LockedBy = l.By
		return &problem.Error{Problem: p}
	}
	if sp, ok := errors.AsType[*stacks.SpaceError](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No space %q in this account.", sp.Slug))
	}
	if t, ok := errors.AsType[*stacks.SlugTaken](err); ok {
		//nolint:wrapcheck // as above
		return problem.AlreadyExists.Error(
			fmt.Sprintf("Another stack of this account has the slug %q.", t.Slug))
	}
	if errors.Is(err, stacksapi.ErrNotFound) {
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No stack %q in this account.", slug))
	}
	return err
}

// inputOf is in as the service takes it. Its vendorConfig is encoded
// back to JSON, so the iac vendors check it as the request sent it.
func inputOf(in api.StackInput) (stacks.Input, error) {
	out := stacks.Input{
		Name: in.Name, Description: in.Description, Repository: in.Repository,
		RepositoryURL: in.RepositoryURL, Branch: in.Branch, Namespace: in.Namespace,
		ProjectRoot: in.ProjectRoot, RunnerImage: in.RunnerImage, WorkerPool: in.WorkerPool,
		VCSIntegration: in.VcsIntegrationId, TerraformVersion: in.TerraformVersion,
		AdditionalProjectGlobs: deref(in.AdditionalProjectGlobs),
		GitSparseCheckoutPaths: deref(in.GitSparseCheckoutPaths),
		Labels:                 deref(in.Labels),
		Autodeploy:             in.Autodeploy, Autoretry: in.Autoretry,
		ProtectFromDeletion: in.ProtectFromDeletion, Disabled: in.IsDisabled,
		LocalPreviewEnabled: in.LocalPreviewEnabled, GithubActionDeploy: in.GithubActionDeploy,
		EnableSensitiveOutputUpload:  in.EnableSensitiveOutputUpload,
		EnableWellKnownSecretMasking: in.EnableWellKnownSecretMasking,
		SkipVendorBinaryDownload:     in.SkipVendorBinaryDownload,
		Hooks: map[string][]string{
			stacksapi.BeforeInit: deref(in.BeforeInit),
			stacksapi.AfterInit:  deref(in.AfterInit),
			stacksapi.BeforePlan: deref(in.BeforePlan),
			stacksapi.AfterPlan:  deref(in.AfterPlan),
			stacksapi.BeforeApply: deref(
				in.BeforeApply,
			),
			stacksapi.AfterApply:    deref(in.AfterApply),
			stacksapi.BeforeDestroy: deref(in.BeforeDestroy),
			stacksapi.AfterDestroy:  deref(in.AfterDestroy),
			stacksapi.BeforePerform: deref(in.BeforePerform),
			stacksapi.AfterPerform:  deref(in.AfterPerform),
			stacksapi.AfterRun:      deref(in.AfterRun),
		},
	}
	if in.Space != nil {
		out.Space = *in.Space
	}
	if in.Provider != nil {
		provider := string(*in.Provider)
		out.Provider = &provider
	}
	if in.VendorConfig != nil {
		raw, err := json.Marshal(in.VendorConfig)
		if err != nil {
			return stacks.Input{}, fmt.Errorf("stacks: encode vendorConfig: %w", err)
		}
		out.VendorConfig = raw
	}
	return out, nil
}

func deref(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}
