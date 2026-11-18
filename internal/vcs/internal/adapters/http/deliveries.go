package http

import (
	"context"
	"errors"
	"math"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

// WithDeliveries attaches the admin surface to the same durable inbox service.
func (s Server) WithDeliveries(deliveries *intake.Service) Server {
	s.deliveries = deliveries
	return s
}

//nolint:wrapcheck // shared problems are returned as the HTTP boundary
func (s Server) deliveryAdmin(ctx context.Context, id string) (kernel.Principal, kernel.ULID, error) {
	p, err := principal(ctx)
	if err != nil {
		return p, kernel.ULID{}, err
	}
	in, admin, err := s.svc.Read(ctx, p, id, "")
	if err != nil {
		return p, kernel.ULID{}, integrationError(err)
	}
	if !admin {
		return p, kernel.ULID{}, problem.Forbidden.Error("The integration requires space administration.")
	}
	if s.deliveries == nil {
		return p, kernel.ULID{}, kernel.ErrUnavailable
	}
	return p, in.ID, nil
}

// VcsIntegrationDeliveries answers metadata, with fixed newest-first paging.
func (s Server) VcsIntegrationDeliveries(ctx context.Context, req api.VcsIntegrationDeliveriesRequestObject) (api.VcsIntegrationDeliveriesResponseObject, error) {
	p, id, err := s.deliveryAdmin(ctx, req.VcsIntegrationId)
	if err != nil {
		return nil, err
	}
	in := search.Input{First: req.Params.Input.First}
	if req.Params.Input.After != nil {
		in.After = *req.Params.Input.After
	}
	if req.Params.Input.RequestedPage != nil {
		in.Page = search.Page(*req.Params.Input.RequestedPage)
	}
	got, err := s.deliveries.List(ctx, p.AccountID, id, in)
	if err != nil {
		return nil, deliveryError(err)
	}
	out := api.VCSDeliveriesOutput{Edges: make([]api.VCSDeliveryConnection, len(got.Edges)), PageInfo: apisearch.PageInfo(got)}
	for i, e := range got.Edges {
		out.Edges[i] = api.VCSDeliveryConnection{Cursor: e.Cursor, Node: deliveryView(e.Node)}
	}
	return api.VcsIntegrationDeliveries200JSONResponse{Body: out}, nil
}

// VcsIntegrationDeliveryReplay returns the reset delivery after commit.
func (s Server) VcsIntegrationDeliveryReplay(ctx context.Context, req api.VcsIntegrationDeliveryReplayRequestObject) (api.VcsIntegrationDeliveryReplayResponseObject, error) {
	p, integration, err := s.deliveryAdmin(ctx, req.VcsIntegrationId)
	if err != nil {
		return nil, err
	}
	id, err := kernel.ParseULID(req.DeliveryId)
	if err != nil {
		return nil, deliveryError(intake.ErrNotFound)
	}
	d, err := s.deliveries.Replay(ctx, p, integration, id, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, deliveryError(err)
	}
	return api.VcsIntegrationDeliveryReplay202JSONResponse{Body: deliveryView(d)}, nil
}

func deliveryView(d intake.Delivery) api.VCSDelivery {
	return api.VCSDelivery{
		Id: d.ID.String(), DeliveryId: d.DeliveryID, Event: d.Event, ReceivedAt: d.ReceivedAt,
		State: api.VCSDeliveryState(strings.ToUpper(d.State)), LastError: d.LastError, RunsCreated: int32(max(0, min(d.RunsCreated, math.MaxInt32))),
	} //nolint:gosec // clamped to the served int32 range
}

//nolint:wrapcheck // typed domain refusals become shared HTTP problems
func deliveryError(err error) error {
	if errors.Is(err, intake.ErrNotFound) {
		return problem.NotFound.Error("No such delivery for this integration.")
	}
	if conflict, ok := errors.AsType[*intake.ReplayConflict](err); ok {
		return problem.InvalidState.Error(conflict.Detail)
	}
	return err
}
