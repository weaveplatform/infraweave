package pg_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccCreateValidates(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(t.Context(), f.admin, core.Input{
		Name: " ", RoleARN: "arn:aws:iam::1:user/x", Region: ptr(" "), Duration: ptr[int32](60),
		ExternalID: "custom",
	})
	want := []string{"/name", "/roleArn", "/region", "/durationSeconds", "/externalID"}
	if got := pointers(t, err); !slices.Equal(got, want) {
		t.Fatalf("pointers = %v, want %v", got, want)
	}
	// A worker's session may last 12 hours and carry a custom external ID.
	i := f.create(t, "worker", "team", core.Input{
		Worker: true, Duration: ptr[int32](core.MaxWorkerDuration), ExternalID: "custom",
		Labels: []string{"b", "a", "b"}, Region: ptr("eu-west-1"),
	})
	if i.Duration != core.MaxWorkerDuration || i.ExternalID != "custom" ||
		!slices.Equal(i.Labels, []string{"a", "b"}) || i.Space != f.sp["team"] || i.Version != 1 {
		t.Fatalf("integration = %+v", i)
	}
	_, err = f.svc.Create(t.Context(), f.admin, core.Input{
		Name: "server", RoleARN: role("r"), Duration: ptr[int32](core.MaxWorkerDuration),
	})
	if got := pointers(t, err); !slices.Equal(got, []string{"/durationSeconds"}) {
		t.Fatalf("pointers = %v", got)
	}
	if got := f.audits(t); !slices.Equal(got, []string{core.ActionCreate}) {
		t.Fatalf("audits = %v", got)
	}
}

func TestAccCreateRefuses(t *testing.T) {
	f := newFixture(t)
	f.create(t, "prod", "", core.Input{})
	_, err := f.svc.Create(t.Context(), f.admin, core.Input{Name: "prod", RoleARN: role("x")})
	if taken, ok := errors.AsType[*core.NameTaken](err); !ok || taken.Name != "prod" {
		t.Fatalf("err = %v, want the name taken", err)
	}
	for _, tc := range []struct{ who, space string }{
		{"admin", "nowhere"}, {"reader", "hidden"},
	} {
		p := map[string]kernel.Principal{"admin": f.admin, "reader": f.reader}[tc.who]
		_, err := f.svc.Create(t.Context(), p, core.Input{
			Name: "x", RoleARN: role("x"), Space: tc.space,
		})
		if m, ok := errors.AsType[*core.Missing](err); !ok || m.Kind != "space" {
			t.Fatalf("%s in %s: err = %v, want a missing space", tc.who, tc.space, err)
		}
	}
	f.spaces.err = errBroken
	if _, err := f.svc.Create(t.Context(), f.admin, core.Input{
		Name: "y", RoleARN: role("y"), Space: "team",
	}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want broken", err)
	}
}

