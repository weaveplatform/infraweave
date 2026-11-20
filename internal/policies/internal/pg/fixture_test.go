package pg_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	policyhttp "github.com/weaveplatform/infraweave/internal/policies/internal/http"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Only platform and policies migrations apply here. Space/authz ports are
// deterministic fixtures; the two-server BDD suite exercises their real domains.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

type spaces struct {
	accessapi.Spaces
	nodes    []accessapi.SpaceNode
	err      error
	slugsErr error
	treeErr  error
	holdErr  error
}

func (s *spaces) Find(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
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

func (s *spaces) Hold(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	if s.holdErr != nil {
		return s.holdErr
	}
	if s.err != nil {
		return s.err
	}
	for _, n := range s.nodes {
		if n.ID == id {
			return nil
		}
	}
	return accessapi.ErrNoSpace
}

func (s *spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	if s.treeErr != nil {
		return nil, s.treeErr
	}
	return slices.Clone(s.nodes), s.err
}

func (s *spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	if s.slugsErr != nil {
		return nil, s.slugsErr
	}
	if s.err != nil {
		return nil, s.err
	}
	out := map[kernel.ULID]string{}
	for _, n := range s.nodes {
		out[n.ID] = n.Slug
	}
	return out, nil
}

type authz struct {
	accessapi.Authorizer
	readable []kernel.ULID
	denied   bool
	err      error
	reachErr error
}

func (a *authz) Authorize(context.Context, kernel.Principal, accessapi.Action, kernel.ULID) (bool, error) {
	return !a.denied, a.err
}

func (a *authz) Reachable(context.Context, kernel.Principal, accessapi.Action) ([]kernel.ULID, error) {
	if a.reachErr != nil {
		return nil, a.reachErr
	}
	return slices.Clone(a.readable), a.err
}

type fixture struct {
	db        *pgxpool.Pool
	store     *pg.Store
	runtime   *rego.Engine
	events    *outbox.Outbox
	spaces    *spaces
	authz     *authz
	principal kernel.Principal
	now       time.Time
	svc       *core.Service
	http      policyhttp.Server
	stacks    *stackPort
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	kinds := outbox.NewKinds()
	if err := kinds.Register(policyapi.ChangedKind, policyapi.ChangedVersion, policyapi.ChangedSchema); err != nil {
		t.Fatal(err)
	}
	if err := kinds.Register(policyapi.EvaluatedKind, policyapi.EvaluationEventVersion, policyapi.EvaluatedSchema); err != nil {
		t.Fatal(err)
	}
	if err := kinds.Register(policyapi.InternalErrorKind, policyapi.EvaluationEventVersion, policyapi.InternalErrorSchema); err != nil {
		t.Fatal(err)
	}
	runtime, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	acct := kernel.NewAccountID()
	team := kernel.NewULID()
	f := &fixture{
		db:      db,
		store:   pg.New(db),
		runtime: runtime,
		events:  outbox.New(db, queue.New(db), kinds),
		spaces:  &spaces{nodes: []accessapi.SpaceNode{{ID: acct.ULID, Slug: "root", Name: "Root"}, {ID: team, Slug: "team", Name: "Team", Parent: acct.ULID, Inherit: true}}},
		authz: &authz{
			readable: []kernel.ULID{acct.ULID, team},
		},
		principal: kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(), AccountID: acct},
		now:       time.Unix(1795130000, 0).UTC(),
	}
	f.stacks = &stackPort{stack: stacksapi.Stack{Account: acct, ID: kernel.NewULID(), Slug: "infra", Name: "Infra", Space: acct.ULID}, labels: []string{"prod"}}
	f.svc = f.service(f.events)
	f.http = policyhttp.New(f.svc, f.spaces)
	return f
}

func (f *fixture) service(events core.Events) *core.Service {
	return core.New(
		core.Deps{
			Store:   f.store,
			DB:      f.db,
			Spaces:  f.spaces,
			Authz:   f.authz,
			Runtime: f.runtime,
			Events:  events,
			Stacks:  f.stacks,
			Targets: f.stacks,
			Queue:   queue.New(f.db),
			Now:     func() time.Time { return f.now },
		},
	)
}

func (f *fixture) create(t *testing.T, name string) core.Policy {
	t.Helper()
	p, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: name, Type: rego.Plan, Body: "package infraweave\n", Labels: []string{"prod"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) count(t *testing.T, table string) int {
	t.Helper()
	if !slices.Contains([]string{"policies", "policy_stack_attachments", "events"}, table) {
		t.Fatal("unknown fixture table")
	}
	var n int
	if err := f.db.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", f.principal.AccountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
