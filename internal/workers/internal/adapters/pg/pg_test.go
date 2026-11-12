package pg_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/credential"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// TestMain migrates the platform stream and the workers stream alone: no
// workers table references another domain's.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

var errDown = errors.New("down")

// spaces is an account's tree: acme on top, team inheriting from it, and
// hidden, which does not. err fails every call when set.
type spaces struct {
	nodes []accessapi.SpaceNode
	err   error
}

func (s spaces) Hold(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	if s.err != nil {
		return s.err
	}
	if !slices.ContainsFunc(s.nodes, func(n accessapi.SpaceNode) bool { return n.ID == id }) {
		return accessapi.ErrNoSpace
	}
	return nil
}

func (s spaces) Find(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if s.err != nil {
		return kernel.ULID{}, s.err
	}
	for _, n := range s.nodes {
		if n.Slug == slug {
			return n.ID, nil
		}
	}
	return kernel.ULID{}, accessapi.ErrNoSpace
}

func (s spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	out := map[kernel.ULID]string{}
	for _, n := range s.nodes {
		out[n.ID] = n.Slug
	}
	return out, s.err
}

func (s spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return s.nodes, s.err
}

// authz grants each login its actions in its spaces; err fails every
// call when set.
type authz struct {
	accessapi.Authorizer
	grants map[string]map[kernel.ULID][]accessapi.Action
	err    error
}

func (a authz) Authorize(
	_ context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	return slices.Contains(a.grants[p.Login][space], action), a.err
}

func (a authz) Reachable(
	_ context.Context,
	p kernel.Principal,
	action accessapi.Action,
) ([]kernel.ULID, error) {
	var out []kernel.ULID
	for space, actions := range a.grants[p.Login] {
		if slices.Contains(actions, action) {
			out = append(out, space)
		}
	}
	return out, a.err
}

type fixture struct {
	db                   *pgxpool.Pool
	keys                 *crypt.Keyring
	store                *pg.Store
	deps                 pool.Deps
	svc                  *pool.Service
	org                  kernel.OrganizationID
	acct                 kernel.AccountID
	admin, reader, mover kernel.Principal
	team, hidden         kernel.ULID
	tree                 spaces
	grants               map[string]map[kernel.ULID][]accessapi.Action
	csr                  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	p, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		db: db, keys: keys, org: kernel.NewOrganizationID(), acct: kernel.NewAccountID(),
		team:   kernel.NewULID(),
		hidden: kernel.NewULID(), csr: request(t),
	}
	f.tree = spaces{nodes: []accessapi.SpaceNode{
		{ID: f.acct.ULID, Slug: "acme"},
		{ID: f.team, Slug: "team", Parent: f.acct.ULID, Inherit: true},
		{ID: f.hidden, Slug: "hidden", Parent: f.acct.ULID},
	}}
	principal := func(login string) kernel.Principal {
		return kernel.Principal{
			Kind:           kernel.PrincipalAPIKey,
			ActorID:        kernel.NewULID(),
			OrganizationID: f.org,
			AccountID:      f.acct,
			Login:          login,
		}
	}
	f.admin, f.reader, f.mover = principal("admin"), principal("reader"), principal("mover")
	all := []accessapi.Action{
		accessapi.ActionSpaceRead,
		accessapi.ActionWorkerPoolCreate,
		accessapi.ActionWorkerPoolUpdate,
	}
	read := []accessapi.Action{accessapi.ActionSpaceRead}
	f.grants = map[string]map[kernel.ULID][]accessapi.Action{
		"admin":  {f.acct.ULID: all, f.team: all, f.hidden: all},
		"reader": {f.acct.ULID: read, f.team: read},
		"mover":  {f.acct.ULID: all, f.team: read},
	}
	f.store = pg.New(db, keys)
	f.deps = pool.Deps{
		Store: f.store, DB: db, Spaces: f.tree, Authz: authz{grants: f.grants},
		Public: "https://infraweave.example/",
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	f.svc = pool.New(f.deps)
	return f
}

