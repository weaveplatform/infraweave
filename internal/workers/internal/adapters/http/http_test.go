package http_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

var errBoom = errors.New("boom")

// store holds pools and their workers by ID, failing every read when
// fail is set.
type store struct {
	pool.Store

	rows    map[kernel.ULID]pool.Pool
	workers map[kernel.ULID][]worker.Worker
	fail    error
}

func (s *store) Get(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
	_ bool,
) (pool.Pool, error) {
	if s.fail != nil {
		return pool.Pool{}, s.fail
	}
	p, ok := s.rows[id]
	if !ok {
		return p, pool.ErrNotFound
	}
	return p, nil
}

func (s *store) Search(
	context.Context,
	kernel.AccountID,
	search.Input,
	pool.Scope,
) (search.Result[pool.Pool], error) {
	out := search.Result[pool.Pool]{}
	for _, p := range s.rows {
		out.Edges = append(out.Edges, search.Edge[pool.Pool]{Node: p})
	}
	return out, s.fail
}

func (s *store) RunnerImages(context.Context, kernel.AccountID) (images.Images, error) {
	return images.Images{}, s.fail
}

func (s *store) SetRunnerImages(context.Context, kernel.AccountID, images.Images) error {
	return s.fail
}

func (s *store) Suggest(
	context.Context,
	kernel.AccountID,
	search.SuggestionsInput,
	pool.Scope,
) (search.Suggestions, error) {
	return search.Suggestions{}, s.fail
}

// workers is the store as the worker service reads it.
type workers struct {
	worker.Store

	s *store
}

func (w workers) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	lock bool,
) (pool.Pool, error) {
	return w.s.Get(ctx, acct, id, lock)
}

func (w workers) Drain(
	_ context.Context,
	_ kernel.AccountID,
	p, id kernel.ULID,
	drain bool,
) (worker.Worker, error) {
	for i, wk := range w.s.workers[p] {
		if wk.ID == id {
			w.s.workers[p][i].Drained = drain
			return w.s.workers[p][i], w.s.fail
		}
	}
	return worker.Worker{}, worker.ErrNotFound
}

func (w workers) Cycle(context.Context, kernel.AccountID, kernel.ULID) error { return w.s.fail }

func (w workers) SearchWorkers(
	_ context.Context,
	_ kernel.AccountID,
	p, _ kernel.ULID,
	_ search.Input,
) (search.Result[worker.Worker], error) {
	out := search.Result[worker.Worker]{}
	for _, wk := range w.s.workers[p] {
		out.Edges = append(out.Edges, search.Edge[worker.Worker]{Cursor: wk.ID.String(), Node: wk})
	}
	return out, w.s.fail
}

func (w workers) SuggestWorkers(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	kernel.ULID,
	search.SuggestionsInput,
) (search.Suggestions, error) {
	return search.Suggestions{}, w.s.fail
}

func (w workers) OfPools(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) (map[kernel.ULID][]worker.Worker, error) {
	return w.s.workers, w.s.fail
}

// spaces fails Slugs when fail is set.
type spaces struct {
	accessapi.Spaces

	fail error
}

func (s spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	return map[kernel.ULID]string{}, s.fail
}

func (s spaces) Find(context.Context, kernel.AccountID, string) (kernel.ULID, error) {
	return kernel.NewULID(), s.fail
}

func (spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return nil, nil
}

// authz fails Actions when fail is set.
type authz struct {
	accessapi.Authorizer

	fail error
}

func (a authz) Actions(context.Context, kernel.Principal, kernel.ULID) ([]accessapi.Action, error) {
	return []accessapi.Action{accessapi.ActionSpaceRead}, a.fail
}

func (authz) Reachable(context.Context, kernel.Principal, accessapi.Action) ([]kernel.ULID, error) {
	return nil, nil
}

// db begins no transaction, so every write fails.
type db struct{}

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return nil, errBoom }

type fixture struct {
	srv   http.Server
	store *store
	sp    *spaces
	az    *authz
	ids   []kernel.ULID
	csr   string
	// worker is the first pool's one worker, busy.
	worker worker.Worker
}

// newFixture serves two pools in one space, the first with a description
// and one busy worker.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	space := kernel.NewULID()
	f := &fixture{
		store: &store{
			rows:    map[kernel.ULID]pool.Pool{},
			workers: map[kernel.ULID][]worker.Worker{},
		},
		sp:  &spaces{},
		az:  &authz{},
		ids: []kernel.ULID{kernel.NewULID(), kernel.NewULID()},
	}
	for i, id := range f.ids {
		f.store.rows[id] = pool.Pool{ID: id, Name: id.String(), Space: space, Labels: []string{}}
		if i == 0 {
			p := f.store.rows[id]
			p.Description = "ours"
			f.store.rows[id] = p
		}
	}
	f.worker = worker.Worker{
		ID: kernel.NewULID(), Pool: f.ids[0], Busy: true, Metadata: map[string]string{"host": "a"},
		CreatedAt: time.Unix(1700000000, 0),
	}
	f.store.workers[f.ids[0]] = []worker.Worker{f.worker}
	svc := pool.New(pool.Deps{Store: f.store, DB: db{}, Spaces: f.sp, Authz: f.az})
	svc.BindStacks(func(_ context.Context, _ kernel.AccountID, id string) (kernel.ULID, error) {
		if id == "gone" {
			return kernel.ULID{}, accessapi.ErrNoSubject
		}
		return space, nil
	})
	wk := worker.New(worker.Deps{Store: workers{s: f.store}, DB: db{}})
	f.srv = http.New(svc, images.New(f.store), wk, f.az, f.sp)
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
	if err != nil {
		t.Fatal(err)
	}
	f.csr = base64.StdEncoding.EncodeToString(der)
	return f
}

