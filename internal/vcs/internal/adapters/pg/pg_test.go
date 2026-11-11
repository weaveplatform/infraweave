package pg_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
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
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// TestMain migrates the platform stream and the vcs stream alone: no vcs
// table references another domain's.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

var errDown = errors.New("down")

// spaces is an account's tree: acme on top, team inheriting from it, and
// hidden, which does not.
type spaces struct{ nodes []accessapi.SpaceNode }

func (s spaces) Hold(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	if !slices.ContainsFunc(s.nodes, func(n accessapi.SpaceNode) bool { return n.ID == id }) {
		return accessapi.ErrNoSpace
	}
	return nil
}

func (s spaces) Find(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
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
	return out, nil
}

func (s spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return s.nodes, nil
}

// authz grants each login its actions in its spaces.
type authz struct {
	accessapi.Authorizer
	grants map[string]map[kernel.ULID][]accessapi.Action
}

func (a authz) Authorize(
	_ context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	return slices.Contains(a.grants[p.Login][space], action), nil
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
	return out, nil
}

// stacks records the integrations DetachVCS is asked for, failing with
// err when it is set.
type stacks struct {
	stacksapi.Stacks
	detached *[]kernel.ULID
	err      error
}

func (s stacks) DetachVCS(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	*s.detached = append(*s.detached, id)
	return s.err
}

// tester passes each check while the credential is "token".
type tester struct{}

func (tester) Test(_ context.Context, in port.Integration) ([]port.Check, error) {
	return []port.Check{{
		Type: port.CheckConnection, Passed: string(in.Credential) == "token",
	}}, nil
}

type fixture struct {
	db            *pgxpool.Pool
	store         *pg.Store
	deps          integration.Deps
	svc           *integration.Service
	acct          kernel.AccountID
	admin, reader kernel.Principal
	team, hidden  kernel.ULID
	detached      []kernel.ULID
	stacks        *stacks
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
		db:     db,
		acct:   kernel.NewAccountID(),
		team:   kernel.NewULID(),
		hidden: kernel.NewULID(),
	}
	tree := spaces{nodes: []accessapi.SpaceNode{
		{ID: f.acct.ULID, Slug: "acme"},
		{ID: f.team, Slug: "team", Parent: f.acct.ULID, Inherit: true},
		{ID: f.hidden, Slug: "hidden", Parent: f.acct.ULID},
	}}
	principal := func(login string) kernel.Principal {
		return kernel.Principal{
			Kind:      kernel.PrincipalAPIKey,
			ActorID:   kernel.NewULID(),
			AccountID: f.acct,
			Login:     login,
		}
	}
	f.admin, f.reader = principal("admin"), principal("reader")
	all := []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin}
	read := []accessapi.Action{accessapi.ActionSpaceRead}
	f.stacks = &stacks{detached: &f.detached}
	f.store = pg.New(db, keys)
	f.deps = integration.Deps{
		Store: f.store, DB: db, Spaces: tree, Stacks: f.stacks,
		Authz: authz{grants: map[string]map[kernel.ULID][]accessapi.Action{
			"admin":  {f.acct.ULID: all, f.team: all, f.hidden: all},
			"reader": {f.acct.ULID: read, f.team: read},
		}},
		Providers: []string{"GITLAB", "BITBUCKET_CLOUD", "GITHUB_ENTERPRISE", "AZURE_DEVOPS"},
		Testers:   map[string]port.Tester{"GITLAB": tester{}},
		Builtin: map[string]port.Integration{"GITHUB": {
			Provider: "GITHUB", Credential: []byte("operator-key"),
			Settings: port.Settings{APIHost: "https://api.github.com", AppID: "1"},
		}},
		Apps:   apps{},
		Public: "https://infraweave.example/",
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	f.svc = integration.New(f.deps)
	return f
}

// pointers are the JSON Pointers of err's field errors.
func pointers(t *testing.T, err error) []string {
	t.Helper()
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		t.Fatalf("got %v, want a *kernel.Invalid", err)
	}
	out := make([]string, len(inv.Errors))
	for i, e := range inv.Errors {
		out[i] = e.Pointer
	}
	return out
}

func gitlab(name, space string, isDefault bool) integration.Input {
	return integration.Input{
		Provider: "GITLAB", Name: name, Space: space, IsDefault: isDefault,
		Labels:   []string{"team:infra"},
		Settings: port.Settings{APIHost: "https://gitlab.example"},
		Secrets: &integration.Secrets{
			Credential: []byte("token"), WebhookSecret: []byte("hook-secret"),
		},
	}
}

