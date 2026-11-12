package http

import (
	"context"
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// WorkerDrainSet drains or undrains a worker of a pool and answers it.
func (s Server) WorkerDrainSet(
	ctx context.Context,
	req api.WorkerDrainSetRequestObject,
) (api.WorkerDrainSetResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.workers.DrainSet(ctx, p.AccountID, req.WorkerPoolId, req.WorkerId, req.Body.Drain)
	if err != nil {
		return nil, poolError(err)
	}
	return api.WorkerDrainSet200JSONResponse{Body: workerView(w)}, nil
}

// WorkerPoolCycle asks every worker of a pool to exit, each when it is
// idle.
func (s Server) WorkerPoolCycle(
	ctx context.Context,
	req api.WorkerPoolCycleRequestObject,
) (api.WorkerPoolCycleResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.workers.Cycle(ctx, p.AccountID, req.WorkerPoolId); err != nil {
		return nil, poolError(err)
	}
	return api.WorkerPoolCycle200JSONResponse{Body: true}, nil
}

// WorkerPoolSearchWorkers searches a pool's workers.
func (s Server) WorkerPoolSearchWorkers(
	ctx context.Context,
	req api.WorkerPoolSearchWorkersRequestObject,
) (api.WorkerPoolSearchWorkersResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.workers.Search(ctx, p.AccountID, req.WorkerPoolId,
		apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, poolError(err)
	}
	out := api.SearchWorkersOutput{
		Edges:    make([]api.WorkerConnection, 0, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(
			out.Edges,
			api.WorkerConnection{Cursor: e.Cursor, Node: workerView(e.Node)},
		)
	}
	return api.WorkerPoolSearchWorkers200JSONResponse{Body: out}, nil
}

// WorkerPoolSearchWorkersSuggestions lists the values a pool's workers
// hold.
func (s Server) WorkerPoolSearchWorkersSuggestions(
	ctx context.Context,
	req api.WorkerPoolSearchWorkersSuggestionsRequestObject,
) (api.WorkerPoolSearchWorkersSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.workers.Suggest(ctx, p.AccountID, req.WorkerPoolId,
		apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, poolError(err)
	}
	return api.WorkerPoolSearchWorkersSuggestions200JSONResponse{
		Body: apisearch.Suggestions(got),
	}, nil
}

// workerView is w as the API answers it; its metadata is a JSON object
// of strings.
func workerView(w worker.Worker) api.Worker {
	v := api.Worker{
		Id: w.ID.String(), Busy: w.Busy, Drained: w.Drained, Status: api.WorkerStatus(w.Status()),
		CreatedAt: unix(w.CreatedAt.Unix()),
	}
	if w.AvailableAt != nil {
		at := unix(w.AvailableAt.Unix())
		v.AvailableAt = &at
	}
	if len(w.Metadata) > 0 {
		// A map of strings always marshals.
		b, _ := json.Marshal(w.Metadata) //nolint:errchkjson // as above
		md := string(b)
		v.Metadata = &md
	}
	return v
}
