package pg_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestMain migrates the platform stream and the configuration stream
// alone: no configuration table references another domain's.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func keyring(t *testing.T, db *pgxpool.Pool) *crypt.Keyring {
	t.Helper()
	p, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	k, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// space is a node of the fake tree: its parent's slug, the top space when
// empty, and whether it inherits its parent's entities.
type space struct {
	parent  string
	inherit bool
}

// spaces are an account's spaces by slug, recording each hold.
type spaces struct {
	mu    sync.Mutex
	top   kernel.ULID
	ids   map[string]kernel.ULID
	nodes map[string]space
	holds []kernel.ULID
	err   error
}

func (s *spaces) Hold(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if !slices.Contains(slices.Collect(maps.Values(s.ids)), id) {
		return accessapi.ErrNoSpace
	}
	s.holds = append(s.holds, id)
	return nil
}

func (s *spaces) Find(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if s.err != nil {
		return kernel.ULID{}, s.err
	}
	if id, ok := s.ids[slug]; ok {
		return id, nil
	}
	return kernel.ULID{}, accessapi.ErrNoSpace
}

func (s *spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := map[kernel.ULID]string{}
	for slug, id := range s.ids {
		out[id] = slug
	}
	return out, nil
}

func (s *spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []accessapi.SpaceNode
	for _, slug := range slices.Sorted(maps.Keys(s.ids)) {
		n := accessapi.SpaceNode{ID: s.ids[slug], Slug: slug, Name: strings.ToUpper(slug)}
		if s.ids[slug] != s.top {
			parent := s.nodes[slug].parent
			if parent == "" {
				parent = "acme"
			}
			n.Parent, n.Inherit = s.ids[parent], s.nodes[slug].inherit
		}
		out = append(out, n)
	}
	return out, nil
}

// stacks are an account's stacks by slug, with their labels by slug,
// recording each hold.
type stacks struct {
	mu     sync.Mutex
	all    map[string]stacksapi.Stack
	labels map[string][]string
	holds  []kernel.ULID
	err    error
}

func (s *stacks) byID(id kernel.ULID) (stacksapi.Stack, bool) {
	for _, st := range s.all {
		if st.ID == id {
			return st, true
		}
	}
	return stacksapi.Stack{}, false
}

