package pg_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/cloud/entrafake"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/azure"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// apps is a fake Graph: the applications it holds by object ID, the
// failures it answers next, and every delete it was asked for.
type apps struct {
	mu        sync.Mutex
	held      map[string]string
	n         int
	createErr error
	deleteErr error
	deleted   []string
}

func (a *apps) Create(_ context.Context, name string) (core.Application, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.createErr != nil {
		return core.Application{}, a.createErr
	}
	if a.held == nil {
		a.held = map[string]string{}
	}
	a.n++
	id := "00000000-0000-0000-0000-00000000000" + string(rune('0'+a.n%10))
	a.held["obj-"+id] = name
	return core.Application{AppID: id, ObjectID: "obj-" + id}, nil
}

func (a *apps) Delete(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleted = append(a.deleted, id)
	if a.deleteErr != nil {
		return a.deleteErr
	}
	delete(a.held, id)
	return nil
}

func (a *apps) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.held)
}

// The customer's tenant and subscriptions in the tests.
const (
	customer = "99999999-8888-7777-6666-555555555555"
	sub1     = "11111111-1111-1111-1111-111111111111"
	sub2     = "22222222-2222-2222-2222-222222222222"
)

// createAzure creates an Azure integration named name in space as the
// admin, with in's other fields, failing the test on a refusal.
func (f *fixture) createAzure(t *testing.T, name, space string, in core.AzureInput) core.AzureIntegration {
	t.Helper()
	in.Name, in.Space = name, space
	if in.TenantID == "" {
		in.TenantID = customer
	}
	i, err := f.svc.CreateAzure(t.Context(), f.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func (f *fixture) attachAzure(t *testing.T, i core.AzureIntegration, slug string, read, write bool, sub *string) core.Attachment {
	t.Helper()
	a, _, err := f.svc.AttachAzure(t.Context(), f.admin, slug, i.ID.String(), read, write, sub)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func azureNames(all []core.AzureIntegration) string {
	out := make([]string, len(all))
	for n, i := range all {
		out[n] = i.Name
	}
	return strings.Join(out, ",")
}

func TestAccAzureCreateReadAndList(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	i := f.createAzure(t, "prod", "team", core.AzureInput{
		TenantID: strings.ToUpper(customer), DefaultSubscription: ptr(sub1), Labels: []string{"b", "a", "a"},
		Autoattach: true,
	})
	if i.TenantID != customer || i.DisplayName != "infraweave-acme-"+i.ID.String() ||
		i.ApplicationID == "" || i.ObjectID == "" || i.ServicePrincipal != nil || i.ConsentProvided ||
		!slices.Equal(i.Labels, []string{"a", "b"}) || i.Space != f.sp["team"] {
		t.Fatalf("created %+v", i)
	}
	if f.apps.count() != 1 {
		t.Fatalf("%d applications", f.apps.count())
	}
	got, err := f.svc.GetAzure(ctx, f.acct, i.ID.String())
	if err != nil || got.ObjectID != i.ObjectID || !got.CreatedAt.Equal(f.now) || got.Version != 1 {
		t.Fatalf("read %+v %v", got, err)
	}
	if _, err := f.svc.GetAzure(ctx, f.acct, "nope"); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.GetAzure(ctx, kernel.NewAccountID(), i.ID.String()); !isMissing(err) {
		t.Fatalf("another account's read: %v", err)
	}
	if space, err := f.svc.AzureSpaceOf(ctx, f.acct, i.ID.String()); err != nil || space != f.sp["team"] {
		t.Fatalf("space %v %v", space, err)
	}
	if _, err := f.svc.AzureSpaceOf(ctx, f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Fatalf("err = %v", err)
	}
	if byName, err := f.svc.AzureByName(ctx, f.reader, "prod"); err != nil || byName.ID != i.ID {
		t.Fatalf("by name %+v %v", byName, err)
	}
	f.createAzure(t, "hid", "hidden", core.AzureInput{})
	if _, err := f.svc.AzureByName(ctx, f.reader, "hid"); !isMissing(err) {
		t.Fatalf("a hidden integration by name: %v", err)
	}
	if _, err := f.svc.AzureByName(ctx, f.reader, "ghost"); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	f.createAzure(t, "app1", "app", core.AzureInput{})
	all, more, err := f.svc.ListAzure(ctx, f.admin, core.Page{Limit: 2})
	if err != nil || !more || azureNames(all) != "app1,hid" {
		t.Fatalf("first page %s %t %v", azureNames(all), more, err)
	}
	all, more, err = f.svc.ListAzure(ctx, f.admin, core.Page{After: "hid", Limit: 2})
	if err != nil || more || azureNames(all) != "prod" {
		t.Fatalf("second page %s %t %v", azureNames(all), more, err)
	}
	if all, _, err := f.svc.ListAzure(ctx, f.reader, core.Page{Limit: 10}); err != nil || azureNames(all) != "prod" {
		t.Fatalf("the reader's list %s %v", azureNames(all), err)
	}
	// A stack in app reaches app, team and acme; one in hidden reaches
	// hidden alone.
	if all, _, err := f.svc.AzureAttachable(ctx, f.admin, "app", core.Page{Limit: 10}); err != nil ||
		azureNames(all) != "app1,prod" {
		t.Fatalf("attachable in app %s %v", azureNames(all), err)
	}
	if all, _, err := f.svc.StackAzureAttachable(ctx, f.admin, "db", core.Page{Limit: 10}); err != nil ||
		azureNames(all) != "hid" {
		t.Fatalf("attachable to db %s %v", azureNames(all), err)
	}
	if _, _, err := f.svc.AzureAttachable(ctx, f.reader, "hidden", core.Page{Limit: 10}); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.StackAzureAttachable(ctx, f.admin, "ghost", core.Page{Limit: 10}); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if n, err := f.svc.AzureCount(ctx, f.acct); err != nil || n != 3 {
		t.Fatalf("count %d %v", n, err)
	}
	if n, err := f.store.Count(ctx, f.acct); err != nil || n != 0 {
		t.Fatalf("AWS count %d %v", n, err)
	}
	if ok, err := f.svc.AzureEnabled(ctx, f.acct); !ok || err != nil {
		t.Fatalf("enabled %t %v", ok, err)
	}
	if got := f.audits(t); !slices.Equal(got, []string{
		core.AzureActionCreate, core.AzureActionCreate, core.AzureActionCreate,
	}) {
		t.Fatalf("audits %v", got)
	}
}

func TestAccAzureCreateRefuses(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	cases := []struct {
		in   core.AzureInput
		want []string
	}{
		{core.AzureInput{TenantID: "acme"}, []string{"/name", "/tenantID"}},
		{core.AzureInput{Name: "x", TenantID: customer, DefaultSubscription: ptr("sub")}, []string{"/defaultSubscriptionId"}},
		{core.AzureInput{Name: "x", TenantID: customer, Autoattach: true}, []string{"/defaultSubscriptionId"}},
	}
	for _, c := range cases {
		_, err := f.svc.CreateAzure(ctx, f.admin, c.in)
		if got := pointers(t, err); !slices.Equal(got, c.want) {
			t.Errorf("%+v: pointers %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := f.svc.CreateAzure(ctx, f.reader, core.AzureInput{Name: "x", TenantID: customer, Space: "hidden"}); !isMissing(err) {
		t.Fatalf("an unreadable space: %v", err)
	}
	f.createAzure(t, "prod", "", core.AzureInput{})
	if _, err := f.svc.CreateAzure(ctx, f.admin, core.AzureInput{Name: "prod", TenantID: customer}); !isTaken(err) ||
		!strings.Contains(err.Error(), "Azure integration") {
		t.Fatalf("a taken name: %v", err)
	}
	// Graph failing creates nothing.
	f.apps.createErr = &core.GraphFailure{Status: 503, Err: errBroken}
	if _, err := f.svc.CreateAzure(ctx, f.admin, core.AzureInput{Name: "down", TenantID: customer}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	f.apps.createErr = nil
	// An account whose slug cannot be read names no application.
	svc := core.New(core.Deps{
		Store: f.store, Azure: pg.NewAzure(f.db), Applications: f.apps, DB: f.db, Spaces: f.spaces,
		Accounts: accounts{err: errBroken}, Stacks: f.stacks, Authz: f.authzOf(), Roles: f.roles,
	})
	if _, err := svc.CreateAzure(ctx, f.admin, core.AzureInput{Name: "slugless", TenantID: customer}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if n := f.apps.count(); n != 1 {
		t.Fatalf("%d applications, want prod's", n)
	}
	if n, _ := f.svc.AzureCount(ctx, f.acct); n != 1 {
		t.Fatalf("%d integrations, want prod", n)
	}
}

// TestAccAzureAbandon deletes the application of an integration whose
// row the transaction fails to write.
func TestAccAzureAbandon(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.createAzure(t, "prod", "team", core.AzureInput{})
	// The space is readable before Graph is called, and gone by the
	// transaction's hold.
	svc := core.New(core.Deps{
		Store: f.store, Azure: pg.NewAzure(f.db), Applications: f.apps, DB: f.db,
		Spaces: holdFails{f.spaces, errBroken}, Accounts: accounts{}, Stacks: f.stacks,
		Authz: f.authzOf(), Roles: f.roles, Now: func() time.Time { return f.now },
	})
	if _, err := svc.CreateAzure(ctx, f.admin, core.AzureInput{Name: "lost", TenantID: customer, Space: "team"}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if n := f.apps.count(); n != 1 || len(f.apps.deleted) != 1 {
		t.Fatalf("%d applications, deletes %v", n, f.apps.deleted)
	}
}

// TestAccAzureAbandonAdopted deletes the application a create adopted
// after Graph failed the answer to its write, when the transaction fails
// to store the integration.
func TestAccAzureAbandonAdopted(t *testing.T) {
	f := newFixture(t)
	fake := entrafake.New()
	fake.AddSecret(tenantOfOperator, clientOfOperator, "s3cret")
	srv := fake.Start()
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, srv.CA, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "EnvironmentCredential")
	t.Setenv("AZURE_TENANT_ID", tenantOfOperator)
	t.Setenv("AZURE_CLIENT_ID", clientOfOperator)
	t.Setenv("AZURE_CLIENT_SECRET", "s3cret")
	graph := azure.Applications{C: azure.New(azure.Settings{
		TenantID: tenantOfOperator, ClientID: clientOfOperator, AuthorityHost: srv.URL + "/",
		GraphEndpoint: srv.URL, CAFile: ca,
	}, azure.Options{Backoff: time.Millisecond, Deadline: 5 * time.Second})}
	svc := core.New(core.Deps{
		Store: f.store, Azure: pg.NewAzure(f.db), Applications: graph, DB: f.db,
		Spaces: holdFails{f.spaces, errBroken}, Accounts: accounts{}, Stacks: f.stacks,
		Authz: f.authzOf(), Roles: f.roles, Now: func() time.Time { return f.now },
	})
	fake.FailNext(entrafake.FaultCommitted, 1)
	_, err := svc.CreateAzure(t.Context(), f.admin, core.AzureInput{Name: "lost", TenantID: customer, Space: "team"})
	if !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if apps := fake.Applications(); len(apps) != 0 {
		t.Errorf("the adopted application survived: %+v", apps)
	}
	var posts, deletes int
	for _, c := range fake.Calls() {
		switch {
		case c.Method == http.MethodPost && c.Path == "/v1.0/applications":
			posts++
		case c.Method == http.MethodDelete:
			deletes++
		}
	}
	if posts != 1 || deletes != 1 {
		t.Errorf("%d creates and %d deletes, want one of each", posts, deletes)
	}
}

// The operator's tenant and Entra application in the tests that reach
// the Entra fake.
const (
	tenantOfOperator = "44444444-3333-2222-1111-000000000000"
	clientOfOperator = "aaaaaaaa-1111-2222-3333-444444444444"
)

// holdFails is spaces whose holds fail with err.
type holdFails struct {
	*spaces
	err error
}

func (h holdFails) Hold(context.Context, kernel.AccountID, kernel.ULID) error { return h.err }

func TestAccAzureUpdateConsentAndDelete(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	i := f.createAzure(t, "prod", "team", core.AzureInput{})
	f.now = f.now.Add(time.Minute)
	up, err := f.svc.UpdateAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}, core.AzureInput{
		Name: "renamed", Space: "team", TenantID: "ignored", DefaultSubscription: ptr(sub1),
		Labels: []string{"x"}, Autoattach: true,
	})
	if err != nil || up.Name != "renamed" || up.TenantID != customer || up.Version != 2 ||
		!up.UpdatedAt.Equal(f.now) || *up.DefaultSubscription != sub1 || !up.Autoattach {
		t.Fatalf("update %+v %v", up, err)
	}
	if _, err := f.svc.UpdateAzure(ctx, f.admin, i.ID.String(), kernel.ExpectVersions(1),
		core.AzureInput{Name: "stale"}); !errors.Is(err, kernel.ErrStale) {
		t.Fatalf("a stale update: %v", err)
	}
	if _, err := f.svc.UpdateAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}, core.AzureInput{}); pointers(t, err)[0] != "/name" {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.UpdateAzure(ctx, f.admin, "nope", kernel.Expect{}, core.AzureInput{Name: "x"}); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	// A move needs SPACE_ADMIN where it goes, and is refused while a stack
	// attached by hand would lose it.
	f.attachAzure(t, up, "web", true, false, nil)
	_, err = f.svc.UpdateAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}, core.AzureInput{
		Name: "renamed", Space: "hidden",
	})
	if !slices.Equal(pointers(t, err), []string{"/space"}) ||
		!strings.Contains(err.Error(), "Stack web would no longer reach Azure integration renamed") {
		t.Fatalf("a move: %v", err)
	}
	if _, err := f.svc.UpdateAzure(ctx, f.writer, i.ID.String(), kernel.Expect{}, core.AzureInput{
		Name: "renamed", Space: "app",
	}); !isDenied(err) {
		t.Fatalf("a move without admin: %v", err)
	}
	moved, err := f.svc.UpdateAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}, core.AzureInput{
		Name: "renamed", Space: "app",
	})
	if err != nil || moved.Space != f.sp["app"] {
		t.Fatalf("a move to app: %+v %v", moved, err)
	}

	f.now = f.now.Add(time.Minute)
	c, err := f.svc.SetConsent(ctx, f.admin, i.ID.String(), true)
	if err != nil || !c.ConsentProvided || *c.ConsentActor != "admin" || !c.ConsentAt.Equal(f.now) {
		t.Fatalf("confirm %+v %v", c, err)
	}
	c, err = f.svc.SetConsent(ctx, f.writer, i.ID.String(), false)
	if err != nil || c.ConsentProvided || *c.ConsentActor != "writer" {
		t.Fatalf("clear %+v %v", c, err)
	}
	if _, err := f.svc.SetConsent(ctx, f.admin, "nope", true); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}

	if _, err := f.svc.DeleteAzure(ctx, f.admin, i.ID.String(), kernel.ExpectVersions(1)); !errors.Is(err, kernel.ErrStale) {
		t.Fatalf("a stale delete: %v", err)
	}
	// Graph failing keeps the integration for a retry.
	f.apps.deleteErr = &core.GraphFailure{Status: 503, Err: errBroken}
	if _, err := f.svc.DeleteAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.GetAzure(ctx, f.acct, i.ID.String()); err != nil {
		t.Fatalf("the integration went with Graph failing: %v", err)
	}
	f.apps.deleteErr = nil
	gone, err := f.svc.DeleteAzure(ctx, f.admin, i.ID.String(), kernel.Expect{})
	if err != nil || gone.ID != i.ID || f.apps.count() != 0 {
		t.Fatalf("delete %+v %v, %d applications", gone, err, f.apps.count())
	}
	if _, err := f.svc.DeleteAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}); !isMissing(err) {
		t.Fatalf("a second delete: %v", err)
	}
	if ids, _ := pg.NewAzure(f.db).AttachedStacks(ctx, f.acct); len(ids) != 0 {
		t.Fatalf("attachments outlived the integration: %v", ids)
	}
	want := []string{
		core.AzureActionCreate, core.AzureActionUpdate, core.AzureActionAttach, core.AzureActionUpdate,
		core.AzureActionConfirmConsent, core.AzureActionClearConsent, core.AzureActionDelete,
	}
	if got := f.audits(t); !slices.Equal(got, want) {
		t.Fatalf("audits %v, want %v", got, want)
	}
}

