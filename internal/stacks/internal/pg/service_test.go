package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// spaces are an account's spaces by slug, recording each hold.
type spaces struct {
	mu    sync.Mutex
	ids   map[string]kernel.ULID
	holds []kernel.ULID
	// parents names a space's parent by slug; acme when it names none.
	parents map[string]string
	// treeErr fails Tree.
	treeErr error
}

func (s *spaces) Hold(_ context.Context, _ kernel.AccountID, id kernel.ULID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.ids {
		if v == id {
			s.holds = append(s.holds, id)
			return nil
		}
	}
	return accessapi.ErrNoSpace
}

func (s *spaces) Find(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if id, ok := s.ids[slug]; ok {
		return id, nil
	}
	return kernel.ULID{}, accessapi.ErrNoSpace
}

func (s *spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	out := map[kernel.ULID]string{}
	for slug, id := range s.ids {
		out[id] = slug
	}
	return out, nil
}

// Tree is every space, acme first as the top space and the rest by slug,
// each below its parent.
func (s *spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	if s.treeErr != nil {
		return nil, s.treeErr
	}
	out := []accessapi.SpaceNode{{ID: s.ids["acme"], Slug: "acme", Name: "ACME"}}
	for _, slug := range slices.Sorted(maps.Keys(s.ids)) {
		if slug == "acme" {
			continue
		}
		parent := s.parents[slug]
		if parent == "" {
			parent = "acme"
		}
		out = append(out, accessapi.SpaceNode{
			ID: s.ids[slug], Slug: slug, Name: strings.ToUpper(slug), Parent: s.ids[parent],
		})
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
	for space, as := range a[p.Caller()] {
		if slices.Contains(as, action) {
			out = append(out, space)
		}
	}
	return out, nil
}

func (a authz) Holds(context.Context, kernel.Principal, accessapi.Action) (bool, error) {
	return false, nil
}

func (a authz) Actions(
	_ context.Context,
	p kernel.Principal,
	space kernel.ULID,
) ([]accessapi.Action, error) {
	return a[p.Caller()][space], nil
}

// fixture is a service over a fresh account with a top space, a team
// space and a hidden one, an admin who may do everything in the first
// two, a writer who may write in the team space, and a reader of it.
type fixture struct {
	db                    *pgxpool.Pool
	svc                   *core.Service
	spaces                *spaces
	authz                 authz
	acct                  kernel.AccountID
	team, hidden          kernel.ULID
	admin, writer, reader kernel.Principal
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := pool(t)
	kinds := outbox.NewKinds()
	if err := kinds.Register(stacksapi.ChangedKind, stacksapi.ChangedVersion,
		stacksapi.ChangedSchema); err != nil {
		t.Fatal(err)
	}
	acct := kernel.NewAccountID()
	team, hidden := kernel.NewULID(), kernel.NewULID()
	sp := &spaces{ids: map[string]kernel.ULID{"acme": acct.ULID, "team": team, "hidden": hidden}}
	principal := func(login string) kernel.Principal {
		return kernel.Principal{
			Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(), AccountID: acct, Login: login,
		}
	}
	admin, writer, reader := principal("admin"), principal("writer"), principal("reader")
	all := []accessapi.Action{
		accessapi.ActionSpaceRead, accessapi.ActionSpaceWrite, accessapi.ActionStackCreate,
		accessapi.ActionStackUnlockForce,
	}
	write := []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionSpaceWrite}
	read := []accessapi.Action{accessapi.ActionSpaceRead}
	az := authz{
		admin.Caller():  {acct.ULID: all, team: all},
		writer.Caller(): {team: write},
		reader.Caller(): {team: read},
	}
	svc := core.New(core.Deps{
		Store: pg.New(db), DB: db, Spaces: sp, Authz: az,
		Events: outbox.New(db, queue.New(db), kinds),
		Now:    func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	return fixture{
		db: db, svc: svc, spaces: sp, authz: az, acct: acct, team: team, hidden: hidden,
		admin: admin, writer: writer, reader: reader,
	}
}

// changes are the Changed events of the account's stack slug's ID, in
// order.
func (f fixture) changes(t *testing.T, id kernel.ULID) []string {
	t.Helper()
	rows, err := f.db.Query(t.Context(), `SELECT document->>'change' FROM events
		WHERE account_id = $1 AND kind = $2 AND subject_id = $3 ORDER BY sequence`,
		f.acct, stacksapi.ChangedKind, id.String())
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

func input(name string) core.Input {
	return core.Input{Space: "team", Name: name, Repository: "infra", Branch: "main"}
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

// TestAccServiceCreate creates stacks with derived and given slugs, their
// settings, hooks and labels, and refuses what a stack cannot hold.
func TestAccServiceCreate(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	in := input("Prod / EU")
	in.Labels = []string{"b", "a"}
	in.Hooks = map[string][]string{stacksapi.BeforePlan: {"echo 1", "echo 2"}}
	in.VendorConfig = json.RawMessage(`{"opentofu":{"version":"~> 1.8.0"},"terraform":null}`)
	st, err := f.svc.Create(ctx, f.admin, "", true, in)
	if err != nil {
		t.Fatal(err)
	}
	if st.Slug != "prod-eu" || st.Space != f.team || !st.ManagesState || st.Version != 1 {
		t.Errorf("created %+v", st)
	}
	got, err := f.svc.Stack(ctx, f.acct, "prod-eu")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Labels, []string{"a", "b"}) ||
		!reflect.DeepEqual(
			got.Hooks,
			map[string][]string{stacksapi.BeforePlan: {"echo 1", "echo 2"}},
		) ||
		got.Vendor != "opentofu" || string(got.VendorConfig) != `{"version": "~> 1.8.0"}` ||
		got.Provider != core.DefaultProvider || !got.GithubActionDeploy {
		t.Errorf("read back %+v", got)
	}
	if !slices.Contains(f.spaces.holds, f.team) {
		t.Error("the create held no space")
	}
	if c := f.changes(t, st.ID); !reflect.DeepEqual(c, []string{stacksapi.ChangeCreated}) {
		t.Errorf("events %v", c)
	}
	plain, err := f.svc.Create(ctx, f.admin, "custom-1", false, core.Input{
		Name: "Plain", Repository: "r", Branch: "b",
	})
	if err != nil || plain.Slug != "custom-1" || plain.Space != f.acct.ULID ||
		plain.Vendor != "terraform" {
		t.Errorf("Create in the top space = %+v, %v", plain, err)
	}
	if _, err := f.svc.Create(
		ctx,
		f.admin,
		"",
		false,
		input("prod eu"),
	); !isSlugTaken(
		err,
		"prod-eu",
	) {
		t.Errorf("Create of a taken slug = %v", err)
	}
	if got := pointers(t, func() error {
		_, err := f.svc.Create(ctx, f.admin, "Not A Slug", false, input("x"))
		return err
	}()); !reflect.DeepEqual(got, []string{"/slug"}) {
		t.Errorf("a bad slug's pointers = %v", got)
	}
	bad := core.Input{
		Name: "!!!", WorkerPool: new("wp"), VCSIntegration: new("vcs"),
		VendorConfig:     json.RawMessage(`{"terraform":{"version":"1.x"},"opentofu":{}}`),
		TerraformVersion: new("latest"),
	}
	want := []string{
		"/input/name", "/input/repository", "/input/branch", "/input/workerPool",
		"/input/vcsIntegrationId", "/input/terraformVersion", "/input/vendorConfig",
		"/input/vendorConfig/terraform/version",
	}
	if got := pointers(t, func() error {
		_, err := f.svc.Create(ctx, f.admin, "", false, bad)
		return err
	}()); !reflect.DeepEqual(got, want) {
		t.Errorf("a bad input's pointers = %v, want %v", got, want)
	}
	for _, space := range []string{"hidden", "nowhere"} {
		in := input("Elsewhere")
		in.Space = space
		if _, err := f.svc.Create(ctx, f.admin, "", false, in); !isNoSpace(err, space) {
			t.Errorf("Create in %s = %v", space, err)
		}
	}
}

func isSlugTaken(err error, slug string) bool {
	t, ok := errors.AsType[*core.SlugTaken](err)
	return ok && t.Slug == slug
}

func isNoSpace(err error, slug string) bool {
	s, ok := errors.AsType[*core.SpaceError](err)
	return ok && s.Slug == slug
}

// TestAccServiceReads reads a stack for other domains and for the
// access domain's middleware.
func TestAccServiceReads(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	st, err := f.svc.Create(ctx, f.admin, "", false, input("Infra"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.Get(ctx, f.acct, st.ID); err != nil || got != st.Stack {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if got, err := f.svc.Settings(ctx, f.acct, st.ID); err != nil || got.Name != "Infra" {
		t.Errorf("Settings = %+v, %v", got, err)
	}
	if got, err := f.svc.BySlug(ctx, f.acct, "infra"); err != nil || got != st.Stack {
		t.Errorf("BySlug = %+v, %v", got, err)
	}
	if _, err := f.svc.BySlug(ctx, f.acct, "nope"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("BySlug a missing stack = %v", err)
	}
	if got, err := f.svc.ByIDs(ctx, f.acct, []kernel.ULID{st.ID}); err != nil ||
		got[st.ID] != st.Stack {
		t.Errorf("ByIDs = %+v, %v", got, err)
	}
	if got, err := f.svc.ByIDs(ctx, f.acct, nil); err != nil || len(got) != 0 {
		t.Errorf("ByIDs of none = %+v, %v", got, err)
	}
	if got, err := f.svc.SpaceOf(ctx, f.acct, "infra"); err != nil || got != f.team {
		t.Errorf("SpaceOf = %v, %v", got, err)
	}
	if _, err := f.svc.SpaceOf(ctx, f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("SpaceOf a missing stack = %v", err)
	}
	got, err := f.svc.ByRepository(ctx, f.acct, nil, "infra", "main")
	if err != nil || !reflect.DeepEqual(got, []stacksapi.Stack{st.Stack}) {
		t.Errorf("ByRepository = %+v, %v", got, err)
	}
	if got, err := f.svc.ByRepository(
		ctx,
		f.acct,
		&f.team,
		"infra",
		"main",
	); err != nil ||
		len(got) != 0 {
		t.Errorf("ByRepository through an integration = %+v, %v", got, err)
	}
	err = tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		s, err := f.svc.LockForRun(ctx, f.acct, st.ID)
		if err == nil && s.Slug != "infra" {
			t.Errorf("LockForRun = %+v", s)
		}
		return err
	})
	if err != nil {
		t.Errorf("LockForRun = %v", err)
	}
	if _, err := f.svc.Settings(
		ctx,
		f.acct,
		kernel.NewULID(),
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("Settings of a missing stack = %v", err)
	}
	if used, err := f.svc.SpaceInUse(ctx, f.acct, f.team); err != nil || !used {
		t.Errorf("SpaceInUse = %v, %v", used, err)
	}
	if lost, err := f.svc.LostOnMove(ctx, accessapi.SpaceMove{}); err != nil || lost != nil {
		t.Errorf("LostOnMove = %v, %v", lost, err)
	}
	other := f.writer
	other.AccountID = kernel.NewAccountID()
	for name, c := range map[string]struct {
		p    kernel.Principal
		want int
	}{
		"the admin":                  {f.admin, 1},
		"the reader of its space":    {f.reader, 1},
		"another account's caller":   {other, 0},
		"a caller who may read none": {kernel.Principal{AccountID: f.acct}, 0},
	} {
		res, err := f.svc.Search(ctx, c.p, search.Input{})
		if err != nil || len(res.Edges) != c.want {
			t.Errorf("Search as %s = %d stacks, %v; want %d", name, len(res.Edges), err, c.want)
		}
	}
}

// TestAccServiceUpdate replaces a stack's settings, moves it only where
// the caller may create stacks, and reslugs it from its name.
func TestAccServiceUpdate(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	st, err := f.svc.Create(ctx, f.admin, "", false, input("Infra"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(ctx, f.admin, "", false, input("Renamed")); err != nil {
		t.Fatal(err)
	}
	in := input("Renamed")
	in.Labels = []string{"x"}
	in.Autodeploy = new(true)
	got, err := f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, in)
	if err != nil || got.Name != "Renamed" || got.Slug != "infra" || !got.Autodeploy ||
		!reflect.DeepEqual(got.Labels, []string{"x"}) || got.Version != 2 {
		t.Errorf("Update = %+v, %v", got, err)
	}
	if _, err := f.svc.Update(
		ctx, f.admin, "infra", kernel.ExpectVersions(st.Version), in,
	); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("an Update of a stale version = %v", err)
	}
	if again, err := f.svc.Update(
		ctx, f.admin, "infra", kernel.ExpectVersions(got.Version), in,
	); err != nil || again.Version != got.Version+1 {
		t.Errorf("an Update of the version read = %+v, %v", again, err)
	}
	labels := in
	labels.Labels = []string{"Prod", "prod", " edge", "Prod"}
	if got := pointers(t, func() error {
		_, err := f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, labels)
		return err
	}()); !reflect.DeepEqual(got, []string{"/labels/2", "/labels/3"}) {
		t.Errorf("refused labels name %v", got)
	}
	if _, err := f.svc.Reslug(ctx, f.admin, "infra"); !isSlugTaken(err, "renamed") {
		t.Errorf("Reslug to a taken slug = %v", err)
	}
	in.Name = "Infra Two"
	if _, err := f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, in); err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.Reslug(ctx, f.admin, "infra"); err != nil || got.Slug != "infra-two" {
		t.Errorf("Reslug = %+v, %v", got, err)
	}
	if _, err := f.svc.Stack(ctx, f.acct, "infra"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("the old slug reads %v", err)
	}
	moved := input("Infra Two")
	moved.Space = ""
	var denied *core.Denied
	if _, err := f.svc.Update(
		ctx,
		f.writer,
		"infra-two",
		kernel.Expect{},
		moved,
	); !isNoSpace(
		err,
		"",
	) {
		t.Errorf("a move to a space the writer cannot read = %v", err)
	}
	f2 := moved
	f2.Space = "team"
	if _, err := f.svc.Update(ctx, f.writer, "infra-two", kernel.Expect{}, f2); err != nil {
		t.Errorf("an update in place by the writer = %v", err)
	}
	if _, err := f.svc.Update(
		ctx,
		f.reader,
		"infra-two",
		kernel.Expect{},
		moved,
	); !isNoSpace(
		err,
		"",
	) {
		t.Errorf("a move by the reader = %v", err)
	}
	f.authzGrant(f.writer, f.acct.ULID, accessapi.ActionSpaceRead)
	if _, err := f.svc.Update(
		ctx,
		f.writer,
		"infra-two",
		kernel.Expect{},
		moved,
	); !errors.As(
		err,
		&denied,
	) ||
		denied.Action != accessapi.ActionStackCreate {
		t.Errorf("a move without STACK_CREATE there = %v", err)
	}
	if got, err := f.svc.Update(
		ctx,
		f.admin,
		"infra-two",
		kernel.Expect{},
		moved,
	); err != nil ||
		got.Space != f.acct.ULID {
		t.Errorf("a move by the admin = %+v, %v", got, err)
	}
	if _, err := f.svc.Update(
		ctx,
		f.admin,
		"infra-two",
		kernel.Expect{},
		core.Input{},
	); err == nil {
		t.Error("an empty input was accepted")
	}
	if _, err := f.svc.Update(
		ctx,
		f.admin,
		"gone",
		kernel.Expect{},
		input("x"),
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("Update of a missing stack = %v", err)
	}
	want := []string{
		stacksapi.ChangeCreated, stacksapi.ChangeUpdated, stacksapi.ChangeUpdated,
		stacksapi.ChangeUpdated, stacksapi.ChangeReslugged, stacksapi.ChangeUpdated,
		stacksapi.ChangeUpdated,
	}
	if c := f.changes(t, st.ID); !reflect.DeepEqual(c, want) {
		t.Errorf("events %v, want %v", c, want)
	}
}

// authzGrant lets p perform action in space too.
func (f fixture) authzGrant(p kernel.Principal, space kernel.ULID, action accessapi.Action) {
	az := f.authz
	if az[p.Caller()] == nil {
		az[p.Caller()] = map[kernel.ULID][]accessapi.Action{}
	}
	az[p.Caller()][space] = append(az[p.Caller()][space], action)
}

// TestAccServiceLocks checks that a lock is its holder's: another
// caller's lock is refused, the holder unlocks without force and anyone
// else needs STACK_UNLOCK_FORCE.
func TestAccServiceLocks(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	if _, err := f.svc.Create(ctx, f.admin, "", false, input("Infra")); err != nil {
		t.Fatal(err)
	}
	st, err := f.svc.Lock(ctx, f.writer, "infra", "deploying")
	if err != nil || st.Lock == nil || st.Lock.By != "writer" || st.Lock.Note != "deploying" ||
		!st.Lock.At.Equal(time.Unix(1_800_000_000, 0)) {
		t.Fatalf("Lock = %+v, %v", st.Lock, err)
	}
	var locked *core.Locked
	if _, err := f.svc.Lock(
		ctx,
		f.admin,
		"infra",
		"",
	); !errors.As(err, &locked) ||
		locked.By != "writer" {
		t.Errorf("a second caller's lock = %v", err)
	}
	if st, err := f.svc.Lock(
		ctx,
		f.writer,
		"infra",
		"still",
	); err != nil ||
		st.Lock.Note != "still" {
		t.Errorf("the holder's relock = %+v, %v", st.Lock, err)
	}
	var denied *core.Denied
	if _, err := f.svc.Unlock(ctx, f.reader, "infra"); !errors.As(err, &denied) ||
		denied.Action != accessapi.ActionStackUnlockForce {
		t.Errorf("another caller's unlock without force = %v", err)
	}
	if st, err := f.svc.Unlock(ctx, f.writer, "infra"); err != nil || st.Lock != nil {
		t.Errorf("the holder's unlock = %+v, %v", st.Lock, err)
	}
	if st, err := f.svc.Unlock(
		ctx,
		f.writer,
		"infra",
	); err != nil || st.Lock != nil ||
		st.Version != 4 {
		t.Errorf("unlocking an unlocked stack = %+v, %v", st, err)
	}
	if _, err := f.svc.Lock(ctx, f.writer, "infra", ""); err != nil {
		t.Fatal(err)
	}
	if st, err := f.svc.Unlock(ctx, f.admin, "infra"); err != nil || st.Lock != nil {
		t.Errorf("a forced unlock = %+v, %v", st.Lock, err)
	}
	var none kernel.Principal
	none.AccountID, none.ActorID = f.acct, kernel.NewULID()
	if _, err := f.svc.Lock(ctx, none, "infra", ""); err != nil {
		t.Fatal(err)
	}
	if st, err := f.svc.Stack(
		ctx,
		f.acct,
		"infra",
	); err != nil ||
		st.Lock.By != none.ActorID.String() {
		t.Errorf("a lock by a caller with no login shows %+v, %v", st.Lock, err)
	}
}

// TestAccServiceStateChanges disables, enables, stars and deletes a
// stack, refusing to delete one protected from deletion.
func TestAccServiceStateChanges(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	in := input("Infra")
	in.ProtectFromDeletion = new(true)
	st, err := f.svc.Create(ctx, f.admin, "", false, in)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.SetDisabled(ctx, f.admin, "infra", true); err != nil || !got.Disabled {
		t.Errorf("SetDisabled(true) = %+v, %v", got, err)
	}
	if got, err := f.svc.SetDisabled(ctx, f.admin, "infra", false); err != nil || got.Disabled {
		t.Errorf("SetDisabled(false) = %+v, %v", got, err)
	}
	if _, err := f.svc.SetStar(ctx, f.reader, "infra", true); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		p    kernel.Principal
		want bool
	}{{f.reader, true}, {f.admin, false}} {
		if got, err := f.svc.Starred(
			ctx,
			c.p,
			[]core.Stack{st},
		); err != nil ||
			got[st.ID] != c.want {
			t.Errorf("Starred for %s = %v, %v", c.p.Login, got, err)
		}
	}
	if _, err := f.svc.SetStar(ctx, f.reader, "infra", false); err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.Starred(ctx, f.reader, []core.Stack{st}); err != nil || got[st.ID] {
		t.Errorf("Starred after unstarring = %v, %v", got, err)
	}
	if _, err := f.svc.SetStar(
		ctx,
		f.reader,
		"nope",
		true,
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("SetStar of a missing stack = %v", err)
	}
	if got := pointers(t, func() error {
		_, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{})
		return err
	}()); len(got) != 0 {
		t.Errorf("a protected stack's delete names %v", got)
	}
	in.ProtectFromDeletion = nil
	if _, err := f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Delete(
		ctx, f.admin, "infra", kernel.ExpectVersions(st.Version),
	); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a Delete of a stale version = %v", err)
	}
	if got, err := f.svc.Delete(
		ctx,
		f.admin,
		"infra",
		kernel.ExpectAny(),
	); err != nil ||
		got.ID != st.ID {
		t.Errorf("Delete = %+v, %v", got, err)
	}
	if _, err := f.svc.Delete(
		ctx,
		f.admin,
		"infra",
		kernel.Expect{},
	); !errors.Is(
		err,
		stacksapi.ErrNotFound,
	) {
		t.Errorf("a second Delete = %v", err)
	}
	if used, err := f.svc.SpaceInUse(ctx, f.acct, f.team); err != nil || used {
		t.Errorf("SpaceInUse after the delete = %v, %v", used, err)
	}
	want := []string{
		stacksapi.ChangeCreated, stacksapi.ChangeDisabled, stacksapi.ChangeEnabled,
		stacksapi.ChangeUpdated, stacksapi.ChangeDeleted,
	}
	if c := f.changes(t, st.ID); !reflect.DeepEqual(c, want) {
		t.Errorf("events %v, want %v", c, want)
	}
}