// TestAccCreate checks that an integration is created with its secrets
// sealed; that a second default of a provider, a default outside the top
// space, a taken slug, an unknown provider and a bad name, mode or label
// are refused at their fields; and that a space the caller cannot read
// is a *integration.Missing.
func TestAccCreate(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	def, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil || !def.IsDefault || def.Slug != "main" || def.Space != f.acct.ULID ||
		def.Checks.Mode != "INDIVIDUAL" {
		t.Fatalf("Create = %+v, %v", def, err)
	}
	var cred, hook []byte
	if err := f.db.QueryRow(ctx, `SELECT credentials_sealed, webhook_secret_sealed
		FROM vcs_integrations WHERE id = $1`, def.ID).Scan(&cred, &hook); err != nil {
		t.Fatal(err)
	}
	for _, plain := range [][]byte{[]byte("token"), []byte("hook-secret")} {
		if bytes.Contains(cred, plain) || bytes.Contains(hook, plain) {
			t.Errorf("a sealed column holds %q", plain)
		}
	}
	bad := gitlab("!!!", "", false)
	bad.Provider, bad.Checks.Mode, bad.Labels = "GIT", "SOME", []string{""}
	for name, tc := range map[string]struct {
		in   integration.Input
		want []string
	}{
		"a second default":           {gitlab("Other", "", true), []string{"/customInput/isDefault"}},
		"a default outside the top":  {gitlab("Team", "team", true), []string{"/customInput/isDefault"}},
		"a taken slug":               {gitlab("MAIN", "", false), []string{"/customInput/name"}},
		"an unknown provider and so": {bad, nil},
	} {
		got := pointers(
			t,
			func() error { _, err := f.svc.Create(ctx, f.admin, tc.in); return err }(),
		)
		if tc.want != nil && !slices.Equal(got, tc.want) {
			t.Errorf("%s's pointers = %v, want %v", name, got, tc.want)
		}
		if tc.want == nil && (len(got) < 4 || got[0] != "/provider") {
			t.Errorf("%s's pointers = %v", name, got)
		}
	}
	if _, err := f.svc.Create(ctx, f.reader, gitlab("Hidden", "hidden", false)); !isMissing(err) {
		t.Errorf("Create in a space the caller cannot read = %v", err)
	}
	if _, err := f.svc.Create(ctx, f.admin, gitlab("Nowhere", "nowhere", false)); !isMissing(err) {
		t.Errorf("Create in a missing space = %v", err)
	}
	if _, err := f.svc.Create(ctx, f.admin, integration.Input{Provider: "GITLAB"}); err == nil {
		t.Error("Create without a name succeeded")
	}
}

func isMissing(err error) bool {
	_, ok := errors.AsType[*integration.Missing](err)
	return ok
}

