package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// StackAttachableContexts lists the contexts the stack may attach that
// the caller may read, by slug.
func (s Server) StackAttachableContexts(
	ctx context.Context,
	req api.StackAttachableContextsRequestObject,
) (api.StackAttachableContextsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.StackAttachable(
		ctx,
		p,
		req.StackId,
		page(req.Params.Cursor, req.Params.Limit),
	)
	if err != nil {
		return nil, contextError(err)
	}
	out, err := s.page(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.StackAttachableContexts200JSONResponse{Body: out}, nil
}

// StackConfigElement reads a stack's config element by its ID.
func (s Server) StackConfigElement(
	ctx context.Context,
	req api.StackConfigElementRequestObject,
) (api.StackConfigElementResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	e, err := s.svc.StackElement(ctx, p.AccountID, req.StackId, req.Params.Id)
	if err != nil {
		return nil, elementError(err, req.Params.Id, "stack")
	}
	return api.StackConfigElement200JSONResponse{Body: elementView(e)}, nil
}

// StackConfigAdd creates or replaces the stack's config element its body
// names.
func (s Server) StackConfigAdd(
	ctx context.Context,
	req api.StackConfigAddRequestObject,
) (api.StackConfigAddResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	e, created, err := s.svc.SetStackElement(ctx, p, req.StackId, core.ElementInput{
		Name: b.Id, FileMount: b.Type == api.FILEMOUNT, Value: &b.Value, WriteOnly: b.WriteOnly,
		Description: b.Description, FileMode: b.FileMode,
	}, "", "/id")
	if err != nil {
		return nil, contextError(err)
	}
	if created {
		return api.StackConfigAdd201JSONResponse{Body: elementView(e)}, nil
	}
	return api.StackConfigAdd200JSONResponse{Body: elementView(e)}, nil
}

// StackConfigSet creates or replaces the stack's config element configId.
func (s Server) StackConfigSet(
	ctx context.Context,
	req api.StackConfigSetRequestObject,
) (api.StackConfigSetResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	e, created, err := s.svc.SetStackElement(ctx, p, req.StackId, core.ElementInput{
		Name: req.ConfigId, FileMount: b.Type == api.FILEMOUNT, Value: b.Value,
		WriteOnly: b.WriteOnly, Description: b.Description, FileMode: b.FileMode,
	}, "", "/configId")
	if err != nil {
		return nil, contextError(err)
	}
	if created {
		return api.StackConfigSet201JSONResponse{Body: elementView(e)}, nil
	}
	return api.StackConfigSet200JSONResponse{Body: elementView(e)}, nil
}

// StackConfigDelete deletes a stack's config element and answers it as it
// was.
func (s Server) StackConfigDelete(
	ctx context.Context,
	req api.StackConfigDeleteRequestObject,
) (api.StackConfigDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	e, err := s.svc.DeleteStackElement(ctx, p, req.StackId, req.ConfigId)
	if err != nil {
		return nil, elementError(err, req.ConfigId, "stack")
	}
	return api.StackConfigDelete200JSONResponse{Body: elementView(e)}, nil
}