// request is base64 of a new P-256 key's certificate signing request.
func request(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "pool"}}, k)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func ptr[T any](v T) *T { return &v }

// TestAccPools creates, reads, changes, moves, resets and deletes a pool
// through the service over the store.
func TestAccPools(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{
		Name: " Builders ", Space: "team", Description: "ours", Labels: []string{"eu"},
		PreparingTimeoutSeconds: ptr[int32](60), DriftDetectionRunLimit: ptr[int32](-1),
	}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Builders" || p.Space != f.team || p.Version != 1 || p.Serial < 2 ||
		f.svc.PreparingTimeout(p) != time.Minute {
		t.Errorf("Create = %+v", p)
	}
	got, err := f.svc.Read(ctx, f.acct, p.ID.String())
	if err != nil || got.Description != "ours" || *got.DriftDetectionRunLimit != -1 ||
		!bytes.Equal(got.Certificate, p.Certificate) || got.PreparingTimeout != time.Minute {
		t.Errorf("Read = %+v, %v", got, err)
	}
	tok, cert, err := credential.DecodeToken(f.svc.Token(got))
	if err != nil || tok.Endpoint != "https://infraweave.example" || tok.Pool != p.ID.String() ||
		cert.SerialNumber.Int64() != p.Serial {
		t.Errorf("Token = %+v, %v", tok, err)
	}
	if space, err := f.svc.SpaceOf(ctx, f.acct, p.ID.String()); err != nil || space != f.team {
		t.Errorf("SpaceOf = %s, %v", space, err)
	}

	up, err := f.svc.Update(ctx, f.admin, p.ID.String(), kernel.Expect{}, pool.Input{
		Name: "Runners", Space: "acme",
	})
	if err != nil || up.Name != "Runners" || up.Space != f.acct.ULID || up.Version != 2 ||
		up.Description != "" || up.PreparingTimeout != 0 || up.Serial != p.Serial ||
		f.svc.PreparingTimeout(up) != pool.DefaultPreparingTimeout {
		t.Errorf("Update = %+v, %v", up, err)
	}
	reset, err := f.svc.Reset(ctx, f.admin, p.ID.String(), request(t))
	if err != nil || reset.Serial == p.Serial || reset.Version != 3 ||
		bytes.Equal(reset.Certificate, p.Certificate) {
		t.Errorf("Reset = %+v, %v", reset, err)
	}
	if _, err := f.svc.Update(ctx, f.admin, p.ID.String(), kernel.ExpectVersions(1),
		pool.Input{Name: "Stale"}); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("Update at a stale version = %v", err)
	}

	gone, err := f.svc.Delete(ctx, f.admin, p.ID.String(), kernel.ExpectVersions(3))
	if err != nil || gone.ID != p.ID {
		t.Errorf("Delete = %+v, %v", gone, err)
	}
	if _, err := f.svc.Read(ctx, f.acct, p.ID.String()); !errors.Is(err, pool.ErrNotFound) {
		t.Errorf("Read after Delete = %v", err)
	}
	if _, err := f.svc.SpaceOf(
		ctx,
		f.acct,
		p.ID.String(),
	); !errors.Is(
		err,
		accessapi.ErrNoSubject,
	) {
		t.Errorf("SpaceOf after Delete = %v", err)
	}
	if _, err := f.svc.Delete(
		ctx,
		f.admin,
		"nope",
		kernel.Expect{},
	); !errors.Is(
		err,
		pool.ErrNotFound,
	) {
		t.Errorf("Delete of nope = %v", err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, kernel.NewULID().String(),
		kernel.Expect{}); !errors.Is(err, pool.ErrNotFound) {
		t.Errorf("Delete of a missing pool = %v", err)
	}
}

