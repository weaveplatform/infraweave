package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// Server is the stacks domain's partial of the API's strict server. The
// route table has decided each operation's action on the stack's space,
// or on the space a create names, before its handler runs, and answered
// 404 for a stack the caller cannot read.
type Server struct {
	stacks *core.Service
	authz  accessapi.Authorizer
	spaces accessapi.Spaces
	edges  *edges
}

// New returns the partial over the service, deciding allowedActions with
// authz and naming spaces through spaces. Its stacks have no configuration
// until BindConfiguration binds a reader, and no blocker until
// SetStackStatus binds one.
func New(svc *core.Service, authz accessapi.Authorizer, spaces accessapi.Spaces) Server {
	return Server{
		stacks: svc, authz: authz, spaces: spaces,
		edges: &edges{config: unbound{}, status: unbound{}},
	}
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
	res, err := s.stacks.Search(ctx, p, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a bad cursor is a problem
	}
	all := make([]core.Stack, 0, len(res.Edges))
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

// SearchStacks searches the stacks of the caller's account in the spaces
// it may read.
func (s Server) SearchStacks(
	ctx context.Context,
	req api.SearchStacksRequestObject,
) (api.SearchStacksResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.stacks.Search(ctx, p, apisearch.Input(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchStacksOutput{
		Edges: make([]api.StackConnection, 0, len(res.Edges)), PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.StackConnection{
			Cursor: e.Cursor, Node: api.StackRef{Id: e.Node.Slug},
		})
	}
	return api.SearchStacks200JSONResponse{Body: out}, nil
}

// SearchStacksSuggestions lists the values the stacks of the caller's
// account in the spaces it may read hold.
func (s Server) SearchStacksSuggestions(
	ctx context.Context,
	req api.SearchStacksSuggestionsRequestObject,
) (api.SearchStacksSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.stacks.Suggest(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.SearchStacksSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
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
	return api.Stack200JSONResponse{
		Body: v, Headers: api.Stack200ResponseHeaders{ETag: etag(st)},
	}, nil
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
	st, err := s.stacks.Update(ctx, p, req.StackId, strict.IfMatch(req.Params.IfMatch), in)
	if err != nil {
		return nil, stackError(err, req.StackId)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	return api.StackUpdate200JSONResponse{
		Body: v, Headers: api.StackUpdate200ResponseHeaders{ETag: etag(st)},
	}, nil
}

// StackPatch changes the settings its merge patch names and keeps the
// rest, writing the result as StackUpdate writes its input.
func (s Server) StackPatch(
	ctx context.Context,
	req api.StackPatchRequestObject,
) (api.StackPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.StackInput, int64, error) {
		st, err := s.stacks.Stack(ctx, p.AccountID, req.StackId)
		if err != nil {
			return api.StackInput{}, 0, err //nolint:wrapcheck // answered by stackError below
		}
		slugs, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.StackInput{}, 0, err //nolint:wrapcheck // the access domain names the read
		}
		in, err := inputView(st, slugs[st.Space])
		return in, st.Version, err
	}
	write := func(view api.StackInput, expect kernel.Expect) (core.Stack, error) {
		in, err := inputOf(view)
		if err != nil {
			return core.Stack{}, err
		}
		return s.stacks.Update(ctx, p, req.StackId, expect, in) //nolint:wrapcheck // as above
	}
	st, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, stackError(err, req.StackId)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	return api.StackPatch200JSONResponse{
		Body: v, Headers: api.StackPatch200ResponseHeaders{ETag: etag(st)},
	}, nil
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
	st, err := s.stacks.Delete(ctx, p, req.StackId, strict.IfMatch(req.Params.IfMatch))
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
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
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
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
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
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
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
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
		return s.stacks.Lock(ctx, p, req.StackId, note)
	})
	if err != nil {
		return nil, err
	}
	return api.StackLock200JSONResponse{Body: st}, nil
}

// StackSyncCommit sets a stack's branch head and tracked commit to its
// branch's head.
func (s Server) StackSyncCommit(
	ctx context.Context,
	req api.StackSyncCommitRequestObject,
) (api.StackSyncCommitResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
		return s.stacks.SyncCommit(ctx, p, req.StackId)
	})
	if err != nil {
		return nil, err
	}
	return api.StackSyncCommit200JSONResponse{Body: st}, nil
}

// StackSetCurrentCommit sets a stack's tracked commit to a commit of its
// repository.
func (s Server) StackSetCurrentCommit(
	ctx context.Context,
	req api.StackSetCurrentCommitRequestObject,
) (api.StackSetCurrentCommitResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
		return s.stacks.SetCurrentCommit(ctx, p, req.StackId, req.Body.Sha)
	})
	if err != nil {
		return nil, err
	}
	return api.StackSetCurrentCommit200JSONResponse{Body: st}, nil
}

