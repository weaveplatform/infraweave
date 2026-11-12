package worker_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/credential"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

var errDown = errors.New("down")

// store keeps pools, nonces and workers in memory; fail names a method
// that fails with errDown, removeOnLock a worker a locked read finds
// gone, and wake is called for each pool a drain or a cycle changes, as
// the pg store's notification reaches WatchPolls.
type store struct {
	mu           sync.Mutex
	ca           []byte
	pools        map[kernel.ULID]pool.Pool
	nonces       map[string]time.Time
	workers      map[kernel.ULID]worker.Worker
	fail         string
	removeOnLock kernel.ULID
	wake         func(kernel.ULID)
	images       images.Images
}

func (s *store) down(method string) error {
	if s.fail == method {
		return errDown
	}
	return nil
}

func (s *store) PoolByID(_ context.Context, id kernel.ULID) (pool.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("PoolByID"); err != nil {
		return pool.Pool{}, err
	}
	p, ok := s.pools[id]
	if !ok {
		return p, pool.ErrNotFound
	}
	return p, nil
}

func (s *store) Get(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	_ bool,
) (pool.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Get"); err != nil {
		return pool.Pool{}, err
	}
	p, ok := s.pools[id]
	if !ok || p.Account != acct {
		return p, pool.ErrNotFound
	}
	return p, nil
}

func (s *store) CA(context.Context) ([]byte, error) { return s.ca, s.down("CA") }

func (s *store) Nonce(
	_ context.Context,
	_ kernel.AccountID,
	p kernel.ULID,
	jti string,
	expires time.Time,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Nonce"); err != nil {
		return false, err
	}
	key := p.String() + "/" + jti
	if _, ok := s.nonces[key]; ok {
		return false, nil
	}
	s.nonces[key] = expires
	return true, nil
}

func (s *store) Count(
	_ context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
) (featuresapi.Usage, error) {
	var u featuresapi.Usage
	for _, w := range s.workers {
		if w.Organization == org {
			u.Organization++
			if w.Account == acct {
				u.Account++
			}
		}
	}
	return u, s.down("Count")
}

// Worker fails on "Worker", and a locked read also on "Lock".
func (s *store) Worker(_ context.Context, id kernel.ULID, lock bool) (worker.Worker, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Worker"); err != nil {
		return worker.Worker{}, 0, err
	}
	if err := s.down("Lock"); lock && err != nil {
		return worker.Worker{}, 0, err
	}
	if lock && id == s.removeOnLock {
		delete(s.workers, id)
	}
	w, ok := s.workers[id]
	if !ok {
		return w, 0, worker.ErrNotFound
	}
	return w, s.pools[w.Pool].Serial, nil
}

func (s *store) CreateWorker(_ context.Context, w worker.Worker) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("CreateWorker"); err != nil {
		return err
	}
	s.workers[w.ID] = w
	return nil
}

func (s *store) Resume(_ context.Context, w worker.Worker) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.workers[w.ID]
	old.Serial, old.Version, old.OS, old.Arch, old.Metadata = w.Serial, w.Version, w.OS, w.Arch,
		w.Metadata
	old.LastSeenAt = w.LastSeenAt
	s.workers[w.ID] = old
	return nil
}

func (s *store) Seen(
	_ context.Context,
	id kernel.ULID,
	busy bool,
	now time.Time,
) (worker.Worker, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Seen"); err != nil {
		return worker.Worker{}, 0, err
	}
	w, ok := s.workers[id]
	if !ok {
		return w, 0, worker.ErrNotFound
	}
	w.LastSeenAt, w.Busy = now, busy
	s.workers[id] = w
	return w, s.pools[w.Pool].Serial, nil
}

func (s *store) Claimed(_ context.Context, id kernel.ULID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.down("Claimed"); err != nil {
		return err
	}
	w := s.workers[id]
	w.Busy, w.AvailableAt = true, nil
	s.workers[id] = w
	return nil
}