// calls are every handler on the first pool, each answering only its
// error.
func (f *fixture) calls(ctx context.Context) map[string]error {
	id := f.ids[0].String()
	return map[string]error{
		"WorkerPools": second(f.srv.WorkerPools(ctx, api.WorkerPoolsRequestObject{})),
		"SearchWorkerPools": second(f.srv.SearchWorkerPools(ctx,
			api.SearchWorkerPoolsRequestObject{Body: &api.SearchWorkerPoolsJSONRequestBody{}})),
		"SearchWorkerPoolsSuggestions": second(f.srv.SearchWorkerPoolsSuggestions(ctx,
			api.SearchWorkerPoolsSuggestionsRequestObject{
				Body: &api.SearchWorkerPoolsSuggestionsJSONRequestBody{},
			})),
		"WorkerPool": second(f.srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: id})),
		"WorkerPoolCreate": second(f.srv.WorkerPoolCreate(ctx, api.WorkerPoolCreateRequestObject{
			Body: &api.WorkerPoolCreateJSONRequestBody{Name: "x", CertificateSigningRequest: f.csr},
		})),
		"WorkerPoolUpdate": second(f.srv.WorkerPoolUpdate(ctx, api.WorkerPoolUpdateRequestObject{
			WorkerPoolId: id, Body: &api.WorkerPoolUpdateJSONRequestBody{Name: "x"},
		})),
		"WorkerPoolPatch": second(f.srv.WorkerPoolPatch(ctx,
			api.WorkerPoolPatchRequestObject{WorkerPoolId: id})),
		"WorkerPoolDelete": second(f.srv.WorkerPoolDelete(ctx,
			api.WorkerPoolDeleteRequestObject{WorkerPoolId: id})),
		"WorkerPoolReset": second(f.srv.WorkerPoolReset(ctx, api.WorkerPoolResetRequestObject{
			WorkerPoolId: id,
			Body:         &api.WorkerPoolResetJSONRequestBody{CertificateSigningRequest: f.csr},
		})),
		"StackAttachableWorkerPools": second(f.srv.StackAttachableWorkerPools(ctx,
			api.StackAttachableWorkerPoolsRequestObject{StackId: "infra"})),
		"AttachableWorkerPoolsInSpace": second(f.srv.AttachableWorkerPoolsInSpace(ctx,
			api.AttachableWorkerPoolsInSpaceRequestObject{
				Params: api.AttachableWorkerPoolsInSpaceParams{Id: "team"},
			})),
		"DefaultPrivateWorkerPoolRunnerImage": second(f.srv.DefaultPrivateWorkerPoolRunnerImage(ctx,
			api.DefaultPrivateWorkerPoolRunnerImageRequestObject{})),
		"WorkerDrainSet": second(f.srv.WorkerDrainSet(ctx, api.WorkerDrainSetRequestObject{
			WorkerPoolId: id, WorkerId: f.worker.ID.String(),
			Body: &api.WorkerDrainSetJSONRequestBody{Drain: true},
		})),
		"WorkerPoolCycle": second(f.srv.WorkerPoolCycle(ctx,
			api.WorkerPoolCycleRequestObject{WorkerPoolId: id})),
		"WorkerPoolSearchWorkers": second(f.srv.WorkerPoolSearchWorkers(ctx,
			api.WorkerPoolSearchWorkersRequestObject{WorkerPoolId: id})),
		"WorkerPoolSearchWorkersSuggestions": second(f.srv.WorkerPoolSearchWorkersSuggestions(ctx,
			api.WorkerPoolSearchWorkersSuggestionsRequestObject{WorkerPoolId: id})),
		"AccountUpdateDefaultWorkerPoolRunnerImages": second(
			f.srv.AccountUpdateDefaultWorkerPoolRunnerImages(ctx,
				api.AccountUpdateDefaultWorkerPoolRunnerImagesRequestObject{
					Body: &api.AccountUpdateDefaultWorkerPoolRunnerImagesJSONRequestBody{},
				})),
	}
}

// store answers only through the store, which no access domain failure
// reaches.
var storeOnly = map[string]bool{
	"SearchWorkerPools": true, "SearchWorkerPoolsSuggestions": true,
	"DefaultPrivateWorkerPoolRunnerImage": true, "AccountUpdateDefaultWorkerPoolRunnerImages": true,
	"WorkerDrainSet": true, "WorkerPoolCycle": true, "WorkerPoolSearchWorkers": true,
	"WorkerPoolSearchWorkersSuggestions": true,
}

