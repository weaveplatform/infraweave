package http

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var errBroken = errors.New("broken")

// store answers one worker integration and one attachment of it for every
// read, or err for every call when it is set.
type store struct {
	i   core.Integration
	a   core.Attachment
	err error
}

func (s *store) Create(context.Context, core.Integration) error { return s.err }
func (s *store) Write(context.Context, core.Integration) error  { return s.err }
func (s *store) Get(context.Context, kernel.AccountID, kernel.ULID, core.Lock) (core.Integration, error) {
	return s.i, s.err
}

func (s *store) ByName(context.Context, kernel.AccountID, string) (core.Integration, error) {
	return s.i, s.err
}

func (s *store) ByIDs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]core.Integration, error) {
	return map[kernel.ULID]core.Integration{s.i.ID: s.i}, s.err
}

func (s *store) List(context.Context, kernel.AccountID, []kernel.ULID, bool, core.Page) ([]core.Integration, bool, error) {
	return []core.Integration{s.i}, true, s.err
}

func (s *store) Labelled(context.Context, kernel.AccountID, []kernel.ULID, []string) ([]core.Integration, error) {
	return nil, s.err
}

func (s *store) InSpace(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return false, s.err
}

func (s *store) Delete(context.Context, kernel.AccountID, kernel.ULID) error { return s.err }
func (s *store) Attach(context.Context, core.Attachment) error               { return s.err }
func (s *store) SetSides(context.Context, kernel.AccountID, kernel.ULID, bool, bool) error {
	return s.err
}

func (s *store) Attachment(context.Context, kernel.AccountID, kernel.ULID) (core.Attachment, error) {
	return s.a, s.err
}

func (s *store) Detach(context.Context, kernel.AccountID, kernel.ULID) error { return s.err }

// Attachments answers the attachment as the integration's alone, so the
// stack takes another.
func (s *store) Attachments(_ context.Context, _ kernel.AccountID, _ []kernel.ULID, byStack bool) (map[kernel.ULID][]core.Attachment, error) {
	if byStack {
		return map[kernel.ULID][]core.Attachment{}, s.err
	}
	return map[kernel.ULID][]core.Attachment{s.a.Integration: {s.a}}, s.err
}

func (s *store) AttachedStacks(context.Context, kernel.AccountID) ([]kernel.ULID, error) {
	return nil, s.err
}

func (s *store) Legacy(context.Context, kernel.AccountID, kernel.ULID) (core.Integration, error) {
	return s.i, s.err
}

func (s *store) DeleteStack(context.Context, kernel.AccountID, kernel.ULID) error { return s.err }
func (s *store) Audit(context.Context, kernel.AuditEntry) error                   { return s.err }

// db begins transactions that commit and roll back nothing.
type db struct{}

type noTx struct{ pgx.Tx }

func (noTx) Commit(context.Context) error   { return nil }
func (noTx) Rollback(context.Context) error { return nil }

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return noTx{}, nil }

// spaces is one top space, whose slugs fail with slugsErr.
type spaces struct {
	top      kernel.ULID
	slugsErr error
}

func (s spaces) Hold(context.Context, kernel.AccountID, kernel.ULID) error { return nil }
func (s spaces) Find(context.Context, kernel.AccountID, string) (kernel.ULID, error) {
	return s.top, nil
}

func (s spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	return map[kernel.ULID]string{s.top: "acme"}, s.slugsErr
}

func (s spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return []accessapi.SpaceNode{{ID: s.top, Slug: "acme"}}, nil
}

// stacks is one stack in the top space; err fails each read.
type stacks struct {
	stacksapi.Stacks
	st  stacksapi.Stack
	err error
}

func (s stacks) BySlug(context.Context, kernel.AccountID, string) (stacksapi.Stack, error) {
	return s.st, s.err
}

func (s stacks) Get(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Stack, error) {
	return s.st, s.err
}

func (s stacks) ByIDs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]stacksapi.Stack, error) {
	return map[kernel.ULID]stacksapi.Stack{s.st.ID: s.st}, s.err
}

func (s stacks) Hold(context.Context, kernel.AccountID, kernel.ULID) error { return s.err }

// authz allows everything.
type authz struct{}

func (authz) Authorize(context.Context, kernel.Principal, accessapi.Action, kernel.ULID) (bool, error) {
	return true, nil
}