func (s *store) Remove(_ context.Context, id kernel.ULID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workers[id]; !ok {
		return worker.ErrNotFound
	}
	delete(s.workers, id)
	return nil
}

func (s *store) Drain(
	_ context.Context,
	acct kernel.AccountID,
	p, id kernel.ULID,
	drain bool,
) (worker.Worker, error) {
	s.mu.Lock()
	w, ok := s.workers[id]
	if !ok || w.Account != acct || w.Pool != p {
		s.mu.Unlock()
		return w, worker.ErrNotFound
	}
	w.Drained = drain
	s.workers[id] = w
	s.mu.Unlock()
	s.wake(p)
	return w, nil
}

func (s *store) Cycle(_ context.Context, acct kernel.AccountID, p kernel.ULID) error {
	s.mu.Lock()
	for id, w := range s.workers {
		if w.Account == acct && w.Pool == p {
			w.Cycle = true
			s.workers[id] = w
		}
	}
	s.mu.Unlock()
	s.wake(p)
	return s.down("Cycle")
}

func (s *store) SearchWorkers(
	_ context.Context,
	_ kernel.AccountID,
	p, _ kernel.ULID,
	_ search.Input,
) (search.Result[worker.Worker], error) {
	var out search.Result[worker.Worker]
	for _, w := range s.workers {
		if w.Pool == p {
			out.Edges = append(out.Edges, search.Edge[worker.Worker]{Node: w})
		}
	}
	return out, nil
}

func (s *store) SuggestWorkers(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	kernel.ULID,
	search.SuggestionsInput,
) (search.Suggestions, error) {
	return search.Suggestions{}, s.down("SuggestWorkers")
}

func (s *store) RunnerImages(context.Context, kernel.AccountID) (images.Images, error) {
	return s.images, s.down("RunnerImages")
}

func (s *store) OfPools(
	_ context.Context,
	_ kernel.AccountID,
	pools []kernel.ULID,
) (map[kernel.ULID][]worker.Worker, error) {
	out := map[kernel.ULID][]worker.Worker{}
	for _, w := range s.workers {
		if slices.Contains(pools, w.Pool) {
			out[w.Pool] = append(out[w.Pool], w)
		}
	}
	return out, nil
}

// tokens mints a token naming its worker or run, valid for an hour;
// fail fails the session tokens and failRun the run tokens.
type tokens struct{ fail, failRun bool }

func (t tokens) WorkerToken(_ context.Context, w kernel.ULID) (accessapi.Token, error) {
	if t.fail {
		return accessapi.Token{}, errDown
	}
	return accessapi.Token{Raw: "session " + w.String(), Expires: time.Now().Add(time.Hour)}, nil
}

func (t tokens) RunToken(_ context.Context, run kernel.ULID) (accessapi.Token, error) {
	if t.failRun {
		return accessapi.Token{}, errDown
	}
	return accessapi.Token{Raw: "run " + run.String(), Expires: time.Now().Add(time.Hour)}, nil
}

// limits refuses a new worker once the organization has max.
type limits struct{ max int64 }

func (l limits) CheckLimit(
	ctx context.Context,
	_ kernel.OrganizationID,
	_ kernel.AccountID,
	limit string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	u, err := count(ctx)
	if err != nil {
		return err
	}
	if u.Organization >= l.max {
		return &featuresapi.LimitError{
			Limit: limit,
			Scope: featuresapi.ScopeOrganization,
			Value: l.max,
		}
	}
	return nil
}

// db begins transactions that commit and roll back nothing.
type db struct{}

type fakeTx struct{ pgx.Tx }

func (fakeTx) Commit(context.Context) error   { return nil }
func (fakeTx) Rollback(context.Context) error { return nil }

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }

