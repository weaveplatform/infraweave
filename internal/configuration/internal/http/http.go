package http

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// Server is the configuration domain's partial of the API's strict
// server. The route table has decided each operation's action on the
// context's space, the stack's for an attachment, or on the space a
// create or a space search names, before its handler runs, and answered
// 404 for one the caller cannot read.
type Server struct {
	svc    *core.Service
	authz  accessapi.Authorizer
	spaces accessapi.Spaces
}

// New returns the partial over the service, deciding allowedActions with
// authz and naming spaces through spaces.
func New(svc *core.Service, authz accessapi.Authorizer, spaces accessapi.Spaces) Server {
	return Server{svc: svc, authz: authz, spaces: spaces}
}

// Contexts lists the contexts of the caller's account in the spaces it may
// read, by slug.
func (s Server) Contexts(
	ctx context.Context,
	req api.ContextsRequestObject,
) (api.ContextsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Search(ctx, p, page(req.Params.Cursor, req.Params.Limit))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a bad cursor is a problem
	}
	out, err := s.page(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.Contexts200JSONResponse{Body: out}, nil
}

// AttachableContextsInSpace lists the contexts a stack in the space may
// attach that the caller may read, by slug.
func (s Server) AttachableContextsInSpace(
	ctx context.Context,
	req api.AttachableContextsInSpaceRequestObject,
) (api.AttachableContextsInSpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, _, err := s.svc.Attachable(ctx, p, req.Params.Id,
		page(req.Params.Cursor, req.Params.Limit))
	if err != nil {
		return nil, contextError(err)
	}
	out, err := s.page(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.AttachableContextsInSpace200JSONResponse{Body: out}, nil
}

// SearchContexts searches the contexts of the caller's account in the
// spaces it may read.
func (s Server) SearchContexts(
	ctx context.Context,
	req api.SearchContextsRequestObject,
) (api.SearchContextsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Search(ctx, p, apisearch.Input(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchContextsOutput{
		Edges: make([]api.ContextsConnection, 0, len(res.Edges)), PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.ContextsConnection{
			Cursor: e.Cursor, Node: api.ContextRef{Id: e.Node.Slug},
		})
	}
	return api.SearchContexts200JSONResponse{Body: out}, nil
}

// SearchContextsSuggestions lists the values the contexts of the caller's
// account in the spaces it may read hold.
func (s Server) SearchContextsSuggestions(
	ctx context.Context,
	req api.SearchContextsSuggestionsRequestObject,
) (api.SearchContextsSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.svc.Suggest(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.SearchContextsSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
}

// SearchSpaceContexts searches the contexts a stack in the space may
// attach, marking those of the spaces it inherits from.
func (s Server) SearchSpaceContexts(
	ctx context.Context,
	req api.SearchSpaceContextsRequestObject,
) (api.SearchSpaceContextsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, space, err := s.svc.Attachable(ctx, p, req.Body.Space, apisearch.Input(req.Body.Input))
	if err != nil {
		return nil, contextError(err)
	}
	out := api.SearchSpaceContextsOutput{
		Edges:    make([]api.SpaceContextConnection, 0, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.SpaceContextConnection{
			Cursor:    e.Cursor,
			Node:      api.ContextRef{Id: e.Node.Slug},
			Inherited: e.Node.Space != space,
		})
	}
	return api.SearchSpaceContexts200JSONResponse{Body: out}, nil
}

// SearchSpaceContextsSuggestions lists the values the contexts a stack in
// the space may attach hold.
func (s Server) SearchSpaceContextsSuggestions(
	ctx context.Context,
	req api.SearchSpaceContextsSuggestionsRequestObject,
) (api.SearchSpaceContextsSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.svc.AttachableSuggest(ctx, p, req.Body.Space,
		apisearch.SuggestionsInput(req.Body.Input))
	if err != nil {
		return nil, contextError(err)
	}
	return api.SearchSpaceContextsSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
}

// Context answers one context of the caller's account.
func (s Server) Context(
	ctx context.Context,
	req api.ContextRequestObject,
) (api.ContextResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.svc.Context(ctx, p.AccountID, req.ContextId)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.view(ctx, p, c)
	if err != nil {
		return nil, err
	}
	return api.Context200JSONResponse{
		Body:    v,
		Headers: api.Context200ResponseHeaders{ETag: etag(c)},
	}, nil
}

// ContextCreateV2 creates a context in the space its input names.
func (s Server) ContextCreateV2(
	ctx context.Context,
	req api.ContextCreateV2RequestObject,
) (api.ContextCreateV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.svc.Create(ctx, p, inputOf(*req.Body))
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.view(ctx, p, c)
	if err != nil {
		return nil, err
	}
	return api.ContextCreateV2201JSONResponse{Body: v}, nil
}

// ContextUpdateV2 replaces a context's fields with its input's, and its
// config elements when replaceConfigElements is true.
func (s Server) ContextUpdateV2(
	ctx context.Context,
	req api.ContextUpdateV2RequestObject,
) (api.ContextUpdateV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.update(ctx, p, req.ContextId, strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.view(ctx, p, c)
	if err != nil {
		return nil, err
	}
	return api.ContextUpdateV2200JSONResponse{
		Body: v, Headers: api.ContextUpdateV2200ResponseHeaders{ETag: etag(c)},
	}, nil
}

func (s Server) update(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
	body api.ContextUpdateV2Request,
) (core.Context, error) {
	replace := body.ReplaceConfigElements != nil && *body.ReplaceConfigElements
	c, err := s.svc.Update(ctx, p, slug, expect, inputOf(body.Input), replace)
	return c, err //nolint:wrapcheck // answered by contextError
}

// ContextPatch changes the fields its merge patch names and keeps the
// rest, writing the result as ContextUpdateV2 writes its body. The view
// it patches leaves the config elements out, so a patch replaces them
// only when it names both configAttachments and replaceConfigElements.
func (s Server) ContextPatch(
	ctx context.Context,
	req api.ContextPatchRequestObject,
) (api.ContextPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.ContextUpdateV2Request, int64, error) {
		c, err := s.svc.Context(ctx, p.AccountID, req.ContextId)
		if err != nil {
			return api.ContextUpdateV2Request{}, 0, err //nolint:wrapcheck // answered by contextError
		}
		slugs, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.ContextUpdateV2Request{}, 0, err //nolint:wrapcheck // the access domain names it
		}
		return api.ContextUpdateV2Request{Input: inputView(c, slugs[c.Space])}, c.Version, nil
	}
	write := func(body api.ContextUpdateV2Request, expect kernel.Expect) (core.Context, error) {
		return s.update(ctx, p, req.ContextId, expect, body)
	}
	c, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.view(ctx, p, c)
	if err != nil {
		return nil, err
	}
	return api.ContextPatch200JSONResponse{
		Body: v, Headers: api.ContextPatch200ResponseHeaders{ETag: etag(c)},
	}, nil
}

// ContextDelete deletes a context and answers it as it was, with the
// caller's access to it then.
func (s Server) ContextDelete(
	ctx context.Context,
	req api.ContextDeleteRequestObject,
) (api.ContextDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	// The view reads the context's elements and attachments, which the
	// delete takes with it, so it is read first.
	c, err := s.svc.Context(ctx, p.AccountID, req.ContextId)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.view(ctx, p, c)
	if err != nil {
		return nil, err
	}
	force := req.Params.Force != nil && *req.Params.Force
	if _, err := s.svc.Delete(
		ctx,
		p,
		req.ContextId,
		strict.IfMatch(req.Params.IfMatch),
		force,
	); err != nil {
		return nil, contextError(err)
	}
	return api.ContextDelete200JSONResponse{Body: v}, nil
}

// ContextAttachedStack answers one of a context's attachments.
func (s Server) ContextAttachedStack(
	ctx context.Context,
	req api.ContextAttachedStackRequestObject,
) (api.ContextAttachedStackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.AttachedStack(ctx, p.AccountID, req.ContextId, req.Params.Id)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.attachment(ctx, p, a, st)
	if err != nil {
		return nil, err
	}
	return api.ContextAttachedStack200JSONResponse{Body: v}, nil
}

// ContextConfigElement answers one of a context's config elements.
func (s Server) ContextConfigElement(
	ctx context.Context,
	req api.ContextConfigElementRequestObject,
) (api.ContextConfigElementResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	e, err := s.svc.Element(ctx, p.AccountID, req.ContextId, req.Params.Id)
	if err != nil {
		return nil, elementError(err, req.Params.Id, "context")
	}
	return api.ContextConfigElement200JSONResponse{Body: elementView(e)}, nil
}

// ContextConfigAdd creates or replaces the config element its body names.
func (s Server) ContextConfigAdd(
	ctx context.Context,
	req api.ContextConfigAddRequestObject,
) (api.ContextConfigAddResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	e, created, err := s.svc.SetElement(ctx, p, req.ContextId, core.ElementInput{
		Name: b.Id, FileMount: b.Type == api.FILEMOUNT, Value: &b.Value, WriteOnly: b.WriteOnly,
		Description: b.Description, FileMode: b.FileMode,
	}, "", "/id")
	if err != nil {
		return nil, contextError(err)
	}
	if created {
		return api.ContextConfigAdd201JSONResponse{Body: elementView(e)}, nil
	}
	return api.ContextConfigAdd200JSONResponse{Body: elementView(e)}, nil
}

// ContextConfigSet creates or replaces the config element configId.
func (s Server) ContextConfigSet(
	ctx context.Context,
	req api.ContextConfigSetRequestObject,
) (api.ContextConfigSetResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	e, created, err := s.svc.SetElement(ctx, p, req.ContextId, core.ElementInput{
		Name: req.ConfigId, FileMount: b.Type == api.FILEMOUNT, Value: b.Value,
		WriteOnly: b.WriteOnly, Description: b.Description, FileMode: b.FileMode,
	}, "", "/configId")
	if err != nil {
		return nil, contextError(err)
	}
	if created {
		return api.ContextConfigSet201JSONResponse{Body: elementView(e)}, nil
	}
	return api.ContextConfigSet200JSONResponse{Body: elementView(e)}, nil
}

// ContextConfigDelete deletes a context's config element and answers it as
// it was.
func (s Server) ContextConfigDelete(
	ctx context.Context,
	req api.ContextConfigDeleteRequestObject,
) (api.ContextConfigDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	e, err := s.svc.DeleteElement(ctx, p, req.ContextId, req.ConfigId)
	if err != nil {
		return nil, elementError(err, req.ConfigId, "context")
	}
	return api.ContextConfigDelete200JSONResponse{Body: elementView(e)}, nil
}

// ContextAttach attaches a context to a stack, or changes the priority of
// its attachment.
func (s Server) ContextAttach(
	ctx context.Context,
	req api.ContextAttachRequestObject,
) (api.ContextAttachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.Attach(ctx, p, req.ContextId, req.StackId, req.Body.Priority)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.attachment(ctx, p, a, st)
	if err != nil {
		return nil, err
	}
	return api.ContextAttach200JSONResponse{Body: v}, nil
}

// ContextDetach detaches a context from a stack and answers the
// attachment as it was.
func (s Server) ContextDetach(
	ctx context.Context,
	req api.ContextDetachRequestObject,
) (api.ContextDetachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.Detach(ctx, p, req.AttachmentId)
	if err != nil {
		return nil, contextError(err)
	}
	v, err := s.attachment(ctx, p, a, st)
	if err != nil {
		return nil, err
	}
	return api.ContextDetach200JSONResponse{Body: v}, nil
}

// page is a list's cursor and limit as a search's input.
func page(cursor *api.Cursor, limit *api.Limit) search.Input {
	in := search.Input{First: limit}
	if cursor != nil {
		in.After = *cursor
	}
	return in
}

// page is res as a page of views.
func (s Server) page(
	ctx context.Context,
	p kernel.Principal,
	res search.Result[core.Context],
) (api.ContextPage, error) {
	all := make([]core.Context, 0, len(res.Edges))
	for _, e := range res.Edges {
		all = append(all, e.Node)
	}
	views, err := s.views(ctx, p, all...)
	if err != nil {
		return api.ContextPage{}, err
	}
	out := api.ContextPage{Items: views}
	if res.HasNext {
		out.NextCursor = &res.End
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

// contextError answers the service's refusals. A *kernel.Invalid and a
// stale If-Match are answered as they are.
func contextError(err error) error {
	if d, ok := errors.AsType[*core.Denied](err); ok {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", d.Action))
		p.Action = string(d.Action)
		return &problem.Error{Problem: p}
	}
	if m, ok := errors.AsType[*core.Missing](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No %s %q in this account.", m.Kind, m.ID))
	}
	if t, ok := errors.AsType[*core.SlugTaken](err); ok {
		//nolint:wrapcheck // as above
		return problem.AlreadyExists.Error(
			fmt.Sprintf("Another context of this account has the slug %q.", t.Slug))
	}
	if errors.Is(err, core.ErrNotFound) {
		//nolint:wrapcheck // as above
		return problem.NotFound.Error("No such context in this account.")
	}
	return err
}

// elementError answers a missing element by its name and the kind of its
// owner: the route has found the owner already.
func elementError(err error, name, owner string) error {
	if errors.Is(err, core.ErrNotFound) {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No config element %q in this %s.", name, owner))
	}
	return contextError(err)
}