// TestAccRead checks reads, searches and attachable lists: a reader reads
// an integration without its details and only in the spaces it reads; a
// provider other than the integration's, or an ID that is no ULID, is
// integration.ErrNotFound; and an attachable list keeps the provider's
// integrations a stack in the space reaches.
func TestAccRead(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	def, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := f.svc.Create(ctx, f.admin, gitlab("Hidden", "hidden", false))
	if err != nil {
		t.Fatal(err)
	}
	bb := gitlab("Cloud", "team", false)
	bb.Provider, bb.Settings = "BITBUCKET_CLOUD", port.Settings{Email: "dev@acme.example"}
	if _, err := f.svc.Create(ctx, f.admin, bb); err != nil {
		t.Fatal(err)
	}
	if in, admin, err := f.svc.Read(ctx, f.admin, def.ID.String(), "GITLAB"); err != nil ||
		!admin || in.Settings.APIHost != "https://gitlab.example" {
		t.Errorf("Read by the admin = %+v, %t, %v", in, admin, err)
	}
	if _, admin, err := f.svc.Read(ctx, f.reader, def.ID.String(), "GITLAB"); err != nil || admin {
		t.Errorf("Read by the reader = %t, %v", admin, err)
	}
	for _, tc := range []struct{ id, provider string }{
		{def.ID.String(), "BITBUCKET_CLOUD"}, {"nope", "GITLAB"}, {kernel.NewULID().String(), "GITLAB"},
	} {
		if _, _, err := f.svc.Read(ctx, f.admin, tc.id, tc.provider); !errors.Is(
			err, integration.ErrNotFound) {
			t.Errorf("Read(%s, %s) = %v", tc.id, tc.provider, err)
		}
	}
	names := func(res search.Result[integration.Integration], err error) []string {
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
	if got := names(f.svc.Search(ctx, f.reader, search.Input{})); !slices.Equal(
		got, []string{"Cloud", "Main"}) {
		t.Errorf("the reader's search = %v", got)
	}
	if got := names(f.svc.Search(ctx, f.admin, search.Input{})); len(got) != 3 {
		t.Errorf("the admin's search = %v", got)
	}
	if got := names(
		f.svc.Attachable(ctx, f.admin, "team", "GITLAB", search.Input{}),
	); !slices.Equal(
		got,
		[]string{"Main"},
	) {
		t.Errorf("GitLab integrations a stack in team binds = %v", got)
	}
	if got := names(
		f.svc.Attachable(ctx, f.admin, "hidden", "GITLAB", search.Input{}),
	); !slices.Equal(
		got,
		[]string{"Hidden"},
	) {
		t.Errorf("GitLab integrations a stack in hidden binds = %v", got)
	}
	if _, err := f.svc.Attachable(
		ctx,
		f.reader,
		"hidden",
		"GITLAB",
		search.Input{},
	); !isMissing(
		err,
	) {
		t.Errorf("Attachable in a space the reader cannot read = %v", err)
	}
	got, err := f.svc.Suggest(ctx, f.reader, search.SuggestionsInput{})
	if err != nil || len(got.Fields) == 0 {
		t.Errorf("Suggest = %+v, %v", got, err)
	}
	if space, err := f.svc.SpaceOf(
		ctx,
		f.acct,
		hidden.ID.String(),
	); err != nil ||
		space != f.hidden {
		t.Errorf("SpaceOf = %v, %v", space, err)
	}
	if _, err := f.svc.SpaceOf(ctx, f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("SpaceOf a missing integration = %v", err)
	}
	inUse, err := f.svc.SpaceGuard().SpaceInUse(ctx, f.acct, f.hidden)
	if err != nil || !inUse {
		t.Errorf("SpaceInUse(hidden) = %t, %v", inUse, err)
	}
	if lost, err := f.svc.SpaceGuard().LostOnMove(ctx, accessapi.SpaceMove{}); err != nil ||
		lost != nil {
		t.Errorf("LostOnMove = %v, %v", lost, err)
	}
}

// TestAccUse checks the integration a stack uses: its own, else its
// provider's default, with its secrets opened; none for raw Git; and
// that Bindable allows only one of the stack's provider in its reach.
func TestAccUse(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	if _, ok, err := f.svc.Use(ctx, f.acct, "GITLAB", nil); err != nil || ok {
		t.Errorf("Use with no default = %t, %v", ok, err)
	}
	def, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := f.svc.Create(ctx, f.admin, gitlab("Hidden", "hidden", false))
	if err != nil {
		t.Fatal(err)
	}
	use, ok, err := f.svc.Use(ctx, f.acct, "GITLAB", nil)
	if err != nil || !ok || string(use.Credential) != "token" ||
		string(use.WebhookSecret) != "hook-secret" {
		t.Errorf("Use of the default = %t, %v", ok, err)
	}
	if _, ok, err := f.svc.Use(ctx, f.acct, "GITLAB", &hidden.ID); err != nil || !ok {
		t.Errorf("Use of the stack's own = %t, %v", ok, err)
	}
	if _, ok, err := f.svc.Use(ctx, f.acct, "GIT", nil); err != nil || ok {
		t.Errorf("Use on raw Git = %t, %v", ok, err)
	}
	for name, tc := range map[string]struct {
		space    kernel.ULID
		provider string
		id       kernel.ULID
		ok       bool
	}{
		"the default from team": {f.team, "GITLAB", def.ID, true},
		"hidden from team":      {f.team, "GITLAB", hidden.ID, false},
		"hidden from hidden":    {f.hidden, "GITLAB", hidden.ID, true},
		"of another provider":   {f.acct.ULID, "BITBUCKET_CLOUD", def.ID, false},
		"one the account lacks": {f.acct.ULID, "GITLAB", kernel.NewULID(), false},
	} {
		err := f.svc.Bindable(ctx, f.acct, tc.space, tc.provider, tc.id)
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, stacksapi.ErrNoIntegration) {
			t.Errorf("Bindable %s = %v", name, err)
		}
	}
}