func (s *stacks) Get(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	if s.err != nil {
		return stacksapi.Stack{}, s.err
	}
	if st, ok := s.byID(id); ok {
		return st, nil
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

func (s *stacks) BySlug(
	_ context.Context,
	_ kernel.AccountID,
	slug string,
) (stacksapi.Stack, error) {
	if s.err != nil {
		return stacksapi.Stack{}, s.err
	}
	if st, ok := s.all[slug]; ok {
		return st, nil
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

func (s *stacks) ByIDs(
	_ context.Context,
	_ kernel.AccountID,
	ids []kernel.ULID,
) (map[kernel.ULID]stacksapi.Stack, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := map[kernel.ULID]stacksapi.Stack{}
	for _, id := range ids {
		if st, ok := s.byID(id); ok {
			out[id] = st
		}
	}
	return out, nil
}

func (s *stacks) Hold(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID(id); !ok {
		return stacksapi.ErrNotFound
	}
	s.holds = append(s.holds, id)
	return nil
}

func (*stacks) Settings(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (stacksapi.Settings, error) {
	return stacksapi.Settings{}, errors.ErrUnsupported
}

func (*stacks) ByRepository(
	context.Context,
	kernel.AccountID,
	*kernel.ULID,
	string,
	string,
) ([]stacksapi.Stack, error) {
	return nil, errors.ErrUnsupported
}

func (*stacks) LockForRun(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (stacksapi.Settings, error) {
	return stacksapi.Settings{}, errors.ErrUnsupported
}

func (s *stacks) Find(
	_ context.Context,
	_ kernel.AccountID,
	spaces []kernel.ULID,
	labels []string,
) ([]stacksapi.Stack, error) {
	return s.matching(spaces, func(slug string) bool {
		return slices.ContainsFunc(
			s.labels[slug],
			func(l string) bool { return slices.Contains(labels, l) },
		)
	})
}

func (s *stacks) InSpaces(
	_ context.Context,
	_ kernel.AccountID,
	spaces []kernel.ULID,
) ([]stacksapi.Stack, error) {
	return s.matching(spaces, func(string) bool { return true })
}

// matching lists the stacks in spaces whose slug keep keeps, by slug.
func (s *stacks) matching(
	spaces []kernel.ULID,
	keep func(slug string) bool,
) ([]stacksapi.Stack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	var out []stacksapi.Stack
	for _, slug := range slices.Sorted(maps.Keys(s.all)) {
		if st := s.all[slug]; slices.Contains(spaces, st.Space) && keep(slug) {
			out = append(out, st)
		}
	}
	return out, nil
}

func (s *stacks) Labels(
	_ context.Context,
	_ kernel.AccountID,
	ids []kernel.ULID,
) (map[kernel.ULID][]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := map[kernel.ULID][]string{}
	for slug, st := range s.all {
		if slices.Contains(ids, st.ID) {
			out[st.ID] = slices.Sorted(slices.Values(s.labels[slug]))
		}
	}
	return out, nil
}

// authz allows each caller the actions it lists in each space.
type authz map[string]map[kernel.ULID][]accessapi.Action

func (a authz) Authorize(
	_ context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	return slices.Contains(a[p.Caller()][space], action), nil
}

func (a authz) Reachable(
	_ context.Context,
	p kernel.Principal,
	action accessapi.Action,
) ([]kernel.ULID, error) {
	var out []kernel.ULID
	for sp, as := range a[p.Caller()] {
		if slices.Contains(as, action) {
			out = append(out, sp)
		}
	}
	return out, nil
}

func (authz) Holds(context.Context, kernel.Principal, accessapi.Action) (bool, error) {
	return false, nil
}

func (a authz) Actions(
	_ context.Context,
	p kernel.Principal,
	sp kernel.ULID,
) ([]accessapi.Action, error) {
	return a[p.Caller()][sp], nil
}

// fixture is a service over a fresh account whose top space acme holds
// team, which inherits, and hidden, which does not; team holds app,
// which inherits, and app holds leaf, which does not. Stack web lives in
// app and stack db in hidden. The admin may do everything everywhere,
// the writer may update contexts in team and app and attach there, and
// the reader may read team and app.
type fixture struct {
	db                    *pgxpool.Pool
	keys                  *crypt.Keyring
	store                 *pg.Store
	svc                   *core.Service
	spaces                *spaces
	stacks                *stacks
	acct                  kernel.AccountID
	sp                    map[string]kernel.ULID
	web, data             stacksapi.Stack
	admin, writer, reader kernel.Principal
	authz                 authz
	now                   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := pool(t)
	kinds := outbox.NewKinds()
	if err := kinds.Register(configapi.ChangedKind, configapi.ChangedVersion,
		configapi.ChangedSchema); err != nil {
		t.Fatal(err)
	}
	acct := kernel.NewAccountID()
	sp := map[string]kernel.ULID{
		"acme": acct.ULID, "team": kernel.NewULID(), "hidden": kernel.NewULID(),
		"app": kernel.NewULID(), "leaf": kernel.NewULID(),
	}
	tree := &spaces{top: acct.ULID, ids: sp, nodes: map[string]space{
		"team": {inherit: true}, "hidden": {}, "app": {parent: "team", inherit: true},
		"leaf": {parent: "app"},
	}}
	web := stacksapi.Stack{
		ID:      kernel.NewULID(),
		Account: acct,
		Slug:    "web",
		Name:    "Web",
		Space:   sp["app"],
	}
	data := stacksapi.Stack{
		ID:      kernel.NewULID(),
		Account: acct,
		Slug:    "db",
		Name:    "DB",
		Space:   sp["hidden"],
	}
	st := &stacks{
		all:    map[string]stacksapi.Stack{"web": web, "db": data},
		labels: map[string][]string{},
	}
	principal := func(login string) kernel.Principal {
		return kernel.Principal{
			Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(), AccountID: acct, Login: login,
		}
	}
	admin, writer, reader := principal("admin"), principal("writer"), principal("reader")
	all := []accessapi.Action{
		accessapi.ActionSpaceRead, accessapi.ActionContextCreate, accessapi.ActionContextUpdate,
		accessapi.ActionContextDelete, accessapi.ActionStackContextAttach,
	}
	write := []accessapi.Action{
		accessapi.ActionSpaceRead,
		accessapi.ActionContextUpdate,
		accessapi.ActionStackContextAttach,
	}
	az := authz{
		admin.Caller():  {},
		writer.Caller(): {sp["team"]: write, sp["app"]: write},
		reader.Caller(): {
			sp["team"]: {accessapi.ActionSpaceRead},
			sp["app"]:  {accessapi.ActionSpaceRead},
		},
	}
	for _, id := range sp {
		az[admin.Caller()][id] = all
	}
	keys := keyring(t, db)
	store := pg.New(db, keys)
	f := &fixture{
		db: db, keys: keys, store: store, spaces: tree, stacks: st, acct: acct, sp: sp,
		web: web, data: data, admin: admin, writer: writer, reader: reader, authz: az,
		now: time.Unix(1_800_000_000, 0),
	}
	f.svc = core.New(core.Deps{
		Store: store, DB: db, Spaces: tree, Stacks: st, Authz: az,
		Events: outbox.New(db, queue.New(db), kinds), Now: func() time.Time { return f.now },
	})
	return f
}

// changes are the Changed events of context id, in order.
func (f *fixture) changes(t *testing.T, id kernel.ULID) []string {
	t.Helper()
	rows, err := f.db.Query(t.Context(), `SELECT document->>'change' FROM events
		WHERE account_id = $1 AND kind = $2 AND subject_id = $3 ORDER BY sequence`,
		f.acct, configapi.ChangedKind, id.String())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// create creates a context named name in space as the admin, failing the
// test on a refusal.
func (f *fixture) create(
	t *testing.T,
	name, space string,
	config ...core.ElementInput,
) core.Context {
	t.Helper()
	c, err := f.svc.Create(
		t.Context(),
		f.admin,
		core.Input{Space: space, Name: name, Config: config},
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// variable is a plain environment variable named name holding value.
func variable(name, value string) core.ElementInput {
	return core.ElementInput{Name: name, Value: &value}
}

// pointers are the refused fields' pointers of err, a *kernel.Invalid.
func pointers(t *testing.T, err error) []string {
	t.Helper()
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		t.Fatalf("err = %v, want *kernel.Invalid", err)
	}
	out := make([]string, 0, len(inv.Errors))
	for _, e := range inv.Errors {
		out = append(out, e.Pointer)
	}
	return out
}

// missing reports whether err is a *core.Missing of kind.
func missing(err error, kind string) bool {
	m, ok := errors.AsType[*core.Missing](err)
	return ok && m.Kind == kind
}

// sum is v's SHA-256 in hex, as a checksum reads.
func sum(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}