func second[T any](_ T, err error) error { return err }

func signedIn(t *testing.T) context.Context {
	t.Helper()
	return kernel.WithPrincipal(t.Context(), kernel.Principal{
		Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(), AccountID: kernel.NewAccountID(),
	})
}

// TestUnitUnauthenticated checks that every handler refuses a request
// with no principal as 401.
func TestUnitUnauthenticated(t *testing.T) {
	for name, err := range newFixture(t).calls(t.Context()) {
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != 401 {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
}

// TestUnitFailures checks that a failing store, access domain or
// database fails each handler that reaches it, not as a refusal.
func TestUnitFailures(t *testing.T) {
	ctx := signedIn(t)
	for name, fail := range map[string]func(*fixture){
		"store":  func(f *fixture) { f.store.fail = errBoom },
		"spaces": func(f *fixture) { f.sp.fail = errBoom },
		"authz":  func(f *fixture) { f.az.fail = errBoom },
	} {
		f := newFixture(t)
		fail(f)
		for call, err := range f.calls(ctx) {
			if storeOnly[call] && name != "store" {
				continue
			}
			if !errors.Is(err, errBoom) {
				t.Errorf("%s with %s down = %v", call, name, err)
			}
		}
	}
	for call, err := range newFixture(t).calls(ctx) {
		switch call {
		case "WorkerPoolCreate", "WorkerPoolUpdate", "WorkerPoolPatch", "WorkerPoolDelete",
			"WorkerPoolReset":
			if !errors.Is(err, errBoom) {
				t.Errorf("%s with the database down = %v", call, err)
			}
		default:
			if err != nil {
				t.Errorf("%s = %v", call, err)
			}
		}
	}
}

// TestUnitMissing checks that a pool or a stack the account lacks
// answers 404.
func TestUnitMissing(t *testing.T) {
	f := newFixture(t)
	ctx := signedIn(t)
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		_, err := f.srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: id})
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != 404 {
			t.Errorf("WorkerPool(%s) = %v", id, err)
		}
	}
	_, err := f.srv.StackAttachableWorkerPools(ctx,
		api.StackAttachableWorkerPoolsRequestObject{StackId: "gone"})
	if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != 404 {
		t.Errorf("StackAttachableWorkerPools(gone) = %v", err)
	}
}

// TestUnitWorkers checks that a pool answers its workers, and that a drain
// answers the worker drained, or 404 for a worker the pool lacks.
func TestUnitWorkers(t *testing.T) {
	f := newFixture(t)
	ctx := signedIn(t)
	got, err := f.srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: f.ids[0].String()})
	if err != nil {
		t.Fatal(err)
	}
	v := got.(api.WorkerPool200JSONResponse).Body
	if v.WorkersCount != 1 || v.BusyWorkers != 1 || len(v.Workers) != 1 ||
		v.Workers[0].Id != f.worker.ID.String() || v.Workers[0].Status != api.BUSY ||
		v.Workers[0].Metadata == nil || *v.Workers[0].Metadata != `{"host":"a"}` ||
		v.Workers[0].AvailableAt != nil || v.Workers[0].CreatedAt != 1700000000 {
		t.Errorf("pool = %+v", v)
	}
	drained, err := f.srv.WorkerDrainSet(ctx, api.WorkerDrainSetRequestObject{
		WorkerPoolId: f.ids[0].String(), WorkerId: f.worker.ID.String(),
		Body: &api.WorkerDrainSetJSONRequestBody{Drain: true},
	})
	if w := drained.(api.WorkerDrainSet200JSONResponse).Body; err != nil || !w.Drained ||
		w.Status != api.DRAINED {
		t.Errorf("drained = %+v, %v", w, err)
	}
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		_, err := f.srv.WorkerDrainSet(ctx, api.WorkerDrainSetRequestObject{
			WorkerPoolId: f.ids[0].String(), WorkerId: id,
			Body: &api.WorkerDrainSetJSONRequestBody{},
		})
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != 404 {
			t.Errorf("drain of worker %s = %v", id, err)
		}
	}
	found, err := f.srv.WorkerPoolSearchWorkers(ctx,
		api.WorkerPoolSearchWorkersRequestObject{WorkerPoolId: f.ids[0].String()})
	if out := found.(api.WorkerPoolSearchWorkers200JSONResponse).Body; err != nil ||
		len(out.Edges) != 1 || out.Edges[0].Cursor != f.worker.ID.String() {
		t.Errorf("search = %+v, %v", out, err)
	}
	idle := worker.Worker{ID: kernel.NewULID(), AvailableAt: &f.worker.CreatedAt}
	f.store.workers[f.ids[1]] = []worker.Worker{idle}
	got, err = f.srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: f.ids[1].String()})
	if v := got.(api.WorkerPool200JSONResponse).Body; err != nil || v.BusyWorkers != 0 ||
		v.Workers[0].AvailableAt == nil || *v.Workers[0].AvailableAt != 1700000000 ||
		v.Workers[0].Metadata != nil {
		t.Errorf("idle pool = %+v, %v", v, err)
	}
}