// TestAccPoolRefusals checks each input and authorization the service
// refuses.
func TestAccPoolRefusals(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders", Space: "acme"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	invalid := func(name string, err error, pointer string) {
		t.Helper()
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) == 0 || inv.Errors[0].Pointer != pointer {
			t.Errorf("%s = %v, want a refusal at %s", name, err, pointer)
		}
	}
	_, err = f.svc.Create(ctx, f.admin, pool.Input{Name: ""}, f.csr)
	invalid("Create with no name", err, "/name")
	_, err = f.svc.Create(
		ctx,
		f.admin,
		pool.Input{Name: "x", PreparingTimeoutSeconds: ptr[int32](0)},
		f.csr,
	)
	invalid("Create with no timeout", err, "/preparingTimeoutSeconds")
	_, err = f.svc.Create(ctx, f.admin, pool.Input{Name: "x"}, "nope")
	invalid("Create with no request", err, "/certificateSigningRequest")
	_, err = f.svc.Reset(ctx, f.admin, p.ID.String(), "nope")
	invalid("Reset with no request", err, "/certificateSigningRequest")
	_, err = f.svc.Update(ctx, f.admin, p.ID.String(), kernel.Expect{}, pool.Input{})
	invalid("Update with no name", err, "/name")

	if _, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders", Space: "team"},
		f.csr); !errors.Is(err, pool.ErrNameTaken) {
		t.Errorf("Create of a taken name = %v", err)
	}
	other, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Other"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(ctx, f.admin, other.ID.String(), kernel.Expect{},
		pool.Input{Name: "Builders"}); !errors.Is(err, pool.ErrNameTaken) {
		t.Errorf("Update to a taken name = %v", err)
	}
	for name, space := range map[string]string{"nowhere": "nowhere", "hidden": "hidden"} {
		_, err := f.svc.Create(ctx, f.reader, pool.Input{Name: "y", Space: space}, f.csr)
		if m, ok := errors.AsType[*pool.Missing](err); !ok || m.Kind != "space" || m.Error() == "" {
			t.Errorf("Create in %s = %v", name, err)
		}
	}
	_, err = f.svc.Update(ctx, f.mover, p.ID.String(), kernel.Expect{},
		pool.Input{Name: "Builders", Space: "team"})
	if d, ok := errors.AsType[*pool.Denied](err); !ok ||
		d.Action != accessapi.ActionWorkerPoolCreate || d.Error() == "" {
		t.Errorf("a move to a space without WORKER_POOL_CREATE = %v", err)
	}

	tree := f.tree
	tree.nodes = append(
		slices.Clone(tree.nodes),
		accessapi.SpaceNode{ID: kernel.NewULID(), Slug: "gone"},
	)
	deps := f.deps
	deps.Spaces = spaces{nodes: tree.nodes[:3]}
	deps.Spaces = findOnly{spaces: deps.Spaces.(spaces), extra: tree.nodes[3]}
	if _, err := pool.New(deps).Create(ctx, f.admin, pool.Input{Name: "z", Space: "gone"},
		f.csr); !isMissing(err) {
		t.Errorf("Create in a space deleted meanwhile = %v", err)
	}
}

// findOnly finds extra, which Hold no longer finds, as a space deleted
// between the two.
type findOnly struct {
	spaces
	extra accessapi.SpaceNode
}

func (s findOnly) Find(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	if slug == s.extra.Slug {
		return s.extra.ID, nil
	}
	return s.spaces.Find(ctx, acct, slug)
}

func isMissing(err error) bool { _, ok := errors.AsType[*pool.Missing](err); return ok }