func (authz) Reachable(context.Context, kernel.Principal, accessapi.Action) ([]kernel.ULID, error) {
	return nil, nil
}

func (authz) Holds(context.Context, kernel.Principal, accessapi.Action) (bool, error) {
	return true, nil
}

func (authz) Actions(context.Context, kernel.Principal, kernel.ULID) ([]accessapi.Action, error) {
	return nil, nil
}

type accounts struct{}

func (accounts) Slug(context.Context, kernel.AccountID) (string, error) { return "acme", nil }

// roles fail with err.
type roles struct{ err error }

func (r roles) CallerAccount(context.Context) (string, error) { return "111111111111", r.err }
func (r roles) Assume(context.Context, core.Assumption) error { return r.err }

// fixture is a server over the fakes, as acct's principal p.
type fixture struct {
	srv   Server
	store *store
	ctx   context.Context
	id    string
}

func newFixture(st *store, sp spaces, sk stacks, r roles) fixture {
	sv := core.New(core.Deps{
		Store: st, DB: db{}, Spaces: sp, Accounts: accounts{}, Stacks: sk, Authz: authz{},
		Roles: r, Enabled: func(context.Context, kernel.AccountID) (bool, error) {
			return false, errBroken
		},
	})
	p := kernel.Principal{
		Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(),
		AccountID: st.i.Account, Login: "admin",
	}
	return fixture{
		srv: New(sv, sp, sk), store: st, ctx: kernel.WithPrincipal(context.Background(), p),
		id: st.i.ID.String(),
	}
}

// world is a store of one worker integration in the top space, attached
// to stack infra, over spaces and stacks that answer.
func world() (*store, spaces, stacks) {
	acct := kernel.NewAccountID()
	i := core.Integration{
		Account: acct, ID: kernel.NewULID(), Space: acct.ULID, Name: "prod",
		RoleARN: "arn:aws:iam::222222222222:role/prod", Duration: 3600, Worker: true,
		ExternalID: "ours", Version: 1,
	}
	st := stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "infra", Space: acct.ULID}
	a := core.Attachment{
		Account: acct, ID: kernel.NewULID(), Integration: i.ID, Stack: st.ID,
		Read: true, Write: true,
	}
	return &store{i: i, a: a}, spaces{top: acct.ULID}, stacks{st: st}
}

// fresh is a fixture over world, with an STS that answers.
func fresh() fixture {
	st, sp, sk := world()
	return newFixture(st, sp, sk, roles{})
}

