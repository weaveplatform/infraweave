package http

import (
	"context"
	"math"
	"slices"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/sample"
)

// Quotas is what the projections read of the features domain: an
// account's effective value of a limit.
type Quotas interface {
	Quota(ctx context.Context, org kernel.OrganizationID, acct kernel.AccountID, limit string) (featuresapi.Quota, error)
}

// Options are what the pool projections read beyond New's services:
// Quotas, the account's public and private worker limits; PublicEnabled,
// worker.public_pool.enabled; Egress, worker.public_pool.outgoing_ip_addresses;
// and Samples, whose retention bounds the usage cycles.
type Options struct {
	Quotas        Quotas
	PublicEnabled bool
	Egress        []string
	Samples       *sample.Service
}

// projections are the options and the other domains' reads the pool
// projections count with, the latter bound after the partial is built.
type projections struct {
	Options
	queues workersapi.Queues
	users  workersapi.Users
}

// WithOptions is s reading o.
func (s Server) WithOptions(o Options) Server {
	s.proj = &projections{Options: o}
	return s
}

// Bind binds the runs domain's queue counts and the stacks domain's pool
// users, which the pool projections count with; it runs during the
// application's construction, before the partial serves a request.
func (s Server) Bind(q workersapi.Queues, u workersapi.Users) {
	s.proj.queues, s.proj.users = q, u
}

// PublicWorkerPool answers the public pool as the caller's account uses
// it: its quota, and its runs and stacks in the spaces the caller may
// read. The installation's workers are not the account's to count.
func (s Server) PublicWorkerPool(ctx context.Context, _ api.PublicWorkerPoolRequestObject) (api.PublicWorkerPoolResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	q, err := s.proj.Quotas.Quota(ctx, p.OrganizationID, p.AccountID, featuresapi.PublicWorkerSlots)
	if err != nil {
		return nil, err //nolint:wrapcheck // the features domain names the read
	}
	c, err := s.proj.queues.PublicQueue(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // the runs domain names the read
	}
	users, err := s.proj.users.PublicUsers(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	return api.PublicWorkerPool200JSONResponse{Body: api.PublicWorkerPool{
		Parallelism:          q.Value,
		BusyWorkers:          count(c.Busy),
		PendingRuns:          count(c.Pending),
		SchedulableRunsCount: count(c.Schedulable),
		UsersCount:           count(users),
		IntentProjects:       []api.IntentProjectReference{},
	}}, nil
}

// DefaultPublicWorkerPoolRunnerImage answers the image the caller's
// account's runs on the public pool use when neither their runtime
// configuration nor their stack names one.
func (s Server) DefaultPublicWorkerPoolRunnerImage(
	ctx context.Context,
	_ api.DefaultPublicWorkerPoolRunnerImageRequestObject,
) (api.DefaultPublicWorkerPoolRunnerImageResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	image, err := s.images.Public(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	return api.DefaultPublicWorkerPoolRunnerImage200JSONResponse{Body: image}, nil
}

// OutgoingIPAddresses answers the addresses the operator declares public
// execution leaves from, none while public execution is switched off. No
// address is looked up from a cloud or a node.
func (s Server) OutgoingIPAddresses(ctx context.Context, _ api.OutgoingIPAddressesRequestObject) (api.OutgoingIPAddressesResponseObject, error) {
	if _, err := principal(ctx); err != nil {
		return nil, err
	}
	out := []string{}
	if s.proj.PublicEnabled {
		out = append(out, s.proj.Egress...)
	}
	return api.OutgoingIPAddresses200JSONResponse{Body: out}, nil
}

// WorkerUsage answers the caller's account's private worker usage for the
// billing cycle its input selects: the cycle and the cycles it may
// select, and its private-workers limit as the contract. No percentile is
// billed, so no burst budget applies.
func (s Server) WorkerUsage(ctx context.Context, req api.WorkerUsageRequestObject) (api.WorkerUsageResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ago := 0
	if in := req.Params.Input; in != nil && in.CyclesAgo != nil {
		ago = int(*in.CyclesAgo)
	}
	cycles := s.proj.Samples.Cycles()
	at := slices.IndexFunc(cycles, func(c sample.Cycle) bool { return c.CyclesAgo == ago })
	if at < 0 {
		return nil, &kernel.Invalid{
			Detail: "The billing cycle cannot be selected.",
			Errors: []kernel.FieldError{{
				Pointer: "#/query/input", Reason: kernel.ReasonRule,
				Message: "cyclesAgo names none of the selectable cycles.",
			}},
		}
	}
	q, err := s.proj.Quotas.Quota(ctx, p.OrganizationID, p.AccountID, featuresapi.PrivateWorkers)
	if err != nil {
		return nil, err //nolint:wrapcheck // the features domain names the read
	}
	out := api.WorkerUsage{
		Cycle:                cycleView(cycles[at]),
		MultiAccountContract: q.Pooled,
		SelectableCycles:     make([]api.WorkerUsageCycle, len(cycles)),
	}
	if q.Value != nil {
		v := count(int(min(*q.Value, math.MaxInt32)))
		out.ContractedLimit = &v
	}
	for i, c := range cycles {
		out.SelectableCycles[i] = cycleView(c)
	}
	return api.WorkerUsage200JSONResponse{Body: out}, nil
}

func cycleView(c sample.Cycle) api.WorkerUsageCycle {
	return api.WorkerUsageCycle{
		CyclesAgo: int32(c.CyclesAgo), //nolint:gosec // at most sample.MaxCyclesAgo
		Start:     unix(c.Start.Unix()), End: unix(c.End.Unix()),
	}
}

// count is n as the API's int32 counts, which no count of rows exceeds.
func count(n int) int32 { return int32(min(n, math.MaxInt32)) } //nolint:gosec // clamped

// counts are the queue counts and the users of each of all, none when the
// other domains' reads are not bound.
func (s Server) counts(ctx context.Context, p kernel.Principal, ids []kernel.ULID) (map[kernel.ULID]workersapi.QueueCounts, map[kernel.ULID][]workersapi.PoolUser, error) {
	if s.proj == nil || s.proj.queues == nil || s.proj.users == nil {
		return nil, nil, nil
	}
	queued, err := s.proj.queues.PoolQueues(ctx, p, ids)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the runs domain names the read
	}
	users, err := s.proj.users.PoolUsers(ctx, p, ids)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	return queued, users, nil
}