// TestAccPoolFailures checks that a failing access domain fails each
// call that reaches it, not as a refusal.
func TestAccPoolFailures(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	for name, deps := range map[string]pool.Deps{
		"spaces": func() pool.Deps { d := f.deps; d.Spaces = spaces{nodes: f.tree.nodes, err: errDown}; return d }(),
		"authz": func() pool.Deps {
			d := f.deps
			d.Authz = authz{grants: f.grants, err: errDown}
			return d
		}(),
	} {
		svc := pool.New(deps)
		for call, err := range map[string]error{
			"Create": func() error {
				_, err := svc.Create(ctx, f.admin, pool.Input{Name: "x", Space: "team"}, f.csr)
				return err
			}(),
			"Update": func() error {
				_, err := svc.Update(ctx, f.admin, p.ID.String(), kernel.Expect{},
					pool.Input{Name: "x", Space: "team"})
				return err
			}(),
			"Search":  func() error { _, err := svc.Search(ctx, f.admin, search.Input{}); return err }(),
			"Suggest": func() error { _, err := svc.Suggest(ctx, f.admin, search.SuggestionsInput{}); return err }(),
		} {
			if !errors.Is(err, errDown) {
				t.Errorf("%s with %s down = %v", call, name, err)
			}
		}
	}
	held := spaces{nodes: f.tree.nodes}
	holdDown := pool.Deps(f.deps)
	holdDown.Spaces = holdFails{held}
	if _, err := pool.New(holdDown).Create(ctx, f.admin, pool.Input{Name: "x"},
		f.csr); !errors.Is(err, errDown) {
		t.Errorf("Create with Hold down = %v", err)
	}
	moveDown := f.deps
	moveDown.Authz = moveFails{authz{grants: f.grants}}
	if _, err := pool.New(moveDown).Update(ctx, f.admin, p.ID.String(), kernel.Expect{},
		pool.Input{Name: "Builders", Space: "team"}); !errors.Is(err, errDown) {
		t.Errorf("a move with WORKER_POOL_CREATE's check down = %v", err)
	}
}

// holdFails fails Hold alone.
type holdFails struct{ spaces }

func (holdFails) Hold(context.Context, kernel.AccountID, kernel.ULID) error { return errDown }

// moveFails fails the check of WORKER_POOL_CREATE alone.
type moveFails struct{ authz }

func (m moveFails) Authorize(
	ctx context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	if action == accessapi.ActionWorkerPoolCreate {
		return false, errDown
	}
	return m.authz.Authorize(ctx, p, action, space)
}