// handlers call each operation of the partial with a request naming f's
// integration, its attachment and the stack infra.
func handlers(f fixture) map[string]func() error {
	ctx, s, id := f.ctx, f.srv, f.id
	att := f.store.a.ID.String()
	region := "eu-west-1"
	update := &api.AwsIntegrationUpdateRequest{
		Name: "prod", RoleArn: "arn:aws:iam::222222222222:role/prod", DurationSeconds: 3600,
		GenerateCredentialsInWorker: true, Labels: []string{}, Region: &region,
	}
	patch := api.AwsIntegrationPatchApplicationMergePatchPlusJSONRequestBody{"name": "next"}
	return map[string]func() error{
		"AwsIntegrations": func() error {
			_, err := s.AwsIntegrations(ctx, api.AwsIntegrationsRequestObject{})
			return err
		},
		"AttachableAwsIntegrationsInSpace": func() error {
			_, err := s.AttachableAwsIntegrationsInSpace(ctx,
				api.AttachableAwsIntegrationsInSpaceRequestObject{Params: api.AttachableAwsIntegrationsInSpaceParams{Id: "acme"}})
			return err
		},
		"StackAttachableAwsIntegrations": func() error {
			_, err := s.StackAttachableAwsIntegrations(ctx,
				api.StackAttachableAwsIntegrationsRequestObject{StackId: "infra"})
			return err
		},
		"AwsIntegration": func() error {
			_, err := s.AwsIntegration(ctx, api.AwsIntegrationRequestObject{AwsIntegrationId: id})
			return err
		},
		"AwsIntegrationByName": func() error {
			_, err := s.AwsIntegrationByName(ctx, api.AwsIntegrationByNameRequestObject{
				Params: api.AwsIntegrationByNameParams{Name: "prod"},
			})
			return err
		},
		"AwsIntegrationCreate": func() error {
			_, err := s.AwsIntegrationCreate(ctx, api.AwsIntegrationCreateRequestObject{
				Body: &api.AwsIntegrationCreateJSONRequestBody{
					Name: "prod", RoleArn: "arn:aws:iam::222222222222:role/prod",
					GenerateCredentialsInWorker: true, Labels: []string{},
				},
			})
			return err
		},
		"AwsIntegrationUpdate": func() error {
			_, err := s.AwsIntegrationUpdate(ctx, api.AwsIntegrationUpdateRequestObject{
				AwsIntegrationId: id, Body: update,
			})
			return err
		},
		"AwsIntegrationPatch": func() error {
			_, err := s.AwsIntegrationPatch(ctx, api.AwsIntegrationPatchRequestObject{
				AwsIntegrationId: id, Body: &patch,
			})
			return err
		},
		"AwsIntegrationDelete": func() error {
			_, err := s.AwsIntegrationDelete(ctx, api.AwsIntegrationDeleteRequestObject{AwsIntegrationId: id})
			return err
		},
		"AwsIntegrationsEnabled": func() error {
			_, err := s.AwsIntegrationsEnabled(ctx, api.AwsIntegrationsEnabledRequestObject{})
			return err
		},
		"AwsIntegrationAttachedStack": func() error {
			_, err := s.AwsIntegrationAttachedStack(ctx, api.AwsIntegrationAttachedStackRequestObject{
				AwsIntegrationId: id, Params: api.AwsIntegrationAttachedStackParams{Id: att},
			})
			return err
		},
		"AwsIntegrationExternalIdForStack": func() error {
			_, err := s.AwsIntegrationExternalIdForStack(ctx,
				api.AwsIntegrationExternalIdForStackRequestObject{
					AwsIntegrationId: id,
					Params:           api.AwsIntegrationExternalIdForStackParams{StackId: "infra", Read: true},
				})
			return err
		},
		"AwsIntegrationAttach": func() error {
			_, err := s.AwsIntegrationAttach(ctx, api.AwsIntegrationAttachRequestObject{
				StackId: "infra", AwsIntegrationId: id,
				Body: &api.AwsIntegrationAttachJSONRequestBody{Read: true},
			})
			return err
		},
		"AwsIntegrationAttachmentUpdate": func() error {
			_, err := s.AwsIntegrationAttachmentUpdate(ctx, api.AwsIntegrationAttachmentUpdateRequestObject{
				AttachmentId: att, Body: &api.AwsIntegrationAttachmentUpdateJSONRequestBody{Read: true},
			})
			return err
		},
		"AwsIntegrationDetach": func() error {
			_, err := s.AwsIntegrationDetach(ctx, api.AwsIntegrationDetachRequestObject{AttachmentId: att})
			return err
		},
		"StackIntegrationAwsCreate": func() error {
			_, err := s.StackIntegrationAwsCreate(ctx, api.StackIntegrationAwsCreateRequestObject{
				StackId: "infra", Body: &api.StackIntegrationAwsCreateRequest{
					RoleArn: "arn:aws:iam::222222222222:role/prod",
				},
			})
			return err
		},
		"StackIntegrationAwsDelete": func() error {
			_, err := s.StackIntegrationAwsDelete(ctx, api.StackIntegrationAwsDeleteRequestObject{StackId: "infra"})
			return err
		},
	}
}

// is reports whether err is a problem of type t.
func is(err error, t problem.Type) bool {
	e, ok := errors.AsType[*problem.Error](err)
	return ok && e.Problem.Status == t.Status && e.Problem.Title == t.Title
}

func TestUnitUnauthenticated(t *testing.T) {
	f := fresh()
	f.ctx = context.Background()
	for name, call := range handlers(f) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s without a principal = %v", name, err)
		}
	}
}

func TestUnitServiceFailures(t *testing.T) {
	st, sp, sk := world()
	st.err = errBroken
	f := newFixture(st, sp, sk, roles{err: errBroken})
	for name, call := range handlers(f) {
		if err := call(); err == nil {
			t.Errorf("%s over a broken store succeeded", name)
		}
	}
	if _, err := f.srv.InstallationAwsAccountId(f.ctx, api.InstallationAwsAccountIdRequestObject{}); err == nil {
		t.Error("the installation's account answered over a broken STS")
	}
}

