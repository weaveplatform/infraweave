package http_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/sample"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// projections stands in for the features, runs and stacks domains: each
// limit's quota, each pool's queue counts and users, failing the read
// fail names.
type projections struct {
	quotas map[string]featuresapi.Quota
	queues map[kernel.ULID]workersapi.QueueCounts
	public workersapi.QueueCounts
	users  map[kernel.ULID][]workersapi.PoolUser
	count  int
	fail   map[string]error
}

func (p *projections) Quota(_ context.Context, _ kernel.OrganizationID, _ kernel.AccountID, limit string) (featuresapi.Quota, error) {
	return p.quotas[limit], p.fail["Quota:"+limit]
}

func (p *projections) PoolQueues(context.Context, kernel.Principal, []kernel.ULID) (map[kernel.ULID]workersapi.QueueCounts, error) {
	return p.queues, p.fail["PoolQueues"]
}

func (p *projections) PublicQueue(context.Context, kernel.Principal) (workersapi.QueueCounts, error) {
	return p.public, p.fail["PublicQueue"]
}

func (p *projections) PoolUsers(context.Context, kernel.Principal, []kernel.ULID) (map[kernel.ULID][]workersapi.PoolUser, error) {
	return p.users, p.fail["PoolUsers"]
}

func (p *projections) PublicUsers(context.Context, kernel.Principal) (int, error) {
	return p.count, p.fail["PublicUsers"]
}

// imageStore holds one account's runner images.
type imageStore struct {
	in   images.Images
	fail error
}

func (s *imageStore) RunnerImages(context.Context, kernel.AccountID) (images.Images, error) {
	return s.in, s.fail
}

func (s *imageStore) SetRunnerImages(_ context.Context, _ kernel.AccountID, in images.Images) error {
	s.in = in
	return s.fail
}

// projected is f's server reading p, with public execution enabled when
// enabled, and the samples kept a year at 2026-11-22.
func projected(f *fixture, p *projections, enabled bool, img images.Store) http.Server {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	svc := pool.New(pool.Deps{Store: f.store, DB: db{}, Spaces: f.sp, Authz: f.az})
	wk := worker.New(worker.Deps{Store: workers{s: f.store}, DB: db{}})
	srv := http.New(svc, images.New(img), wk, f.az, f.sp).WithOptions(http.Options{
		Quotas: p, PublicEnabled: enabled, Egress: []string{"203.0.113.7"},
		Samples: sample.New(sample.Deps{Retention: 365 * 24 * time.Hour, Now: func() time.Time { return now }}),
	})
	srv.Bind(p, p)
	return srv
}

// TestUnitPublicWorkerPool answers the account's quota, queue and users,
// and each domain's failure as it is.
func TestUnitPublicWorkerPool(t *testing.T) {
	ctx := signedIn(t)
	slots := int64(3)
	p := &projections{
		quotas: map[string]featuresapi.Quota{featuresapi.PublicWorkerSlots: {Value: &slots}},
		public: workersapi.QueueCounts{Pending: 4, Schedulable: 2, Busy: 1}, count: 5, fail: map[string]error{},
	}
	srv := projected(newFixture(t), p, true, &imageStore{})
	res, err := srv.PublicWorkerPool(ctx, api.PublicWorkerPoolRequestObject{})
	got, ok := res.(api.PublicWorkerPool200JSONResponse)
	if err != nil || !ok || *got.Body.Parallelism != 3 || got.Body.PendingRuns != 4 || got.Body.SchedulableRunsCount != 2 ||
		got.Body.BusyWorkers != 1 || got.Body.UsersCount != 5 || got.Body.IntentProjects == nil {
		t.Fatalf("PublicWorkerPool = %+v, %v", res, err)
	}
	for _, read := range []string{"Quota:" + featuresapi.PublicWorkerSlots, "PublicQueue", "PublicUsers"} {
		p.fail = map[string]error{read: errBoom}
		if _, err := srv.PublicWorkerPool(ctx, api.PublicWorkerPoolRequestObject{}); !errors.Is(err, errBoom) {
			t.Errorf("PublicWorkerPool with %s failing = %v", read, err)
		}
	}
	if _, err := srv.PublicWorkerPool(t.Context(), api.PublicWorkerPoolRequestObject{}); err == nil {
		t.Error("PublicWorkerPool without a principal succeeded")
	}
}

// TestUnitPoolViewCounts fills a pool's view with its queue counts and
// users, and fails as either domain does.
func TestUnitPoolViewCounts(t *testing.T) {
	ctx := signedIn(t)
	f := newFixture(t)
	id := f.ids[0]
	space := kernel.NewULID()
	p := &projections{
		queues: map[kernel.ULID]workersapi.QueueCounts{id: {Pending: 2, Schedulable: 2}},
		users:  map[kernel.ULID][]workersapi.PoolUser{id: {{Slug: "infra", Name: "Infra", Space: space}}},
		fail:   map[string]error{},
	}
	srv := projected(f, p, true, &imageStore{})
	res, err := srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: id.String()})
	got, ok := res.(api.WorkerPool200JSONResponse)
	if err != nil || !ok || got.Body.PendingRuns != 2 || got.Body.SchedulableRunsCount != 2 || got.Body.UsersCount != 1 ||
		len(got.Body.Users) != 1 || got.Body.Users[0].StackId != "infra" || got.Body.Users[0].StackName != "Infra" {
		t.Fatalf("WorkerPool = %+v, %v", res, err)
	}
	for _, read := range []string{"PoolQueues", "PoolUsers"} {
		p.fail = map[string]error{read: errBoom}
		if _, err := srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: id.String()}); !errors.Is(err, errBoom) {
			t.Errorf("WorkerPool with %s failing = %v", read, err)
		}
	}
}