// TestAccAuthority checks that concurrent creates on an installation
// with no pool CA make one CA, every pool's certificate chaining to it
// under a serial of its own.
func TestAccAuthority(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	const n = 8
	pools := make([]pool.Pool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			pools[i], errs[i] = f.svc.Create(ctx, f.admin,
				pool.Input{Name: kernel.NewULID().String()}, f.csr)
		})
	}
	wg.Wait()
	var certDER, sealed []byte
	var cas int
	if err := f.db.QueryRow(ctx, `SELECT count(*) OVER (), certificate, private_key_sealed
		FROM worker_pool_ca`).Scan(&cas, &certDER, &sealed); err != nil || cas != 1 {
		t.Fatalf("pool CAs: %d, %v", cas, err)
	}
	key, err := f.keys.OpenColumn(ctx, pg.SealedColumns()[0], []string{"1"}, sealed)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := credential.ParseCA(certDER, key.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	serials := map[int64]bool{}
	for i, p := range pools {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		cert, err := x509.ParseCertificate(p.Certificate)
		if err != nil {
			t.Fatal(err)
		}
		if err := ca.Verify(cert, p.ID, time.Now()); err != nil {
			t.Errorf("pool %d: %v", i, err)
		}
		serials[p.Serial] = true
	}
	if len(serials) != n {
		t.Errorf("%d serials for %d pools", len(serials), n)
	}

	mintDown := pg.New(f.db, f.keys)
	if _, err := f.db.Exec(ctx, `DELETE FROM worker_pool_ca`); err != nil {
		t.Fatal(err)
	}
	if _, err := mintDown.Authority(ctx, func() (pool.Authority, error) {
		return pool.Authority{}, errDown
	}); !errors.Is(err, errDown) {
		t.Errorf("Authority with a failing mint = %v", err)
	}
	if _, err := pg.New(f.db, sealFails{f.keys}).Authority(ctx, func() (pool.Authority, error) {
		return pool.Authority{Certificate: []byte("c"), Key: []byte("k")}, nil
	}); !errors.Is(err, errDown) {
		t.Errorf("Authority with a failing seal = %v", err)
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO worker_pool_ca
		(id, certificate, private_key_sealed, next_serial) VALUES (1, 'c', 'not sealed', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Authority(ctx, nil); err == nil {
		t.Error("Authority opened a key that is not sealed")
	}
	if _, err := f.db.Exec(
		ctx,
		`UPDATE worker_pool_ca SET private_key_sealed = $1`,
		sealed,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE worker_pool_ca SET certificate = 'not DER'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Broken"}, f.csr); err == nil {
		t.Error("Create signed under a CA whose certificate does not parse")
	}
}

// sealFails fails every seal.
type sealFails struct{ pg.Sealer }

func (sealFails) SealColumn(context.Context, crypt.Column, []string, []byte) ([]byte, error) {
	return nil, errDown
}

// TestAccPoolSearch searches and suggests the pools in the spaces a
// caller reads.
func TestAccPoolSearch(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for _, in := range []pool.Input{
		{Name: "Top", Labels: []string{"eu"}},
		{Name: "Ours", Space: "team", Labels: []string{"eu", "prod"}},
		{Name: "Hidden", Space: "hidden"},
	} {
		if _, err := f.svc.Create(ctx, f.admin, in, f.csr); err != nil {
			t.Fatal(err)
		}
	}
	names := func(p kernel.Principal) []string {
		t.Helper()
		res, err := f.svc.Search(ctx, p, search.Input{})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(res.Edges))
		for _, e := range res.Edges {
			out = append(out, e.Node.Name)
		}
		return out
	}
	if got := names(f.admin); !slices.Equal(got, []string{"Hidden", "Ours", "Top"}) {
		t.Errorf("the admin finds %v", got)
	}
	if got := names(f.reader); !slices.Equal(got, []string{"Ours", "Top"}) {
		t.Errorf("the reader finds %v", got)
	}
	s, err := f.svc.Suggest(ctx, f.reader, search.SuggestionsInput{Fields: []string{"label"}})
	if err != nil || s.Count != 2 {
		t.Errorf("Suggest = %+v, %v", s, err)
	}
	if _, err := f.svc.Search(ctx, f.reader, search.Input{After: "nope"}); err == nil {
		t.Error("a bad cursor searched")
	}
}

// TestAccPoolPurgeAndGuard deletes a pool with its workers and nonces but
// not its samples, purges a deleted account's samples, runner images,
// pools, workers and nonces a batch at a time, and reports the spaces
// pools live in.
func TestAccPoolPurgeAndGuard(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	ids := make([]kernel.ULID, 0, 2)
	for _, name := range []string{"a", "b"} {
		p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: name, Space: "team"}, f.csr)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
		if _, err := f.db.Exec(ctx, `INSERT INTO workers
			(account_id, organization_id, id, pool_id, serial) VALUES ($1, $2, $3, $4, $5)`,
			f.acct, f.org, kernel.NewULID(), p.ID, p.Serial); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(
			ctx,
			`INSERT INTO worker_nonces (account_id, pool_id, jti, expires_at)
			VALUES ($1, $2, 'j', now())`,
			f.acct,
			p.ID,
		); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if err := f.db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE account_id = $1`,
			f.acct).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n, err := f.store.Sample(ctx, time.Now(), nil); err != nil || n != 2 {
		t.Fatalf("Sample = %d, %v", n, err)
	}
	guard := f.svc.SpaceGuard()
	if in, err := guard.SpaceInUse(ctx, f.acct, f.team); err != nil || !in {
		t.Errorf("SpaceInUse(team) = %t, %v", in, err)
	}
	if in, err := guard.SpaceInUse(ctx, f.acct, f.hidden); err != nil || in {
		t.Errorf("SpaceInUse(hidden) = %t, %v", in, err)
	}
	if lost, err := guard.LostOnMove(ctx, accessapi.SpaceMove{}); err != nil || lost != nil {
		t.Errorf("LostOnMove = %v, %v", lost, err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, ids[0].String(), kernel.Expect{}); err != nil {
		t.Fatal(err)
	}
	if count("workers") != 1 || count("worker_nonces") != 1 || count("worker_pools") != 1 ||
		count("worker_pool_samples") != 2 {
		t.Errorf("after a delete: %d workers, %d nonces, %d pools, %d samples",
			count("workers"), count("worker_nonces"), count("worker_pools"),
			count("worker_pool_samples"))
	}
	if err := f.store.SetRunnerImages(ctx, f.acct, images.Images{Private: ptr("x")}); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		done, err := f.svc.Purge(ctx, f.acct, 1)
		if err != nil || done {
			t.Fatalf("Purge = %t, %v", done, err)
		}
	}
	if done, err := f.svc.Purge(ctx, f.acct, 1); err != nil || !done {
		t.Errorf("the last Purge = %t, %v", done, err)
	}
}

// TestAccUnavailable checks that every read and write on a closed pool
// fails, and not as a missing row.
func TestAccUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Close()
	s, acct := f.store, f.acct
	for name, err := range map[string]error{
		"Authority": func() error {
			_, err := s.Authority(ctx, nil)
			return err
		}(),
		"Create":  s.Create(ctx, p),
		"Write":   s.Write(ctx, p),
		"Get":     func() error { _, err := s.Get(ctx, acct, p.ID, true); return err }(),
		"Delete":  s.Delete(ctx, acct, p.ID),
		"InSpace": func() error { _, err := s.InSpace(ctx, acct, p.ID); return err }(),
		"Purge":   func() error { _, err := s.Purge(ctx, acct, 1); return err }(),
		"Sample":  func() error { _, err := s.Sample(ctx, time.Now(), nil); return err }(),
		"Prune":   func() error { _, err := s.Prune(ctx, time.Now(), 1); return err }(),
		"Between": func() error {
			_, err := s.Between(ctx, acct, p.ID, time.Time{}, time.Now())
			return err
		}(),
		"Search": func() error { _, err := s.Search(ctx, acct, search.Input{}, pool.Scope{}); return err }(),
		"Suggest": func() error {
			_, err := s.Suggest(ctx, acct, search.SuggestionsInput{}, pool.Scope{})
			return err
		}(),
		"Service.SpaceOf": func() error { _, err := f.svc.SpaceOf(ctx, acct, p.ID.String()); return err }(),
	} {
		if err == nil || errors.Is(err, pool.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
	if _, err := s.Share(ctx, acct, p.ID); err == nil || errors.Is(err, pool.ErrNotFound) {
		t.Errorf("Share on a closed pool = %v", err)
	}
	if len(pg.Held()) != 8 || len(pg.Keys()) != 1 || len(pg.SealedColumns()) != 1 {
		t.Errorf("held %v, keys %v, sealed %v", pg.Held(), pg.Keys(), pg.SealedColumns())
	}
}

// TestAccWriteMissing checks that writing a pool the account lacks is
// pool.ErrNotFound.
func TestAccWriteMissing(t *testing.T) {
	f := newFixture(t)
	err := f.store.Write(t.Context(), pool.Pool{
		Account: f.acct, ID: kernel.NewULID(), Labels: []string{}, Certificate: []byte{},
	})
	if !errors.Is(err, pool.ErrNotFound) {
		t.Errorf("Write of a missing pool = %v", err)
	}
}

// guard reports uses as the stacks that name every pool, failing when
// err is set.
type guard struct {
	uses []workersapi.PoolUse
	err  error
}

func (g guard) PoolInUse(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) ([]workersapi.PoolUse, error) {
	return g.uses, g.err
}

// TestAccHold checks that a space holds the pools of the spaces it
// reaches through inheriting spaces, and no other.
func TestAccHold(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	top, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Top"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	team, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Team", Space: "team"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		space, id kernel.ULID
		ok        bool
	}{
		"its own space's":            {space: f.team, id: team.ID, ok: true},
		"an inherited space's":       {space: f.team, id: top.ID, ok: true},
		"a space that inherits none": {space: f.hidden, id: top.ID},
		"a space below's":            {space: f.acct.ULID, id: team.ID},
		"a missing pool":             {space: f.team, id: kernel.NewULID()},
	} {
		err := f.svc.Hold(ctx, f.acct, tc.space, tc.id)
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, workersapi.ErrNoPool) {
			t.Errorf("Hold of %s = %v", name, err)
		}
	}
	deps := f.deps
	deps.Spaces = spaces{nodes: f.tree.nodes, err: errDown}
	if err := pool.New(deps).Hold(ctx, f.acct, f.team, top.ID); !errors.Is(err, errDown) {
		t.Errorf("Hold with spaces down = %v", err)
	}
}

// TestAccAttachable lists the pools a stack or a space reaches, among the
// spaces the caller reads.
func TestAccAttachable(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for _, in := range []pool.Input{
		{Name: "Top"}, {Name: "Team", Space: "team"}, {Name: "Hidden", Space: "hidden"},
	} {
		if _, err := f.svc.Create(ctx, f.admin, in, f.csr); err != nil {
			t.Fatal(err)
		}
	}
	names := func(res search.Result[pool.Pool], err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(res.Edges))
		for _, e := range res.Edges {
			out = append(out, e.Node.Name)
		}
		return out
	}
	for _, tc := range []struct {
		who   kernel.Principal
		space string
		want  []string
	}{
		{f.admin, "team", []string{"Team", "Top"}},
		{f.admin, "hidden", []string{"Hidden"}},
		{f.admin, "acme", []string{"Top"}},
		{f.reader, "team", []string{"Team", "Top"}},
		{f.reader, "hidden", nil},
	} {
		got := names(f.svc.AttachableInSpace(ctx, tc.who, tc.space, search.Input{}))
		if !slices.Equal(got, tc.want) {
			t.Errorf(
				"%s's pools attachable in %s = %v, want %v",
				tc.who.Login,
				tc.space,
				got,
				tc.want,
			)
		}
	}
	if _, err := f.svc.AttachableInSpace(ctx, f.admin, "nowhere", search.Input{}); !isMissing(err) {
		t.Errorf("AttachableInSpace(nowhere) = %v", err)
	}
	if _, err := f.svc.AttachableToStack(ctx, f.admin, "infra", search.Input{}); !isMissing(err) {
		t.Errorf("AttachableToStack before a binding = %v", err)
	}
	f.svc.BindStacks(func(_ context.Context, _ kernel.AccountID, id string) (kernel.ULID, error) {
		switch id {
		case "gone":
			return kernel.ULID{}, accessapi.ErrNoSubject
		case "down":
			return kernel.ULID{}, errDown
		}
		return f.team, nil
	})
	if got := names(
		f.svc.AttachableToStack(ctx, f.admin, "infra", search.Input{}),
	); !slices.Equal(
		got,
		[]string{"Team", "Top"},
	) {
		t.Errorf("the pools attachable to a stack in team = %v", got)
	}
	if _, err := f.svc.AttachableToStack(ctx, f.admin, "gone", search.Input{}); !isMissing(err) {
		t.Errorf("AttachableToStack(gone) = %v", err)
	}
	if _, err := f.svc.AttachableToStack(
		ctx,
		f.admin,
		"down",
		search.Input{},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("AttachableToStack with stacks down = %v", err)
	}
	deps := f.deps
	deps.Spaces = spaces{nodes: f.tree.nodes, err: errDown}
	if _, err := pool.New(deps).AttachableInSpace(ctx, f.admin, "team",
		search.Input{}); !errors.Is(err, errDown) {
		t.Errorf("AttachableInSpace with spaces down = %v", err)
	}
	deps = f.deps
	deps.Authz = authz{grants: f.grants, err: errDown}
	svc := pool.New(deps)
	svc.BindStacks(func(context.Context, kernel.AccountID, string) (kernel.ULID, error) {
		return f.team, nil
	})
	if _, err := svc.AttachableToStack(
		ctx,
		f.admin,
		"infra",
		search.Input{},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("AttachableToStack with authz down = %v", err)
	}
}

// TestAccInUse checks that a pool a stack names refuses its delete, and a
// move that leaves the stack out of reach, naming the stacks.
func TestAccInUse(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Top"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.Guard(guard{uses: []workersapi.PoolUse{
		{Stack: "web", Space: f.team}, {Stack: "api", Space: f.acct.ULID},
	}})
	f.svc.Guard(guard{})
	_, err = f.svc.Delete(ctx, f.admin, p.ID.String(), kernel.Expect{})
	if u, ok := errors.AsType[*pool.InUse](
		err,
	); !ok ||
		!slices.Equal(u.Stacks, []string{"api", "web"}) ||
		u.Error() == "" {
		t.Errorf("Delete of a pool in use = %v", err)
	}
	_, err = f.svc.Update(ctx, f.admin, p.ID.String(), kernel.Expect{},
		pool.Input{Name: "Top", Space: "team"})
	if u, ok := errors.AsType[*pool.InUse](err); !ok || !slices.Equal(u.Stacks, []string{"api"}) {
		t.Errorf("a move out of api's reach = %v", err)
	}

	other := pool.New(f.deps)
	other.Guard(guard{uses: []workersapi.PoolUse{{Stack: "web", Space: f.team}}})
	if _, err := other.Update(ctx, f.admin, p.ID.String(), kernel.Expect{},
		pool.Input{Name: "Top", Space: "team"}); err != nil {
		t.Errorf("a move within web's reach = %v", err)
	}
	failing := pool.New(f.deps)
	failing.Guard(guard{err: errDown})
	if _, err := failing.Delete(
		ctx,
		f.admin,
		p.ID.String(),
		kernel.Expect{},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("Delete with the guard down = %v", err)
	}
	deps := f.deps
	deps.Spaces = treeFails{spaces{nodes: f.tree.nodes}}
	moving := pool.New(deps)
	moving.Guard(guard{})
	if _, err := moving.Update(ctx, f.admin, p.ID.String(), kernel.Expect{},
		pool.Input{Name: "Top", Space: "acme"}); !errors.Is(err, errDown) {
		t.Errorf("a move with the tree down = %v", err)
	}
}

// treeFails fails Tree alone.
type treeFails struct{ spaces }

func (treeFails) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return nil, errDown
}

// TestAccRunnerImages sets and reads an account's default runner images.
func TestAccRunnerImages(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	img := images.New(f.store)
	if got, err := img.Private(ctx, f.acct); err != nil || got != images.DefaultPrivate {
		t.Errorf("Private with none set = %q, %v", got, err)
	}
	set, err := img.Set(
		ctx,
		f.acct,
		images.Images{Private: ptr(" ghcr.io/acme/runner:1 "), Public: ptr("")},
	)
	if err != nil || *set.Private != "ghcr.io/acme/runner:1" || set.Public != nil {
		t.Errorf("Set = %+v, %v", set, err)
	}
	if got, err := img.Private(ctx, f.acct); err != nil || got != "ghcr.io/acme/runner:1" {
		t.Errorf("Private = %q, %v", got, err)
	}
	if _, err := img.Set(ctx, f.acct, images.Images{}); err != nil {
		t.Fatal(err)
	}
	if got, err := img.Private(ctx, f.acct); err != nil || got != images.DefaultPrivate {
		t.Errorf("Private once cleared = %q, %v", got, err)
	}
	_, err = img.Set(ctx, f.acct, images.Images{
		Private: ptr("two words"), Public: ptr(strings.Repeat("x", 256)),
	})
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok || len(inv.Errors) != 2 || inv.Errors[0].Pointer != "/privateWorkerPoolRunnerImage" ||
		inv.Errors[1].Reason != kernel.ReasonMaxLength {
		t.Errorf("Set of refused images = %v", err)
	}
	f.db.Close()
	if _, err := img.Private(ctx, f.acct); err == nil {
		t.Error("Private read a closed pool")
	}
	if _, err := img.Set(ctx, f.acct, images.Images{}); err == nil {
		t.Error("Set wrote a closed pool")
	}
}