func TestUnitViewFailures(t *testing.T) {
	st, sp, sk := world()
	sp.slugsErr = errBroken
	f := newFixture(st, sp, sk, roles{})
	for name, call := range handlers(f) {
		switch name {
		case "AwsIntegrationsEnabled", "AwsIntegrationExternalIdForStack", "StackIntegrationAwsCreate",
			"StackIntegrationAwsDelete":
			continue
		}
		if err := call(); !errors.Is(err, errBroken) {
			t.Errorf("%s with the spaces down = %v", name, err)
		}
	}
	st, sp, sk = world()
	sk.err = errBroken
	f = newFixture(st, sp, sk, roles{})
	for _, name := range []string{"AwsIntegration", "AwsIntegrationAttachedStack"} {
		if err := handlers(f)[name](); !errors.Is(err, errBroken) {
			t.Errorf("%s with the stacks down = %v", name, err)
		}
	}
	st, sp, sk = world()
	f = newFixture(st, sp, sk, roles{err: errBroken})
	if err := handlers(f)["StackIntegrationAwsCreate"](); !errors.Is(err, errBroken) {
		t.Errorf("a legacy role's view with STS down = %v", err)
	}
}

func TestUnitAnswers(t *testing.T) {
	f := fresh()
	for name, call := range handlers(f) {
		if name == "AwsIntegrationsEnabled" {
			continue
		}
		if err := call(); err != nil {
			t.Errorf("%s = %v", name, err)
		}
	}
	res, err := f.srv.AwsIntegrations(f.ctx, api.AwsIntegrationsRequestObject{})
	page, _ := res.(api.AwsIntegrations200JSONResponse)
	if err != nil || page.Body.NextCursor == nil || len(page.Body.Items) != 1 ||
		page.Body.Items[0].Labels == nil {
		t.Fatalf("page = %+v, %v", page, err)
	}
	res2, err := f.srv.InstallationAwsAccountId(f.ctx, api.InstallationAwsAccountIdRequestObject{})
	if got, _ := res2.(api.InstallationAwsAccountId200JSONResponse); err != nil || got.Body != "111111111111" {
		t.Fatalf("installation = %+v, %v", res2, err)
	}
	f.store.i.ExternalID = ""
	if _, err := f.srv.StackIntegrationAwsCreate(f.ctx, api.StackIntegrationAwsCreateRequestObject{
		StackId: "infra", Body: &api.StackIntegrationAwsCreateRequest{
			RoleArn: "arn:aws:iam::222222222222:role/prod",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnitPage(t *testing.T) {
	big, small, bad, empty := api.Limit(500), api.Limit(0), "!!", ""
	if p, err := page(nil, &big); err != nil || p.Limit != MaxLimit {
		t.Fatalf("page = %+v, %v", p, err)
	}
	if p, err := page(&empty, &small); err != nil || p.Limit != 1 || p.After != "" {
		t.Fatalf("page = %+v, %v", p, err)
	}
	if _, err := page(&bad, nil); !is(err, problem.InvalidCursor) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnitCloudError(t *testing.T) {
	for err, want := range map[error]problem.Type{
		fmt.Errorf("x: %w", core.ErrUnsupported):           problem.InstallationUnsupported,
		&core.Upstream{Code: "Throttling", Err: errBroken}: problem.UpstreamError,
		&core.Denied{Action: "SPACE_ADMIN"}:                problem.Forbidden,
		&core.Missing{Kind: "stack", ID: "x"}:              problem.NotFound,
		&core.NameTaken{Name: "x"}:                         problem.AlreadyExists,
		&core.Conflict{Detail: "taken"}:                    problem.InvalidState,
		fmt.Errorf("x: %w", core.ErrNotFound):              problem.NotFound,
	} {
		if got := cloudError(err); !is(got, want) {
			t.Errorf("%v answers %v, want %s", err, got, want.Slug)
		}
	}
	if err := cloudError(errBroken); !errors.Is(err, errBroken) {
		t.Fatalf("an unknown error answers %v", err)
	}
	for in, want := range map[string]string{"": "", "Taken": "Taken", "taken": "Taken"} {
		if got := upper(in); got != want {
			t.Errorf("upper(%q) = %q", in, got)
		}
	}
}