// StackUnlock unlocks a stack.
func (s Server) StackUnlock(
	ctx context.Context,
	req api.StackUnlockRequestObject,
) (api.StackUnlockResponseObject, error) {
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
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
	st, err := s.change(ctx, req.StackId, func(p kernel.Principal) (core.Stack, error) {
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
	f func(p kernel.Principal) (core.Stack, error),
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

func (s Server) view(ctx context.Context, p kernel.Principal, st core.Stack) (api.Stack, error) {
	vs, err := s.views(ctx, p, st)
	if err != nil {
		return api.Stack{}, err
	}
	return vs[0], nil
}

// views are all as p sees them: the actions p holds in each one's space,
// whether p has starred it, its space by slug, and its attached contexts
// and config elements, read for all at once.
func (s Server) views(
	ctx context.Context,
	p kernel.Principal,
	all ...core.Stack,
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
	ids := make([]kernel.ULID, 0, len(all))
	for _, st := range all {
		ids = append(ids, st.ID)
		if _, ok := held[st.Space]; ok {
			continue
		}
		if held[st.Space], err = s.authz.Actions(ctx, p, st.Space); err != nil {
			return nil, err //nolint:wrapcheck // the snapshot's read names itself
		}
	}
	attached, err := s.edges.config.AttachedContexts(ctx, p.AccountID, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the configuration domain names the read
	}
	config, err := s.edges.config.Config(ctx, p.AccountID, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	blockers, err := s.edges.status.Blockers(ctx, p.AccountID, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the runs domain names the read
	}
	for _, st := range all {
		v, err := viewOf(st, slugs[st.Space], held[st.Space], starred[st.ID])
		if err != nil {
			return nil, err
		}
		v.AttachedContexts = make([]api.StackContextAttachment, 0, len(attached[st.ID]))
		for _, c := range attached[st.ID] {
			v.AttachedContexts = append(v.AttachedContexts, contextView(c))
		}
		v.Config = elementViews(config[st.ID])
		if run, ok := blockers[st.ID]; ok {
			v.Blocker = &api.RunRef{Id: run.String()}
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
	if d, ok := errors.AsType[*core.Denied](err); ok {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", d.Action))
		p.Action = string(d.Action)
		return &problem.Error{Problem: p}
	}
	if l, ok := errors.AsType[*core.Locked](err); ok {
		p := problem.ResourceLocked.New(fmt.Sprintf("The stack is locked by %s.", l.By))
		p.LockedBy = l.By
		return &problem.Error{Problem: p}
	}
	if sp, ok := errors.AsType[*core.SpaceError](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No space %q in this account.", sp.Slug))
	}
	if t, ok := errors.AsType[*core.SlugTaken](err); ok {
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
func inputOf(in api.StackInput) (core.Input, error) {
	out := core.Input{
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
			return core.Input{}, fmt.Errorf("stacks: encode vendorConfig: %w", err)
		}
		out.VendorConfig = raw
	}
	return out, nil
}

// inputView is st's settings as the StackInput a PUT would send to keep
// them, with its space by slug: the document a merge patch applies to.
// A Terraform stack with no vendor settings sends no vendorConfig, so a
// patch that names another vendor does not also have to null Terraform.
func inputView(st core.Stack, space string) (api.StackInput, error) {
	in := api.StackInput{
		Name: st.Name, Description: st.Description, Repository: st.Repository,
		RepositoryURL: st.RepositoryURL, Branch: st.Branch, Space: &space,
		Namespace: &st.Namespace, ProjectRoot: st.ProjectRoot, RunnerImage: st.RunnerImage,
		TerraformVersion:             st.TerraformVersion,
		AdditionalProjectGlobs:       &st.AdditionalProjectGlobs,
		GitSparseCheckoutPaths:       sparse(st.GitSparseCheckoutPaths),
		Labels:                       &st.Labels,
		Autodeploy:                   &st.Autodeploy,
		Autoretry:                    &st.Autoretry,
		ProtectFromDeletion:          &st.ProtectFromDeletion,
		IsDisabled:                   &st.Disabled,
		LocalPreviewEnabled:          &st.LocalPreviewEnabled,
		GithubActionDeploy:           &st.GithubActionDeploy,
		EnableSensitiveOutputUpload:  &st.EnableSensitiveOutputUpload,
		EnableWellKnownSecretMasking: &st.EnableWellKnownSecretMasking,
		SkipVendorBinaryDownload:     &st.SkipVendorBinaryDownload,
	}
	provider := api.VCSProvider(st.Provider)
	in.Provider = &provider
	h := hooksOf(st.Hooks)
	in.BeforeInit, in.AfterInit = &h.BeforeInit, &h.AfterInit
	in.BeforePlan, in.AfterPlan = &h.BeforePlan, &h.AfterPlan
	in.BeforeApply, in.AfterApply = &h.BeforeApply, &h.AfterApply
	in.BeforeDestroy, in.AfterDestroy = &h.BeforeDestroy, &h.AfterDestroy
	in.BeforePerform, in.AfterPerform = &h.BeforePerform, &h.AfterPerform
	in.AfterRun = &h.AfterRun
	if st.Vendor == string(iac.Terraform) && string(st.VendorConfig) == "{}" {
		return in, nil
	}
	raw, err := json.Marshal(map[string]json.RawMessage{st.Vendor: st.VendorConfig})
	if err != nil {
		return api.StackInput{}, fmt.Errorf("stacks: vendorConfig of %s: %w", st.Slug, err)
	}
	var vendor api.StackConfigVendorInput
	if err := json.Unmarshal(raw, &vendor); err != nil {
		return api.StackInput{}, fmt.Errorf("stacks: vendorConfig of %s: %w", st.Slug, err)
	}
	in.VendorConfig = &vendor
	return in, nil
}

// etag is st's entity tag, which an If-Match names to change it.
func etag(st core.Stack) *string {
	t := strict.ETag(st.Version)
	return &t
}

func deref(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}