func TestAccReadsAndLists(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, "alpha", "team", core.Input{})
	f.create(t, "beta", "team", core.Input{})
	f.create(t, "gamma", "hidden", core.Input{})
	if _, _, err := f.svc.SetLegacy(t.Context(), f.admin, "web",
		core.LegacyInput{RoleARN: role("legacy")}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	got, err := f.svc.Get(ctx, f.acct, a.ID.String())
	if err != nil || got.Name != "alpha" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if _, err := f.svc.Get(ctx, f.acct, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.Get(ctx, f.acct, kernel.NewULID().String()); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if sp, err := f.svc.SpaceOf(ctx, f.acct, a.ID.String()); err != nil || sp != f.sp["team"] {
		t.Fatalf("space = %v, %v", sp, err)
	}
	if _, err := f.svc.SpaceOf(ctx, f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Fatalf("err = %v", err)
	}
	if i, err := f.svc.ByName(ctx, f.reader, "alpha"); err != nil || i.ID != a.ID {
		t.Fatalf("by name = %+v, %v", i, err)
	}
	for _, name := range []string{"gamma", "nope"} {
		if _, err := f.svc.ByName(ctx, f.reader, name); !isMissing(err) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	page, more, err := f.svc.List(ctx, f.admin, false, core.Page{Limit: 2})
	if err != nil || !more || names(page) != "alpha,beta" {
		t.Fatalf("page = %s, %v, %v", names(page), more, err)
	}
	page, more, err = f.svc.List(ctx, f.admin, false, core.Page{After: "beta", Limit: 2})
	if err != nil || more || names(page) != "gamma" {
		t.Fatalf("page = %s, %v, %v", names(page), more, err)
	}
	page, _, err = f.svc.List(ctx, f.admin, true, core.Page{Limit: 10})
	if err != nil || names(page) != "alpha,beta,gamma,legacy-aws-role-"+f.web.ID.String() {
		t.Fatalf("with legacy = %s, %v", names(page), err)
	}
	page, _, err = f.svc.List(ctx, f.reader, false, core.Page{Limit: 10})
	if err != nil || names(page) != "alpha,beta" {
		t.Fatalf("reader = %s, %v", names(page), err)
	}
	// A stack in app reaches team's integrations, not hidden's.
	page, _, err = f.svc.StackAttachable(ctx, f.admin, "web", core.Page{Limit: 10})
	if err != nil || names(page) != "alpha,beta" {
		t.Fatalf("attachable to web = %s, %v", names(page), err)
	}
	page, _, err = f.svc.Attachable(ctx, f.admin, "hidden", core.Page{Limit: 10})
	if err != nil || names(page) != "gamma" {
		t.Fatalf("attachable in hidden = %s, %v", names(page), err)
	}
	if _, _, err := f.svc.Attachable(ctx, f.reader, "hidden", core.Page{Limit: 10}); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.StackAttachable(ctx, f.admin, "nope", core.Page{Limit: 10}); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if ok, err := f.svc.Enabled(ctx, f.acct); !ok || err != nil {
		t.Fatalf("enabled = %v, %v", ok, err)
	}
	if account, err := f.svc.InstallationAccount(ctx); account != "111111111111" || err != nil {
		t.Fatalf("installation = %q, %v", account, err)
	}
}

func TestAccUpdateAndDelete(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{})
	f.create(t, "other", "team", core.Input{})
	ctx := t.Context()
	in := core.Input{Name: "prod2", Space: "team", RoleARN: role("next"), Labels: []string{"x"}}
	got, err := f.svc.Update(ctx, f.admin, i.ID.String(), kernel.ExpectVersions(1), in)
	if err != nil || got.Name != "prod2" || got.Version != 2 || got.RoleARN != role("next") {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if _, err := f.svc.Update(ctx, f.admin, i.ID.String(),
		kernel.ExpectVersions(1), in); !errors.Is(err, kernel.ErrStale) {
		t.Fatalf("err = %v, want stale", err)
	}
	in.Name = "other"
	if _, err := f.svc.Update(ctx, f.admin, i.ID.String(), kernel.Expect{}, in); !isTaken(err) {
		t.Fatalf("err = %v, want the name taken", err)
	}
	in.Name = ""
	if _, err := f.svc.Update(ctx, f.admin, i.ID.String(), kernel.Expect{}, in); err == nil {
		t.Fatal("an update without a name succeeded")
	}
	if _, err := f.svc.Update(ctx, f.admin, "nope", kernel.Expect{},
		core.Input{Name: "a", RoleARN: role("a")}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// The writer may read app but administers no space, so moves nothing.
	if _, err := f.svc.Update(ctx, f.writer, i.ID.String(), kernel.Expect{},
		core.Input{Name: "prod2", Space: "app", RoleARN: role("next")}); !isDenied(err) {
		t.Fatalf("err = %v, want denied", err)
	}
	if _, err := f.svc.Update(ctx, f.writer, i.ID.String(), kernel.Expect{},
		core.Input{Name: "prod2", Space: "nowhere", RoleARN: role("next")}); !isMissing(err) {
		t.Fatalf("err = %v, want missing", err)
	}
	// Moving it to app keeps web, which lives in app, in reach.
	f.attach(t, got, "web", true, false)
	moved, err := f.svc.Update(ctx, f.admin, i.ID.String(), kernel.Expect{},
		core.Input{Name: "prod2", Space: "app", RoleARN: role("next")})
	if err != nil || moved.Space != f.sp["app"] {
		t.Fatalf("move = %+v, %v", moved, err)
	}
	// Moving it to hidden cuts web off.
	_, err = f.svc.Update(ctx, f.admin, i.ID.String(), kernel.Expect{},
		core.Input{Name: "prod2", Space: "hidden", RoleARN: role("next")})
	if got := pointers(t, err); !slices.Equal(got, []string{"/space"}) {
		t.Fatalf("pointers = %v", got)
	}
	if _, err := f.svc.Delete(ctx, f.admin, i.ID.String(),
		kernel.ExpectVersions(1)); !errors.Is(err, kernel.ErrStale) {
		t.Fatalf("err = %v, want stale", err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, i.ID.String(), kernel.Expect{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, i.ID.String(), kernel.Expect{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, "nope", kernel.Expect{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	stored, err := f.store.Attachments(ctx, f.acct, []kernel.ULID{f.web.ID}, true)
	if err != nil || len(stored[f.web.ID]) != 0 {
		t.Fatalf("web's attachments = %v, %v; want none after the delete", stored, err)
	}
	want := []string{
		core.ActionCreate, core.ActionCreate, core.ActionUpdate, core.ActionAttach,
		core.ActionUpdate, core.ActionDelete,
	}
	if got := f.audits(t); !slices.Equal(got, want) {
		t.Fatalf("audits = %v, want %v", got, want)
	}
}

func TestAccAttachTestsTrust(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{Region: ptr("eu-west-1")})
	ctx := t.Context()
	read := core.ExternalID("acme", i.ID, "web", core.Read)
	write := core.ExternalID("acme", i.ID, "web", core.Write)
	f.roles.trusted[i.RoleARN] = []string{read}
	_, _, err := f.svc.Attach(ctx, f.writer, "web", i.ID.String(), true, true)
	if got := pointers(t, err); !slices.Equal(got, []string{"/write"}) {
		t.Fatalf("pointers = %v", got)
	}
	if inv, _ := errors.AsType[*kernel.Invalid](err); !strings.HasPrefix(inv.Detail, core.TrustDetail) {
		t.Fatalf("detail = %q", inv.Detail)
	}
	if got := f.roles.externalIDs(); !slices.Equal(got, []string{read, write}) {
		t.Fatalf("assumed with %v", got)
	}
	a := f.roles.assumed[0]
	if a.Region != "eu-west-1" || a.Duration.Seconds() != core.MinDuration ||
		!strings.HasSuffix(a.SessionName, "@web@acme") {
		t.Fatalf("assumption = %+v", a)
	}
	att, st, err := f.svc.Attach(ctx, f.writer, "web", i.ID.String(), true, false)
	if err != nil || st.ID != f.web.ID || !att.Read || att.Write {
		t.Fatalf("attach = %+v, %+v, %v", att, st, err)
	}
	if !slices.Contains(f.stacks.holds, f.web.ID) || !slices.Contains(f.spaces.holds, f.sp["app"]) {
		t.Fatalf("holds = %v, %v", f.stacks.holds, f.spaces.holds)
	}
	// A second attachment of the same integration, or of another for the
	// read side, is refused before any role is assumed.
	other := f.create(t, "other", "team", core.Input{})
	n := len(f.roles.assumed)
	for _, tc := range []struct {
		id          kernel.ULID
		read, write bool
	}{{i.ID, false, true}, {other.ID, true, false}} {
		if _, _, err := f.svc.Attach(ctx, f.admin, "web", tc.id.String(), tc.read,
			tc.write); !isConflict(err) {
			t.Fatalf("err = %v, want a conflict", err)
		}
	}
	if len(f.roles.assumed) != n {
		t.Fatal("a refused attachment assumed the role")
	}
	f.attach(t, other, "web", false, true)
	if _, _, err := f.svc.Attach(ctx, f.admin, "web", f.create(t, "third", "", core.Input{}).ID.String(),
		false, true); !isConflict(err) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if got := f.audits(t); !slices.Equal(got, []string{
		core.ActionCreate, core.ActionAttach, core.ActionCreate, core.ActionAttach, core.ActionCreate,
	}) {
		t.Fatalf("audits = %v", got)
	}
}

func TestAccAttachRefuses(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{})
	hidden := f.create(t, "hidden", "hidden", core.Input{})
	ctx := t.Context()
	if _, _, err := f.svc.Attach(ctx, f.admin, "web", i.ID.String(), false, false); err == nil {
		t.Fatal("an attachment of no side succeeded")
	}
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		if _, _, err := f.svc.Attach(ctx, f.admin, "web", id, true, false); !isMissing(err) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
	if _, _, err := f.svc.Attach(ctx, f.writer, "web", hidden.ID.String(), true, false); !isMissing(err) {
		t.Fatalf("err = %v, want the hidden integration missing", err)
	}
	if _, _, err := f.svc.Attach(ctx, f.admin, "nope", i.ID.String(), true, false); !isMissing(err) {
		t.Fatalf("err = %v, want the stack missing", err)
	}
	_, _, err := f.svc.Attach(ctx, f.admin, "db", i.ID.String(), true, false)
	if got := pointers(t, err); !slices.Equal(got, []string{"/stackId"}) {
		t.Fatalf("pointers = %v", got)
	}
	f.roles.none = true
	if _, _, err := f.svc.Attach(ctx, f.admin, "web", i.ID.String(), true, false); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("err = %v, want unsupported", err)
	}
	// A worker assumes the role itself, so the server tests nothing.
	worker := f.create(t, "worker", "team", core.Input{Worker: true})
	if _, _, err := f.svc.Attach(ctx, f.admin, "web", worker.ID.String(), true, false); err != nil {
		t.Fatal(err)
	}
	f.roles.none, f.roles.err = false, &core.Upstream{Code: "Throttling", Err: errBroken}
	if _, _, err := f.svc.Attach(ctx, f.admin, "web", i.ID.String(), false, true); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want broken", err)
	}
	f.roles.err = nil
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "db", core.LegacyInput{RoleARN: role("l")}); err != nil {
		t.Fatal(err)
	}
	legacy, _, _, err := f.svc.Legacy(ctx, f.admin, "db")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.svc.Attach(ctx, f.admin, "web", legacy.ID.String(), false, true)
	if got := pointers(t, err); !slices.Equal(got, []string{"/awsIntegrationId"}) {
		t.Fatalf("pointers = %v", got)
	}
	f.accountsBroken(t)
	if _, _, err := f.svc.Attach(ctx, f.admin, "web", i.ID.String(), false, true); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want broken", err)
	}
}

// accountsBroken breaks the fixture's account slugs.
func (f *fixture) accountsBroken(t *testing.T) {
	t.Helper()
	f.svc = core.New(core.Deps{
		Store: f.store, Azure: pg.NewAzure(f.db), DB: f.db, Spaces: f.spaces, Accounts: accounts{err: errBroken},
		Stacks: f.stacks, Authz: f.authzOf(), Roles: f.roles,
	})
}

// authzOf is the fixture's authorizer, which the admin may use anywhere.
func (f *fixture) authzOf() authz {
	all := []accessapi.Action{
		accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin,
		accessapi.ActionStackAwsIntegrationAttach,
	}
	az := authz{f.admin.Caller(): {}}
	for _, id := range f.sp {
		az[f.admin.Caller()][id] = all
	}
	return az
}

func TestAccUpdateAttachmentAndDetach(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{})
	other := f.create(t, "other", "team", core.Input{})
	a := f.attach(t, i, "web", true, false)
	b := f.attach(t, other, "web", false, true)
	ctx := t.Context()
	if _, _, err := f.svc.UpdateAttachment(ctx, f.admin, a.ID.String(), true, true); !isConflict(err) {
		t.Fatalf("err = %v, want the write side taken", err)
	}
	if _, _, err := f.svc.UpdateAttachment(ctx, f.admin, a.ID.String(), false, false); err == nil {
		t.Fatal("an attachment of no side succeeded")
	}
	if _, _, err := f.svc.Detach(ctx, f.admin, b.ID.String()); err != nil {
		t.Fatal(err)
	}
	got, st, err := f.svc.UpdateAttachment(ctx, f.admin, a.ID.String(), false, true)
	if err != nil || got.Read || !got.Write || st.ID != f.web.ID {
		t.Fatalf("update = %+v, %v", got, err)
	}
	stored, err := f.store.Attachment(ctx, f.acct, a.ID)
	if err != nil || stored.Read || !stored.Write {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		if _, _, err := f.svc.UpdateAttachment(ctx, f.admin, id, true, false); !isMissing(err) {
			t.Fatalf("err = %v", err)
		}
		if _, _, err := f.svc.Detach(ctx, f.admin, id); !isMissing(err) {
			t.Fatalf("err = %v", err)
		}
	}
	auto := core.AutoID(i.ID, f.web.ID)
	for _, err := range []error{
		func() error { _, _, err := f.svc.UpdateAttachment(ctx, f.admin, auto, true, false); return err }(),
		func() error { _, _, err := f.svc.Detach(ctx, f.admin, auto); return err }(),
	} {
		if got := pointers(t, err); !slices.Equal(got, []string{"/attachmentId"}) {
			t.Fatalf("pointers = %v", got)
		}
	}
	// A stack's legacy role keeps both sides, and detaching it deletes it.
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "db", core.LegacyInput{RoleARN: role("l")}); err != nil {
		t.Fatal(err)
	}
	legacy, err := f.store.Attachments(ctx, f.acct, []kernel.ULID{f.data.ID}, true)
	if err != nil || len(legacy[f.data.ID]) != 1 {
		t.Fatalf("db's attachments = %v, %v", legacy, err)
	}
	l := legacy[f.data.ID][0]
	_, _, err = f.svc.UpdateAttachment(ctx, f.admin, l.ID.String(), true, false)
	if got := pointers(t, err); !slices.Equal(got, []string{"/read"}) {
		t.Fatalf("pointers = %v", got)
	}
	if _, _, err := f.svc.UpdateAttachment(ctx, f.admin, l.ID.String(), true, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Detach(ctx, f.admin, l.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := f.svc.Legacy(ctx, f.admin, "db"); found || err != nil {
		t.Fatalf("legacy = %v, %v; want it deleted", found, err)
	}
	f.roles.trusted[i.RoleARN] = []string{}
	_, _, err = f.svc.UpdateAttachment(ctx, f.admin, a.ID.String(), true, true)
	if got := pointers(t, err); !slices.Equal(got, []string{"/read", "/write"}) {
		t.Fatalf("pointers = %v", got)
	}
}

func TestAccAttachmentSpaceOf(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{})
	a := f.attach(t, i, "web", true, true)
	ctx := t.Context()
	for id, want := range map[string]kernel.ULID{
		a.ID.String():                f.sp["app"],
		core.AutoID(i.ID, f.data.ID): f.sp["hidden"],
	} {
		if got, err := f.svc.AttachmentSpaceOf(ctx, f.acct, id); err != nil || got != want {
			t.Fatalf("%s: space = %v, %v", id, got, err)
		}
	}
	for _, id := range []string{
		"nope", kernel.NewULID().String(), "auto-x", "auto-" + i.ID.String() + "-x",
		core.AutoID(i.ID, kernel.NewULID()),
	} {
		if _, err := f.svc.AttachmentSpaceOf(ctx, f.acct, id); !errors.Is(err, accessapi.ErrNoSubject) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}

func TestAccExternalID(t *testing.T) {
	f := newFixture(t)
	i := f.create(t, "prod", "team", core.Input{})
	ctx := t.Context()
	id, statement, err := f.svc.ExternalID(ctx, f.admin, i.ID.String(), "web", true, true)
	write := core.ExternalID("acme", i.ID, "web", core.Write)
	if err != nil || id != write {
		t.Fatalf("external ID = %q, %v", id, err)
	}
	if want := core.PolicyStatement("111111111111",
		core.ExternalID("acme", i.ID, "web", core.Read), write); statement != want {
		t.Fatalf("statement = %s, want %s", statement, want)
	}
	if _, _, err := f.svc.ExternalID(ctx, f.admin, i.ID.String(), "web", false, false); err == nil {
		t.Fatal("no side answered an external ID")
	}
	if _, _, err := f.svc.ExternalID(ctx, f.admin, "nope", "web", true, false); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.ExternalID(ctx, f.admin, i.ID.String(), "nope", true, false); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "web", core.LegacyInput{RoleARN: role("l")}); err != nil {
		t.Fatal(err)
	}
	legacy, _, _, err := f.svc.Legacy(ctx, f.admin, "web")
	if err != nil {
		t.Fatal(err)
	}
	if id, _, err := f.svc.ExternalID(ctx, f.admin, legacy.ID.String(), "web", true, false); err != nil ||
		id != core.LegacyExternalID("acme", "web") {
		t.Fatalf("legacy external ID = %q, %v", id, err)
	}
	f.roles.none = true
	if _, _, err := f.svc.ExternalID(ctx, f.admin, i.ID.String(), "web", true, false); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	f.accountsBroken(t)
	if _, _, err := f.svc.ExternalID(ctx, f.admin, i.ID.String(), "web", true, false); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
}

func TestAccLegacyRole(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	ext := core.LegacyExternalID("acme", "web")
	f.roles.trusted[role("l")] = []string{ext}
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "web", core.LegacyInput{RoleARN: role("bad")}); err != nil {
		t.Fatal(err)
	}
	f.roles.trusted[role("bad")] = nil
	_, _, err := f.svc.SetLegacy(ctx, f.admin, "web", core.LegacyInput{RoleARN: role("bad")})
	if got := pointers(t, err); !slices.Equal(got, []string{"/roleArn"}) {
		t.Fatalf("pointers = %v", got)
	}
	i, st, err := f.svc.SetLegacy(ctx, f.admin, "web", core.LegacyInput{
		RoleARN: role("l"), Region: ptr("us-east-2"), Duration: ptr[int32](1800),
	})
	if err != nil || !i.Legacy || i.RoleARN != role("l") || i.Version != 2 || st.ID != f.web.ID {
		t.Fatalf("legacy = %+v, %v", i, err)
	}
	if got := f.roles.externalIDs(); got[len(got)-1] != ext {
		t.Fatalf("assumed with %v", got)
	}
	statement, link, err := f.svc.LegacyPolicy(ctx, st)
	if err != nil || statement != core.PolicyStatement("111111111111", ext) ||
		!strings.Contains(link, "externalID=acme%40web") {
		t.Fatalf("policy = %s, %s, %v", statement, link, err)
	}
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "web", core.LegacyInput{RoleARN: "x"}); err == nil {
		t.Fatal("a legacy role without an ARN succeeded")
	}
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "nope", core.LegacyInput{RoleARN: role("l")}); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, _, _, err := f.svc.Legacy(ctx, f.admin, "nope"); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	got, found, err := f.svc.DeleteLegacy(ctx, f.admin, "web")
	if err != nil || !found || got.ID != i.ID {
		t.Fatalf("delete = %+v, %v, %v", got, found, err)
	}
	if _, found, err := f.svc.DeleteLegacy(ctx, f.admin, "web"); found || err != nil {
		t.Fatalf("second delete = %v, %v", found, err)
	}
	if _, _, err := f.svc.DeleteLegacy(ctx, f.admin, "nope"); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	f.roles.none = true
	if s, l, err := f.svc.LegacyPolicy(ctx, st); s != "" || l != "" || err != nil {
		t.Fatalf("policy without credentials = %q, %q, %v", s, l, err)
	}
	f.roles.none, f.roles.err = false, errBroken
	if _, _, err := f.svc.LegacyPolicy(ctx, st); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	f.roles.err = nil
	f.accountsBroken(t)
	if _, _, err := f.svc.LegacyPolicy(ctx, st); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	want := []string{core.ActionCreate, core.ActionUpdate, core.ActionDelete}
	if got := f.audits(t); !slices.Equal(got, want) {
		t.Fatalf("audits = %v, want %v", got, want)
	}
}