// TestAccWrite checks updates, tests and deletes: an update keeps the
// name and the default and replaces the secrets it carries; a stale
// version is kernel.ErrStale; a test runs the provider's Tester with the
// opened credential, and a provider without one is ErrNoTester; a delete
// detaches the stacks in its transaction, and a failed detach deletes
// nothing; and a purge empties the account.
func TestAccWrite(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	def, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	in := gitlab("Renamed", "acme", false)
	in.Description, in.Checks.Mode = "Main GitLab", "ALL"
	in.Secrets = &integration.Secrets{Credential: []byte("rotated")}
	got, err := f.svc.Update(ctx, f.admin, def.ID.String(), kernel.Expect{}, in)
	if err != nil || got.Name != "Main" || !got.IsDefault || got.Space != f.acct.ULID ||
		got.Description != "Main GitLab" || got.Checks.Mode != "ALL" || got.Version != 2 {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	stale := kernel.ExpectVersions(1)
	if _, err := f.svc.Update(
		ctx,
		f.admin,
		def.ID.String(),
		stale,
		in,
	); !errors.Is(
		err,
		kernel.ErrStale,
	) {
		t.Errorf("Update at a stale version = %v", err)
	}
	if _, err := f.svc.Update(ctx, f.admin, "nope", kernel.Expect{}, in); !errors.Is(
		err, integration.ErrNotFound) {
		t.Errorf("Update of a missing integration = %v", err)
	}
	in.Checks.Mode = "SOME"
	if _, err := f.svc.Update(ctx, f.admin, def.ID.String(), kernel.Expect{}, in); err == nil {
		t.Error("Update with a bad mode succeeded")
	}
	checks, err := f.svc.Test(ctx, f.admin, def.ID.String(), "GITLAB")
	if err != nil || len(checks) != 1 || checks[0].Passed {
		t.Errorf("Test after the rotation = %+v, %v", checks, err)
	}
	bb := gitlab("Cloud", "", false)
	bb.Provider, bb.Secrets = "BITBUCKET_CLOUD", nil
	bb.Settings = port.Settings{Email: "dev@acme.example"}
	cloud, err := f.svc.Create(ctx, f.admin, bb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Test(ctx, f.admin, cloud.ID.String(), "BITBUCKET_CLOUD"); !errors.Is(
		err, integration.ErrNoTester) {
		t.Errorf("Test without a Tester = %v", err)
	}
	if _, err := f.svc.Test(
		ctx,
		f.admin,
		"nope",
		"GITLAB",
	); !errors.Is(
		err,
		integration.ErrNotFound,
	) {
		t.Errorf("Test of a missing integration = %v", err)
	}
	f.stacks.err = errDown
	if _, err := f.svc.Delete(ctx, f.admin, def.ID.String(), "GITLAB"); !errors.Is(err, errDown) {
		t.Errorf("Delete with stacks down = %v", err)
	}
	if _, _, err := f.svc.Read(ctx, f.admin, def.ID.String(), "GITLAB"); err != nil {
		t.Errorf("Read after a failed delete = %v", err)
	}
	f.stacks.err = nil
	if _, err := f.svc.Delete(ctx, f.admin, def.ID.String(), "GITLAB"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Read(ctx, f.admin, def.ID.String(), "GITLAB"); !errors.Is(
		err, integration.ErrNotFound) {
		t.Errorf("Read after the delete = %v", err)
	}
	if !slices.Equal(f.detached, []kernel.ULID{def.ID, def.ID}) {
		t.Errorf("detached %v", f.detached)
	}
	if _, err := f.svc.Delete(ctx, f.admin, cloud.ID.String(), "GITLAB"); !errors.Is(
		err, integration.ErrNotFound) {
		t.Errorf("Delete under another provider = %v", err)
	}
	if done, err := f.svc.Purge(ctx, f.acct, 1); err != nil || done {
		t.Errorf("Purge = %t, %v", done, err)
	}
	if done, err := f.svc.Purge(ctx, f.acct, 1); err != nil || !done {
		t.Errorf("Purge of an empty account = %t, %v", done, err)
	}
}