// TestUnitAccountPoolReads answers the public runner image, the declared
// egress while public execution is enabled, and the usage cycles.
func TestUnitAccountPoolReads(t *testing.T) {
	ctx := signedIn(t)
	f := newFixture(t)
	img := &imageStore{}
	srv := projected(f, &projections{fail: map[string]error{}}, true, img)
	image := func() (string, error) {
		res, err := srv.DefaultPublicWorkerPoolRunnerImage(ctx, api.DefaultPublicWorkerPoolRunnerImageRequestObject{})
		got, _ := res.(api.DefaultPublicWorkerPoolRunnerImage200JSONResponse)
		return got.Body, err
	}
	if got, err := image(); err != nil || got != images.DefaultPrivate {
		t.Errorf("the default public image = %q, %v", got, err)
	}
	img.in.Public = new("ghcr.io/acme/public:1")
	if got, err := image(); err != nil || got != "ghcr.io/acme/public:1" {
		t.Errorf("the account's public image = %q, %v", got, err)
	}
	img.fail = errBoom
	if _, err := image(); !errors.Is(err, errBoom) {
		t.Errorf("the public image with the store failing = %v", err)
	}
	for enabled, want := range map[bool][]string{true: {"203.0.113.7"}, false: {}} {
		srv := projected(f, &projections{}, enabled, img)
		res, err := srv.OutgoingIPAddresses(ctx, api.OutgoingIPAddressesRequestObject{})
		got, ok := res.(api.OutgoingIPAddresses200JSONResponse)
		if err != nil || !ok || got.Body == nil || !slices.Equal(got.Body, want) {
			t.Errorf("enabled %t: OutgoingIPAddresses = %+v, %v", enabled, res, err)
		}
	}
	for _, call := range []func(context.Context) error{
		func(ctx context.Context) error {
			return second(srv.OutgoingIPAddresses(ctx, api.OutgoingIPAddressesRequestObject{}))
		},
		func(ctx context.Context) error {
			return second(srv.DefaultPublicWorkerPoolRunnerImage(ctx, api.DefaultPublicWorkerPoolRunnerImageRequestObject{}))
		},
		func(ctx context.Context) error { return second(srv.WorkerUsage(ctx, api.WorkerUsageRequestObject{})) },
	} {
		if err := call(t.Context()); err == nil {
			t.Error("an account read without a principal succeeded")
		}
	}
}

// TestUnitWorkerUsage answers the selected cycle among the six a year's
// retention covers, the private-workers quota as the contract, and refuses
// a cycle out of reach.
func TestUnitWorkerUsage(t *testing.T) {
	ctx := signedIn(t)
	workers := int64(3)
	p := &projections{
		quotas: map[string]featuresapi.Quota{featuresapi.PrivateWorkers: {Value: &workers, Pooled: true}},
		fail:   map[string]error{},
	}
	srv := projected(newFixture(t), p, true, &imageStore{})
	usage := func(ago *int32) (api.WorkerUsage, error) {
		req := api.WorkerUsageRequestObject{}
		if ago != nil {
			req.Params.Input = &api.WorkerUsageInput{CyclesAgo: ago}
		}
		res, err := srv.WorkerUsage(ctx, req)
		got, _ := res.(api.WorkerUsage200JSONResponse)
		return got.Body, err
	}
	got, err := usage(nil)
	if err != nil || got.Cycle.CyclesAgo != 0 || len(got.SelectableCycles) != sample.MaxCyclesAgo+1 ||
		got.ContractedLimit == nil || *got.ContractedLimit != 3 || !got.MultiAccountContract ||
		time.Unix(int64(got.Cycle.Start), 0).UTC() != time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("WorkerUsage = %+v, %v", got, err)
	}
	if got, err := usage(new(int32(5))); err != nil || got.Cycle.CyclesAgo != 5 ||
		time.Unix(int64(got.Cycle.Start), 0).UTC() != time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("WorkerUsage five cycles ago = %+v, %v", got, err)
	}
	for _, ago := range []int32{-1, 6} {
		_, err := usage(&ago)
		if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Pointer != "#/query/input" {
			t.Errorf("WorkerUsage %d cycles ago = %v, want a refusal", ago, err)
		}
	}
	p.quotas = nil
	if got, err := usage(nil); err != nil || got.ContractedLimit != nil || got.MultiAccountContract {
		t.Errorf("WorkerUsage unbounded = %+v, %v", got, err)
	}
	p.fail["Quota:"+featuresapi.PrivateWorkers] = errBoom
	if _, err := usage(nil); !errors.Is(err, errBoom) {
		t.Errorf("WorkerUsage with the features domain failing = %v", err)
	}
}

// TestUnitPublicSelectorReserved refuses the public selector as a private
// pool's ID before any read.
func TestUnitPublicSelectorReserved(t *testing.T) {
	f := newFixture(t)
	f.store.fail = errBoom
	svc := pool.New(pool.Deps{Store: f.store, DB: db{}, Spaces: f.sp, Authz: f.az})
	_, err := svc.SpaceOf(t.Context(), kernel.NewAccountID(), pool.PublicSelector)
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Pointer != "#/path/workerPoolId" {
		t.Errorf("SpaceOf(public) = %v, want a refusal at the path", err)
	}
}