func TestAccAutoattach(t *testing.T) {
	f := newFixture(t)
	f.stacks.labels["web"] = []string{"prod"}
	f.stacks.labels["db"] = []string{"prod"}
	ctx := t.Context()
	reader := f.create(t, "a-read", "team", core.Input{
		Autoattach: true, Labels: []string{"autoattach_read:prod"},
	})
	every := f.create(t, "b-any", "hidden", core.Input{
		Autoattach: true, Labels: []string{"autoattach:*"},
	})
	writer := f.create(t, "c-write", "", core.Input{
		Autoattach: true, Labels: []string{"autoattach_write:prod"},
	})
	f.create(t, "d-off", "", core.Input{Labels: []string{"autoattach:prod"}})
	f.create(t, "e-none", "", core.Input{Autoattach: true, Labels: []string{"team"}})
	names := map[kernel.ULID]string{reader.ID: "a-read", every.ID: "b-any", writer.ID: "c-write"}
	// web, in app, reaches team and acme; db, in hidden, which does not
	// inherit, reaches hidden alone.
	attached, err := f.svc.Attached(ctx, f.acct, []kernel.ULID{f.web.ID, f.data.ID, kernel.NewULID()})
	if err != nil {
		t.Fatal(err)
	}
	if got := sidesOf(names, attached[f.web.ID]); got != "a-read:r,c-write:w" {
		t.Fatalf("web = %s", got)
	}
	if got := sidesOf(names, attached[f.data.ID]); got != "b-any:rw" {
		t.Fatalf("db = %s", got)
	}
	if len(attached) != 2 {
		t.Fatalf("attached = %v; want an unknown stack left out", attached)
	}
	// A stored attachment outranks a computed one, and is not computed
	// again.
	f.attach(t, writer, "web", false, true)
	got, err := f.svc.AttachedTo(ctx, reader)
	if err != nil || len(got) != 1 || !got[0].Auto || !got[0].Read || got[0].Write {
		t.Fatalf("a-read = %+v, %v", got, err)
	}
	a, err := f.svc.AttachedStack(ctx, f.admin, reader.ID.String(), got[0].APIID())
	if err != nil || a.Stack != f.web.ID {
		t.Fatalf("attached stack = %+v, %v", a, err)
	}
	if _, err := f.svc.AttachedStack(ctx, f.admin, reader.ID.String(), "nope"); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.AttachedStack(ctx, f.admin, "nope", "x"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	all, err := f.svc.AttachedTo(ctx, every)
	if err != nil || len(all) != 1 || all[0].Stack != f.data.ID {
		t.Fatalf("b-any = %+v, %v", all, err)
	}
	stored, err := f.svc.AttachedTo(ctx, writer)
	if err != nil || len(stored) != 1 || stored[0].Auto {
		t.Fatalf("c-write = %+v, %v", stored, err)
	}
	if web, err := f.svc.Attached(ctx, f.acct, []kernel.ULID{f.web.ID}); err != nil ||
		sidesOf(names, web[f.web.ID]) != "c-write:w,a-read:r" {
		t.Fatalf("web = %v, %v", web, err)
	}
	f.stacks.err = errBroken
	if _, err := f.svc.AttachedTo(ctx, reader); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.AttachedTo(ctx, every); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.Attached(ctx, f.acct, []kernel.ULID{f.web.ID}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	f.stacks.err, f.spaces.err = nil, errBroken
	if _, err := f.svc.AttachedTo(ctx, reader); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.Attached(ctx, f.acct, []kernel.ULID{f.web.ID}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
}

// sidesOf names each attachment's integration by names, with r and w for
// its sides, in order.
func sidesOf(names map[kernel.ULID]string, all []core.Attachment) string {
	out := make([]string, len(all))
	for n, a := range all {
		s := ""
		if a.Read {
			s += "r"
		}
		if a.Write {
			s += "w"
		}
		out[n] = names[a.Integration] + ":" + s
	}
	return strings.Join(out, ",")
}

func TestAccGuards(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	space, stack := f.svc.SpaceGuard(), f.svc.StackGuard()
	if in, err := space.SpaceInUse(ctx, f.acct, f.sp["team"]); in || err != nil {
		t.Fatalf("in use = %v, %v", in, err)
	}
	if lost, err := space.LostOnMove(ctx, accessapi.SpaceMove{Account: f.acct, Space: f.sp["app"]}); lost != nil || err != nil {
		t.Fatalf("lost = %v, %v", lost, err)
	}
	i := f.create(t, "prod", "team", core.Input{})
	f.attach(t, i, "web", true, true)
	if _, _, err := f.svc.SetLegacy(ctx, f.admin, "db", core.LegacyInput{RoleARN: role("l")}); err != nil {
		t.Fatal(err)
	}
	if in, err := space.SpaceInUse(ctx, f.acct, f.sp["team"]); !in || err != nil {
		t.Fatalf("in use = %v, %v", in, err)
	}
	// Moving app under hidden, which does not inherit, cuts web off team.
	lost, err := space.LostOnMove(ctx, accessapi.SpaceMove{
		Account: f.acct, Space: f.sp["app"], To: []kernel.ULID{f.sp["hidden"], f.acct.ULID},
	})
	if err != nil || len(lost) != 1 || lost[0] != (accessapi.LostReach{Stack: "web", Entity: "aws integration prod"}) {
		t.Fatalf("lost = %v, %v", lost, err)
	}
	// Moving hidden, which holds only db and its legacy role, cuts nothing.
	if lost, err := space.LostOnMove(ctx, accessapi.SpaceMove{
		Account: f.acct, Space: f.sp["hidden"], To: []kernel.ULID{f.sp["team"]},
	}); lost != nil || err != nil {
		t.Fatalf("lost = %v, %v", lost, err)
	}
	if lost, err := space.LostOnMove(ctx, accessapi.SpaceMove{
		Account: f.acct, Space: f.sp["team"],
	}); lost != nil || err != nil {
		t.Fatalf("lost = %v, %v", lost, err)
	}
	names, err := stack.LostOnMove(ctx, stackMove(f.web, f.sp["hidden"]))
	if err != nil || !slices.Equal(names, []string{"aws integration prod"}) {
		t.Fatalf("lost = %v, %v", names, err)
	}
	if names, err := stack.LostOnMove(ctx, stackMove(f.web, f.sp["team"])); names != nil || err != nil {
		t.Fatalf("lost = %v, %v", names, err)
	}
	if names, err := stack.LostOnMove(ctx, stackMove(f.data, f.sp["team"])); names != nil || err != nil {
		t.Fatalf("legacy lost = %v, %v", names, err)
	}
	for _, st := range []string{"web", "db"} {
		if err := stack.Deleting(ctx, f.stacks.all[st]); err != nil {
			t.Fatal(err)
		}
	}
	if ids, err := f.store.AttachedStacks(ctx, f.acct); err != nil || len(ids) != 0 {
		t.Fatalf("attached stacks = %v, %v", ids, err)
	}
	if _, _, found, _ := f.svc.Legacy(ctx, f.admin, "db"); found {
		t.Fatal("db's legacy role outlived it")
	}
	if _, err := f.svc.Get(ctx, f.acct, i.ID.String()); err != nil {
		t.Fatalf("prod outlived web? %v", err)
	}
	f.attach(t, i, "web", true, true)
	f.stacks.err = errBroken
	if _, err := space.LostOnMove(ctx, accessapi.SpaceMove{Account: f.acct, Space: f.sp["app"]}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	f.stacks.err, f.spaces.err = nil, errBroken
	if _, err := stack.LostOnMove(ctx, stackMove(f.web, f.sp["team"])); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := space.LostOnMove(ctx, accessapi.SpaceMove{Account: f.acct, Space: f.sp["app"]}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
}

func stackMove(st stacksapi.Stack, to kernel.ULID) stacksapi.StackMove {
	return stacksapi.StackMove{Stack: st, To: to}
}

func isMissing(err error) bool {
	_, ok := errors.AsType[*core.Missing](err)
	return ok
}

func isTaken(err error) bool {
	_, ok := errors.AsType[*core.NameTaken](err)
	return ok
}

func isConflict(err error) bool {
	_, ok := errors.AsType[*core.Conflict](err)
	return ok
}

func isDenied(err error) bool {
	_, ok := errors.AsType[*core.Denied](err)
	return ok
}

// names are the integrations' names, comma separated.
func names(all []core.Integration) string {
	out := make([]string, len(all))
	for n, i := range all {
		out[n] = i.Name
	}
	return strings.Join(out, ",")
}