// fixture is a service over one pool of one account, whose launcher
// holds key and the pool certificate cert.
type fixture struct {
	svc   *worker.Service
	store *store
	ca    credential.CA
	pool  pool.Pool
	key   *ecdsa.PrivateKey
	cert  *x509.Certificate
}

func newFixture(t *testing.T, d worker.Deps) *fixture {
	t.Helper()
	now := time.Now()
	ca, err := credential.NewCA(now)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{ca: ca, store: &store{
		ca: ca.Certificate.Raw, pools: map[kernel.ULID]pool.Pool{},
		nonces: map[string]time.Time{}, workers: map[kernel.ULID]worker.Worker{},
	}}
	f.pool = pool.Pool{
		Account: kernel.NewAccountID(), Organization: kernel.NewOrganizationID(),
		ID: kernel.NewULID(), Space: kernel.NewULID(), Serial: 2,
	}
	f.key, f.cert = f.sign(t, ca, f.pool.ID, f.pool.Serial)
	f.store.pools[f.pool.ID] = f.pool
	d.Store, d.DB = f.store, db{}
	if d.Tokens == nil {
		d.Tokens = tokens{}
	}
	f.svc = worker.New(d)
	f.store.wake = f.svc.Wake
	return f
}

// sign is a new key and the certificate ca signs for it as pool's.
func (*fixture) sign(
	t *testing.T,
	ca credential.CA,
	p kernel.ULID,
	serial int64,
) (*ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Sign(csr, p, serial, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return key, cert
}

// payload is a registration's payload for the fixture's pool.
func (f *fixture) payload() map[string]any {
	return map[string]any{
		"pool": f.pool.ID.String(), "iat": time.Now().Unix(), "jti": kernel.NewULID().String(),
		"version": "1.4.0", "os": "linux", "arch": "arm64",
		"metadata": map[string]string{"host": "a"},
	}
}

// jws signs payload with key, carrying cert in x5c.
func jws(t *testing.T, key *ecdsa.PrivateKey, cert *x509.Certificate, payload any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithHeader("x5c",
			[]string{base64.StdEncoding.EncodeToString(cert.Raw)}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *fixture) register(t *testing.T, edit func(map[string]any)) (worker.Registration, error) {
	t.Helper()
	p := f.payload()
	if edit != nil {
		edit(p)
	}
	return f.svc.Register(t.Context(), jws(t, f.key, f.cert, p))
}

// TestUnitWorkerRegistration checks that a registration signed with the
// pool key under the pool's current certificate registers a worker, and
// that a foreign chain, a reset serial, a replayed jti, a skewed iat and
// an old minor version are refused.
func TestUnitWorkerRegistration(t *testing.T) {
	f := newFixture(t, worker.Deps{Version: "v1.4.2"})
	reg, err := f.register(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := f.store.workers[reg.Worker]
	if w.Pool != f.pool.ID || w.Account != f.pool.Account ||
		w.Organization != f.pool.Organization || w.Serial != 2 || w.OS != "linux" ||
		w.Metadata["host"] != "a" || w.AvailableAt == nil || w.Status() != worker.StatusIdle {
		t.Errorf("registered %+v", w)
	}
	if reg.Token.Raw != "session "+reg.Worker.String() {
		t.Errorf("token = %+v", reg.Token)
	}
	// A registration naming the worker resumes it, keeping its flags.
	w.Busy = true
	f.store.workers[w.ID] = w
	again, err := f.register(t, func(p map[string]any) {
		p["worker"], p["os"] = w.ID.String(), "darwin"
	})
	if err != nil || again.Worker != w.ID || len(f.store.workers) != 1 ||
		f.store.workers[w.ID].OS != "darwin" || !f.store.workers[w.ID].Busy {
		t.Errorf("resume = %+v, %v; workers %+v", again, err, f.store.workers)
	}
	// One that names a worker it cannot resume registers a new one.
	for _, named := range []string{"nope", kernel.NewULID().String()} {
		if got, err := f.register(t, func(p map[string]any) { p["worker"] = named }); err != nil ||
			got.Worker == w.ID {
			t.Errorf("naming %q = %+v, %v", named, got, err)
		}
	}
	other := newFixture(t, worker.Deps{})
	foreignKey, foreignCert := other.key, other.cert
	for name, tc := range map[string]struct {
		raw   func() string
		setup func()
	}{
		"foreign chain": {raw: func() string {
			p := f.payload()
			p["pool"] = other.pool.ID.String()
			return jws(t, foreignKey, foreignCert, p)
		}},
		"another pool's certificate": {raw: func() string {
			p := f.payload()
			p["pool"] = kernel.NewULID().String()
			return jws(t, f.key, f.cert, p)
		}},
		"signed by another key": {raw: func() string {
			key, _ := f.sign(t, f.ca, f.pool.ID, f.pool.Serial)
			return jws(t, key, f.cert, f.payload())
		}},
		"reset serial": {setup: func() {
			p := f.pool
			p.Serial = 3
			f.store.pools[p.ID] = p
		}},
		"skewed iat behind": {raw: func() string {
			p := f.payload()
			p["iat"] = time.Now().Add(-6 * time.Minute).Unix()
			return jws(t, f.key, f.cert, p)
		}},
		"skewed iat ahead": {raw: func() string {
			p := f.payload()
			p["iat"] = time.Now().Add(6 * time.Minute).Unix()
			return jws(t, f.key, f.cert, p)
		}},
		"no jti": {raw: func() string {
			p := f.payload()
			delete(p, "jti")
			return jws(t, f.key, f.cert, p)
		}},
		"not JSON":   {raw: func() string { return jws(t, f.key, f.cert, "text") }},
		"not a JWS":  {raw: func() string { return "garbage" }},
		"no pool CA": {setup: func() { f.store.ca = nil }},
		"pool gone":  {setup: func() { delete(f.store.pools, f.pool.ID) }},
	} {
		savedCA, savedPools := f.store.ca, f.store.pools
		f.store.pools = map[kernel.ULID]pool.Pool{f.pool.ID: f.pool}
		if tc.setup != nil {
			tc.setup()
		}
		raw := jws(t, f.key, f.cert, f.payload())
		if tc.raw != nil {
			raw = tc.raw()
		}
		if _, err := f.svc.Register(t.Context(), raw); !errors.Is(err, worker.ErrUnauthenticated) {
			t.Errorf("%s = %v", name, err)
		}
		f.store.ca, f.store.pools = savedCA, savedPools
	}
	replay := jws(t, f.key, f.cert, f.payload())
	if _, err := f.svc.Register(t.Context(), replay); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Register(t.Context(), replay); !errors.Is(err, worker.ErrUnauthenticated) {
		t.Errorf("replayed jti = %v", err)
	}
	for version, ok := range map[string]bool{
		"1.4.0": true, "v1.4.9": true, "1.3.0": true, "1.2.9": false, "1.5.0": false,
		"2.4.0": false, "devel": false, "": false,
	} {
		_, err := f.register(t, func(p map[string]any) { p["version"] = version })
		inv, invalid := errors.AsType[*kernel.Invalid](err)
		switch {
		case ok && err != nil:
			t.Errorf("version %q = %v", version, err)
		case !ok && (!invalid || !strings.Contains(inv.Detail, "oldest accepted is 1.3.0")):
			t.Errorf("version %q = %v", version, err)
		}
	}
	// A server that is no release serves every launcher.
	devel := newFixture(t, worker.Deps{Version: "devel"})
	if _, err := devel.register(t, func(p map[string]any) { p["version"] = "0.0.1" }); err != nil {
		t.Errorf("devel server = %v", err)
	}
}

// TestUnitWorkerRegistrationLimit checks that a new worker beyond
// private-workers is refused and a resumed one is not counted again.
func TestUnitWorkerRegistrationLimit(t *testing.T) {
	f := newFixture(t, worker.Deps{Limits: limits{max: 1}})
	first, err := f.register(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.register(t, nil)
	if le, ok := errors.AsType[*featuresapi.LimitError](err); !ok ||
		le.Limit != featuresapi.PrivateWorkers {
		t.Errorf("beyond the limit = %v", err)
	}
	if _, err := f.register(t, func(p map[string]any) {
		p["worker"] = first.Worker.String()
	}); err != nil {
		t.Errorf("resume at the limit = %v", err)
	}
}

// TestUnitWorkerRegistrationBounds checks that a registration beyond
// its bounds is refused as invalid, one at them registers, and that a
// registration that is no JWS is refused before the pool CA is read.
func TestUnitWorkerRegistrationBounds(t *testing.T) {
	f := newFixture(t, worker.Deps{})
	long := func(n int) string { return strings.Repeat("é", n) }
	at := map[string]string{}
	for i := range worker.MaxMetadata - 1 {
		at[strconv.Itoa(i)] = long(worker.MaxMetadataValue)
	}
	at[long(worker.MaxMetadataKey)] = "v"
	if _, err := f.register(t, func(p map[string]any) {
		p["os"], p["arch"], p["metadata"] = long(worker.MaxDescription), "amd64", at
	}); err != nil {
		t.Errorf("at the bounds = %v", err)
	}
	over := map[string]any{}
	for i := range worker.MaxMetadata + 1 {
		over[strconv.Itoa(i)] = "v"
	}
	for name, edit := range map[string]func(map[string]any){
		"long version": func(p map[string]any) { p["version"] = long(worker.MaxDescription + 1) },
		"long os":      func(p map[string]any) { p["os"] = long(worker.MaxDescription + 1) },
		"long arch":    func(p map[string]any) { p["arch"] = long(worker.MaxDescription + 1) },
		"many entries": func(p map[string]any) { p["metadata"] = over },
		"long key": func(p map[string]any) {
			p["metadata"] = map[string]string{long(worker.MaxMetadataKey + 1): "v"}
		},
		"long value": func(p map[string]any) {
			p["metadata"] = map[string]string{"k": long(worker.MaxMetadataValue + 1)}
		},
		"long registration": func(p map[string]any) { p["pad"] = long(worker.MaxRegistration) },
	} {
		_, err := f.register(t, edit)
		if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || len(inv.Errors) != 1 ||
			inv.Errors[0].Pointer != "/registration" ||
			inv.Errors[0].Reason != kernel.ReasonMaxLength {
			t.Errorf("%s = %v", name, err)
		}
	}
	f.store.fail = "CA"
	if _, err := f.svc.Register(
		t.Context(),
		"garbage",
	); !errors.Is(
		err,
		worker.ErrUnauthenticated,
	) {
		t.Errorf("garbage with the CA unreadable = %v", err)
	}
}

// TestUnitWorkerRegistrationFailures checks that a failing store or
// token mint fails the registration, not as a refusal.
func TestUnitWorkerRegistrationFailures(t *testing.T) {
	for _, method := range []string{"CA", "PoolByID", "Nonce", "Worker", "CreateWorker", "Count"} {
		f := newFixture(t, worker.Deps{Limits: limits{max: 5}})
		f.store.fail = method
		_, err := f.register(t, func(p map[string]any) { p["worker"] = kernel.NewULID().String() })
		if !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", method, err)
		}
	}
	f := newFixture(t, worker.Deps{Tokens: tokens{fail: true}})
	if _, err := f.register(t, nil); !errors.Is(err, errDown) {
		t.Errorf("mint down = %v", err)
	}
	f = newFixture(t, worker.Deps{})
	f.store.ca = []byte("not DER")
	if _, err := f.register(t, nil); err == nil || errors.Is(err, worker.ErrUnauthenticated) {
		t.Errorf("a corrupt CA = %v", err)
	}
}
