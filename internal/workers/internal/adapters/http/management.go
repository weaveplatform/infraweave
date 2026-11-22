package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// WorkerPoolManaged recovers the exact resource's pool and public token after
// response loss without a name search or a new creation attempt.
func (s Server) WorkerPoolManaged(ctx context.Context, req api.WorkerPoolManagedRequestObject) (api.WorkerPoolManagedResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	owner := kernel.WorkerOwner{ClusterID: req.Params.ClusterID, Namespace: req.Params.Namespace, ResourceUID: req.Params.ResourceUID}
	wp, err := s.svc.Managed(ctx, p, owner)
	if err != nil {
		return nil, poolError(err)
	}
	out, err := s.view(ctx, p, wp)
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolManaged200JSONResponse{Body: out, Headers: api.WorkerPoolManaged200ResponseHeaders{ETag: etag(wp)}}, nil
}

func ownerInput(in *api.WorkerPoolManagement) *kernel.WorkerOwner {
	if in == nil {
		return nil
	}
	return &kernel.WorkerOwner{ClusterID: in.ClusterID, Namespace: in.Namespace, ResourceUID: in.ResourceUID}
}

func ownerView(in *kernel.WorkerOwner) *api.WorkerPoolManagement {
	if in == nil {
		return nil
	}
	return &api.WorkerPoolManagement{ClusterID: in.ClusterID, Namespace: in.Namespace, ResourceUID: in.ResourceUID}
}