// TestAccAzureUnsupported checks that an installation without cloud.azure
// creates and deletes nothing, and reports Azure integrations disabled.
func TestAccAzureUnsupported(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	i := f.createAzure(t, "prod", "team", core.AzureInput{})
	svc := core.New(core.Deps{
		Store: f.store, Azure: pg.NewAzure(f.db), DB: f.db, Spaces: f.spaces, Accounts: accounts{},
		Stacks: f.stacks, Authz: f.authzOf(), Roles: f.roles,
		Enabled: func(context.Context, kernel.AccountID) (bool, error) { return true, nil },
	})
	if _, err := svc.CreateAzure(ctx, f.admin, core.AzureInput{Name: "x", TenantID: customer}); !errors.Is(err, core.ErrAzureUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.DeleteAzure(ctx, f.admin, i.ID.String(), kernel.Expect{}); !errors.Is(err, core.ErrAzureUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if ok, err := svc.AzureEnabled(ctx, f.acct); ok || err != nil {
		t.Fatalf("enabled %t %v", ok, err)
	}
	// The rows of a deleted account go without Graph.
	if done, err := svc.PurgeAzure(ctx, f.acct, 10); done || err != nil {
		t.Fatalf("purge %t %v", done, err)
	}
	if done, err := svc.PurgeAzure(ctx, f.acct, 10); !done || err != nil {
		t.Fatalf("a second purge %t %v", done, err)
	}
	if f.apps.count() != 1 {
		t.Fatal("the purge reached Graph without cloud.azure")
	}
}

func TestAccAzureAttachments(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	prod := f.createAzure(t, "prod", "team", core.AzureInput{})
	other := f.createAzure(t, "other", "team", core.AzureInput{})
	hid := f.createAzure(t, "hid", "hidden", core.AzureInput{})
	a, st, err := f.svc.AttachAzure(ctx, f.admin, "web", prod.ID.String(), true, false, ptr(sub1))
	if err != nil || st.Slug != "web" || *a.Subscription != sub1 || !a.Read || a.Write {
		t.Fatalf("attach %+v %v", a, err)
	}
	for _, c := range []struct {
		name           string
		slug, id       string
		read, write    bool
		sub            *string
		missing, taken bool
		pointers       []string
	}{
		{name: "no side", slug: "web", id: other.ID.String(), pointers: []string{"/read"}},
		{name: "bad subscription", slug: "web", id: other.ID.String(), read: true, sub: ptr("x"), pointers: []string{"/subscriptionId"}},
		{name: "read taken", slug: "web", id: other.ID.String(), read: true, taken: true},
		{name: "attached already", slug: "web", id: prod.ID.String(), write: true, taken: true},
		{name: "out of reach", slug: "web", id: hid.ID.String(), write: true, pointers: []string{"/stackId"}},
		{name: "unknown integration", slug: "web", id: "nope", write: true, missing: true},
		{name: "unknown stack", slug: "ghost", id: other.ID.String(), write: true, missing: true},
	} {
		_, _, err := f.svc.AttachAzure(ctx, f.admin, c.slug, c.id, c.read, c.write, c.sub)
		switch {
		case c.missing && !isMissing(err), c.taken && !isConflict(err):
			t.Errorf("%s: %v", c.name, err)
		case c.pointers != nil && !slices.Equal(pointers(t, err), c.pointers):
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, _, err := f.svc.AttachAzure(ctx, f.reader, "db", hid.ID.String(), true, false, nil); !isMissing(err) {
		t.Fatalf("an integration the caller cannot read: %v", err)
	}
	w := f.attachAzure(t, other, "web", false, true, nil)
	if space, err := f.svc.AzureAttachmentSpaceOf(ctx, f.acct, w.ID.String()); err != nil || space != f.sp["app"] {
		t.Fatalf("space %v %v", space, err)
	}
	if _, err := f.svc.AzureAttachmentSpaceOf(ctx, f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Fatalf("err = %v", err)
	}
	// Each side has one attachment: swapping needs one freed first.
	if _, _, err := f.svc.UpdateAzureAttachment(ctx, f.admin, a.ID.String(), true, true, nil); !isConflict(err) {
		t.Fatalf("a second writer: %v", err)
	}
	if _, _, err := f.svc.UpdateAzureAttachment(ctx, f.admin, a.ID.String(), false, false, nil); pointers(t, err)[0] != "/read" {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.UpdateAzureAttachment(ctx, f.admin, a.ID.String(), true, false, ptr("x")); pointers(t, err)[0] != "/subscriptionId" {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.UpdateAzureAttachment(ctx, f.admin, "nope", true, false, nil); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := f.svc.UpdateAzureAttachment(ctx, f.admin, core.AutoPrefix+"x", true, false, nil); err == nil {
		t.Fatal("an automatic attachment was updated")
	}
	up, _, err := f.svc.UpdateAzureAttachment(ctx, f.admin, a.ID.String(), true, false, ptr(sub2))
	if err != nil || *up.Subscription != sub2 {
		t.Fatalf("update %+v %v", up, err)
	}
	stored, err := pg.NewAzure(f.db).Attachment(ctx, f.acct, a.ID)
	if err != nil || *stored.Subscription != sub2 {
		t.Fatalf("stored %+v %v", stored, err)
	}
	attached, err := f.svc.AzureAttached(ctx, f.acct, []kernel.ULID{f.web.ID})
	if err != nil || len(attached[f.web.ID]) != 2 {
		t.Fatalf("attached %+v %v", attached, err)
	}
	if got, err := f.svc.AzureAttachedStack(ctx, f.admin, prod.ID.String(), a.ID.String()); err != nil || got.ID != a.ID {
		t.Fatalf("attached stack %+v %v", got, err)
	}
	if _, err := f.svc.AzureAttachedStack(ctx, f.admin, prod.ID.String(), w.ID.String()); !isMissing(err) {
		t.Fatalf("another integration's attachment: %v", err)
	}
	if _, err := f.svc.AzureAttachedStack(ctx, f.admin, "nope", w.ID.String()); !isMissing(err) {
		t.Fatalf("err = %v", err)
	}
	gone, st, err := f.svc.DetachAzure(ctx, f.admin, a.ID.String())
	if err != nil || gone.ID != a.ID || st.Slug != "web" {
		t.Fatalf("detach %+v %v", gone, err)
	}
	if _, _, err := f.svc.DetachAzure(ctx, f.admin, a.ID.String()); !isMissing(err) {
		t.Fatalf("a second detach: %v", err)
	}
	if _, _, err := f.svc.DetachAzure(ctx, f.admin, core.AutoPrefix+"x"); err == nil {
		t.Fatal("an automatic attachment was detached")
	}
	want := []string{
		core.AzureActionCreate, core.AzureActionCreate, core.AzureActionCreate, core.AzureActionAttach,
		core.AzureActionAttach, core.AzureActionAttachment, core.AzureActionDetach,
	}
	if got := f.audits(t); !slices.Equal(got, want) {
		t.Fatalf("audits %v, want %v", got, want)
	}
}

func TestAccAzureAutoattach(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.stacks.labels["web"] = []string{"prod"}
	i := f.createAzure(t, "prod", "team", core.AzureInput{
		Labels: []string{"autoattach:prod"}, Autoattach: true, DefaultSubscription: ptr(sub1),
	})
	f.createAzure(t, "noauto", "team", core.AzureInput{Labels: []string{"autoattach:prod"}})
	all, err := f.svc.AzureAttachedTo(ctx, i)
	if err != nil || len(all) != 1 || !all[0].Auto || !all[0].Read || !all[0].Write || all[0].Stack != f.web.ID {
		t.Fatalf("automatic %+v %v", all, err)
	}
	got, err := f.svc.AzureAttachedStack(ctx, f.admin, i.ID.String(), all[0].APIID())
	if err != nil || got.Stack != f.web.ID {
		t.Fatalf("attached stack %+v %v", got, err)
	}
	if space, err := f.svc.AzureAttachmentSpaceOf(ctx, f.acct, all[0].APIID()); err != nil || space != f.sp["app"] {
		t.Fatalf("space %v %v", space, err)
	}
	attached, err := f.svc.AzureAttached(ctx, f.acct, []kernel.ULID{f.web.ID, f.data.ID})
	if err != nil || len(attached[f.web.ID]) != 1 || len(attached[f.data.ID]) != 0 {
		t.Fatalf("attached %+v %v", attached, err)
	}
	// A stored attachment outranks the label's, which is not computed
	// again.
	f.attachAzure(t, i, "web", true, false, ptr(sub2))
	attached, err = f.svc.AzureAttached(ctx, f.acct, []kernel.ULID{f.web.ID})
	if a := attached[f.web.ID]; err != nil || len(a) != 1 || a[0].Auto || *a[0].Subscription != sub2 {
		t.Fatalf("attached %+v %v", attached, err)
	}
}

func TestAccAzureGuards(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	space, stack := f.svc.SpaceGuard(), f.svc.StackGuard()
	i := f.createAzure(t, "prod", "team", core.AzureInput{})
	if in, err := space.SpaceInUse(ctx, f.acct, f.sp["team"]); !in || err != nil {
		t.Fatalf("in use = %v, %v", in, err)
	}
	if in, err := space.SpaceInUse(ctx, f.acct, f.sp["app"]); in || err != nil {
		t.Fatalf("in use = %v, %v", in, err)
	}
	f.attachAzure(t, i, "web", true, true, nil)
	aws := f.create(t, "aws", "team", core.Input{})
	f.attach(t, aws, "web", true, true)
	lost, err := space.LostOnMove(ctx, accessapi.SpaceMove{
		Account: f.acct, Space: f.sp["app"], To: []kernel.ULID{f.sp["hidden"], f.acct.ULID},
	})
	if err != nil || len(lost) != 2 || lost[0].Entity != "aws integration aws" ||
		lost[1] != (accessapi.LostReach{Stack: "web", Entity: "azure integration prod"}) {
		t.Fatalf("lost = %v, %v", lost, err)
	}
	names, err := stack.LostOnMove(ctx, stackMove(f.web, f.sp["hidden"]))
	if err != nil || !slices.Equal(names, []string{"aws integration aws", "azure integration prod"}) {
		t.Fatalf("lost = %v, %v", names, err)
	}
	if err := stack.Deleting(ctx, f.web); err != nil {
		t.Fatal(err)
	}
	if ids, err := pg.NewAzure(f.db).AttachedStacks(ctx, f.acct); err != nil || len(ids) != 0 {
		t.Fatalf("attached stacks = %v, %v", ids, err)
	}
}

// TestAccAzureStore checks the Azure store's unique keys and checks.
func TestAccAzureStore(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := pg.NewAzure(f.db)
	i := f.createAzure(t, "prod", "team", core.AzureInput{})
	dup := i
	dup.ID = kernel.NewULID()
	if err := s.Create(ctx, dup); !isTaken(err) {
		t.Fatalf("a taken name: %v", err)
	}
	other := f.createAzure(t, "other", "team", core.AzureInput{})
	other.Name = "prod"
	if err := s.Write(ctx, other); !isTaken(err) {
		t.Fatalf("a rename to a taken name: %v", err)
	}
	ghost := i
	ghost.ID = kernel.NewULID()
	if err := s.Write(ctx, ghost); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// An automatic attachment needs a default subscription.
	bad := i
	bad.Autoattach = true
	if err := s.Write(ctx, bad); err == nil || isTaken(err) {
		t.Fatalf("an auto-attaching integration without a subscription: %v", err)
	}
	if _, err := s.Get(ctx, f.acct, kernel.NewULID(), core.NoLock); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetAttachment(ctx, f.acct, kernel.NewULID(), true, false, nil); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Attachment(ctx, f.acct, kernel.NewULID()); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	a := core.Attachment{
		ID: kernel.NewULID(), Account: f.acct, Integration: i.ID, Stack: f.web.ID, Read: true, CreatedAt: f.now,
	}
	if err := s.Attach(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		a    core.Attachment
		want string
	}{
		{core.Attachment{Integration: i.ID, Write: true}, "attached to the stack already"},
		{core.Attachment{Integration: other.ID, Read: true}, "read Azure attachment"},
	} {
		c.a.ID, c.a.Account, c.a.Stack, c.a.CreatedAt = kernel.NewULID(), f.acct, f.web.ID, f.now
		if err := s.Attach(ctx, c.a); !isConflict(err) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("err = %v, want %s", err, c.want)
		}
	}
	w := core.Attachment{
		ID: kernel.NewULID(), Account: f.acct, Integration: other.ID, Stack: f.web.ID, Write: true, CreatedAt: f.now,
	}
	if err := s.Attach(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAttachment(ctx, f.acct, a.ID, true, true, nil); !isConflict(err) ||
		!strings.Contains(err.Error(), "write Azure attachment") {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetAttachment(ctx, f.acct, a.ID, false, false, nil); err == nil || isConflict(err) {
		t.Fatalf("an attachment of no side: %v", err)
	}
}

// TestAccAzurePurge purges a deleted account's Azure integrations with
// their applications, a batch at a time.
func TestAccAzurePurge(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.createAzure(t, "a", "team", core.AzureInput{})
	f.createAzure(t, "b", "team", core.AzureInput{})
	f.attachAzure(t, a, "web", true, false, nil)
	f.apps.deleteErr = errBroken
	if _, err := f.svc.PurgeAzure(ctx, f.acct, 1); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	f.apps.deleteErr = nil
	for _, want := range []struct {
		done bool
		left int
	}{{false, 1}, {false, 0}, {true, 0}} {
		done, err := f.svc.PurgeAzure(ctx, f.acct, 1)
		if n, _ := f.svc.AzureCount(ctx, f.acct); err != nil || done != want.done || n != want.left {
			t.Fatalf("a batch: done %t, %v, %d left, want %+v", done, err, n, want)
		}
	}
	if f.apps.count() != 0 {
		t.Fatalf("%d applications outlived the purge", f.apps.count())
	}
	if ids, _ := pg.NewAzure(f.db).AttachedStacks(ctx, f.acct); len(ids) != 0 {
		t.Fatal("attachments outlived the purge")
	}
}

// TestAccAzureStoreUnavailable checks that every read and write of the
// Azure store on a closed pool fails, and not as a missing row or a
// refusal.
func TestAccAzureStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.NewAzure(db)
	db.Close()
	ctx := t.Context()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	i := core.AzureIntegration{Account: acct, ID: id, Name: "x"}
	_, getErr := s.Get(ctx, acct, id, core.NoLock)
	_, byIDsErr := s.ByIDs(ctx, acct, []kernel.ULID{id})
	_, _, listErr := s.List(ctx, acct, nil, core.Page{Limit: 1})
	_, labelErr := s.Labelled(ctx, acct, nil, []string{"x"})
	_, inErr := s.InSpace(ctx, acct, id)
	_, countErr := s.Count(ctx, acct)
	_, oldErr := s.Oldest(ctx, acct, 1)
	_, attErr := s.Attachment(ctx, acct, id)
	_, attsErr := s.Attachments(ctx, acct, []kernel.ULID{id}, false)
	_, stacksErr := s.AttachedStacks(ctx, acct)
	for n, err := range []error{
		s.Create(ctx, i), s.Write(ctx, i), getErr, byIDsErr, listErr, labelErr, inErr, countErr,
		oldErr, s.Delete(ctx, acct, id), s.Attach(ctx, core.Attachment{Account: acct, ID: id}),
		s.SetAttachment(ctx, acct, id, true, false, nil), attErr, s.Detach(ctx, acct, id), attsErr,
		stacksErr, s.DeleteStack(ctx, acct, id),
	} {
		if err == nil || errors.Is(err, core.ErrNotFound) || isTaken(err) || isConflict(err) {
			t.Errorf("call %d: err = %v", n, err)
		}
	}
}
