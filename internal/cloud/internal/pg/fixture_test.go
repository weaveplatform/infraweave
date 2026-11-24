package pg_test

import (
	"context"
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
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestMain migrates the platform stream and the cloud stream alone: no
// cloud table references another domain's.
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

// errBroken is the failure a broken fake answers.
var errBroken = errors.New("broken")

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
// recording each hold. The methods the cloud domain never calls are the
// embedded interface's, nil.
type stacks struct {
	stacksapi.Stacks
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

func (s *stacks) Get(_ context.Context, _ kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error) {
	if s.err != nil {
		return stacksapi.Stack{}, s.err
	}
	if st, ok := s.byID(id); ok {
		return st, nil
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

func (s *stacks) BySlug(_ context.Context, _ kernel.AccountID, slug string) (stacksapi.Stack, error) {
	if s.err != nil {
		return stacksapi.Stack{}, s.err
	}
	if st, ok := s.all[slug]; ok {
		return st, nil
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

func (s *stacks) ByIDs(_ context.Context, _ kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]stacksapi.Stack, error) {
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

func (s *stacks) Find(_ context.Context, _ kernel.AccountID, spaces []kernel.ULID, labels []string) ([]stacksapi.Stack, error) {
	return s.matching(spaces, func(slug string) bool {
		return slices.ContainsFunc(s.labels[slug],
			func(l string) bool { return slices.Contains(labels, l) })
	})
}

func (s *stacks) InSpaces(_ context.Context, _ kernel.AccountID, spaces []kernel.ULID) ([]stacksapi.Stack, error) {
	return s.matching(spaces, func(string) bool { return true })
}

// matching lists the stacks in spaces whose slug keep keeps, by slug.
func (s *stacks) matching(spaces []kernel.ULID, keep func(slug string) bool) ([]stacksapi.Stack, error) {
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

func (s *stacks) Labels(_ context.Context, _ kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID][]string, error) {
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

func (a authz) Authorize(_ context.Context, p kernel.Principal, action accessapi.Action, space kernel.ULID) (bool, error) {
	return slices.Contains(a[p.Caller()][space], action), nil
}

func (a authz) Reachable(_ context.Context, p kernel.Principal, action accessapi.Action) ([]kernel.ULID, error) {
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

func (a authz) Actions(_ context.Context, p kernel.Principal, sp kernel.ULID) ([]accessapi.Action, error) {
	return a[p.Caller()][sp], nil
}

// accounts name every account acme.
type accounts struct{ err error }

func (a accounts) Slug(context.Context, kernel.AccountID) (string, error) { return "acme", a.err }

// roles is the installation's AWS identity, account 111111111111: it
// records each assumption, and refuses one of a role refused lists with
// an external ID it does not trust.
type roles struct {
	mu      sync.Mutex
	none    bool
	err     error
	trusted map[string][]string
	assumed []core.Assumption
}

func (r *roles) CallerAccount(context.Context) (string, error) {
	switch {
	case r.none:
		return "", core.ErrUnsupported
	case r.err != nil:
		return "", r.err
	}
	return "111111111111", nil
}

func (r *roles) Assume(_ context.Context, a core.Assumption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.none:
		return core.ErrUnsupported
	case r.err != nil:
		return r.err
	}
	r.assumed = append(r.assumed, a)
	if ids, ok := r.trusted[a.RoleARN]; ok && !slices.Contains(ids, a.ExternalID) {
		return &core.Refused{Message: "not authorized to perform sts:AssumeRole"}
	}
	return nil
}

// externalIDs are the external IDs of each assumption, in order.
func (r *roles) externalIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.assumed))
	for n, a := range r.assumed {
		out[n] = a.ExternalID
	}
	return out
}

// fixture is a service over a fresh account whose top space acme holds
// team, which inherits, and hidden, which does not; team holds app, which
// inherits. Stack web lives in app and stack db in hidden. The admin may
// do everything everywhere, the writer may read team and app, and attach
// there, and the reader may read team.
type fixture struct {
	db                    *pgxpool.Pool
	store                 *pg.Store
	svc                   *core.Service
	spaces                *spaces
	stacks                *stacks
	roles                 *roles
	acct                  kernel.AccountID
	sp                    map[string]kernel.ULID
	web, data             stacksapi.Stack
	admin, writer, reader kernel.Principal
	now                   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := pool(t)
	acct := kernel.NewAccountID()
	sp := map[string]kernel.ULID{
		"acme": acct.ULID, "team": kernel.NewULID(), "hidden": kernel.NewULID(),
		"app": kernel.NewULID(),
	}
	tree := &spaces{top: acct.ULID, ids: sp, nodes: map[string]space{
		"team": {inherit: true}, "hidden": {}, "app": {parent: "team", inherit: true},
	}}
	web := stacksapi.Stack{
		ID: kernel.NewULID(), Account: acct, Slug: "web", Name: "Web",
		Space: sp["app"],
	}
	data := stacksapi.Stack{
		ID: kernel.NewULID(), Account: acct, Slug: "db", Name: "DB",
		Space: sp["hidden"],
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
		accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin,
		accessapi.ActionStackAwsIntegrationAttach,
	}
	write := []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionStackAwsIntegrationAttach}
	az := authz{
		admin.Caller():  {},
		writer.Caller(): {sp["team"]: write, sp["app"]: write},
		reader.Caller(): {sp["team"]: {accessapi.ActionSpaceRead}},
	}
	for _, id := range sp {
		az[admin.Caller()][id] = all
	}
	store := pg.New(db)
	r := &roles{trusted: map[string][]string{}}
	f := &fixture{
		db: db, store: store, spaces: tree, stacks: st, roles: r, acct: acct, sp: sp,
		web: web, data: data, admin: admin, writer: writer, reader: reader,
		now: time.Unix(1_800_000_000, 0),
	}
	f.svc = core.New(core.Deps{
		Store: store, DB: db, Spaces: tree, Accounts: accounts{}, Stacks: st, Authz: az,
		Roles: r, Enabled: func(context.Context, kernel.AccountID) (bool, error) { return true, nil },
		Now: func() time.Time { return f.now },
	})
	return f
}

// role is an IAM role ARN of account 222222222222.
func role(name string) string { return "arn:aws:iam::222222222222:role/" + name }

// create creates an integration named name in space as the admin, with
// in's other fields, failing the test on a refusal.
func (f *fixture) create(t *testing.T, name, space string, in core.Input) core.Integration {
	t.Helper()
	in.Name, in.Space = name, space
	if in.RoleARN == "" {
		in.RoleARN = role(name)
	}
	i, err := f.svc.Create(t.Context(), f.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// attach attaches i to the stack slug as the admin for the sides given,
// failing the test on a refusal.
func (f *fixture) attach(t *testing.T, i core.Integration, slug string, read, write bool) core.Attachment {
	t.Helper()
	a, _, err := f.svc.Attach(t.Context(), f.admin, slug, i.ID.String(), read, write)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// pointers are the refused fields' pointers of err, a *kernel.Invalid.
func pointers(t *testing.T, err error) []string {
	t.Helper()
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		t.Fatalf("err = %v, want a *kernel.Invalid", err)
	}
	out := make([]string, len(inv.Errors))
	for n, e := range inv.Errors {
		out[n] = e.Pointer
	}
	return out
}

// audits are the actions of the account's audit entries, in order.
func (f *fixture) audits(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query(t.Context(), `SELECT action FROM audit_trail_entries
		WHERE account_id = $1 ORDER BY created_at, id`, f.acct.ULID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func ptr[T any](v T) *T { return &v }
