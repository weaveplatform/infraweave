package http

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// azureStore answers one Azure integration and one attachment of it for
// every read, or err for every call when it is set.
type azureStore struct {
	i   core.AzureIntegration
	a   core.Attachment
	err error
}

func (s *azureStore) Create(context.Context, core.AzureIntegration) error { return s.err }
func (s *azureStore) Write(context.Context, core.AzureIntegration) error  { return s.err }
func (s *azureStore) Get(context.Context, kernel.AccountID, kernel.ULID, core.Lock) (core.AzureIntegration, error) {
	return s.i, s.err
}

// ByName finds the integration by its name alone.
func (s *azureStore) ByName(_ context.Context, _ kernel.AccountID, name string) (core.AzureIntegration, error) {
	if s.err == nil && name != s.i.Name {
		return core.AzureIntegration{}, core.ErrNotFound
	}
	return s.i, s.err
}

func (s *azureStore) ByIDs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]core.AzureIntegration, error) {
	return map[kernel.ULID]core.AzureIntegration{s.i.ID: s.i}, s.err
}

func (s *azureStore) List(context.Context, kernel.AccountID, []kernel.ULID, core.Page) ([]core.AzureIntegration, bool, error) {
	return []core.AzureIntegration{s.i}, true, s.err
}

func (s *azureStore) Labelled(context.Context, kernel.AccountID, []kernel.ULID, []string) ([]core.AzureIntegration, error) {
	return nil, s.err
}

func (s *azureStore) InSpace(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return false, s.err
}

func (s *azureStore) Delete(context.Context, kernel.AccountID, kernel.ULID) error { return s.err }
func (s *azureStore) Count(context.Context, kernel.AccountID) (int, error)        { return 1, s.err }

func (s *azureStore) Oldest(context.Context, kernel.AccountID, int) ([]core.AzureIntegration, error) {
	return nil, s.err
}
func (s *azureStore) Attach(context.Context, core.Attachment) error { return s.err }
func (s *azureStore) SetAttachment(context.Context, kernel.AccountID, kernel.ULID, bool, bool, *string) error {
	return s.err
}

func (s *azureStore) Attachment(context.Context, kernel.AccountID, kernel.ULID) (core.Attachment, error) {
	return s.a, s.err
}

func (s *azureStore) Detach(context.Context, kernel.AccountID, kernel.ULID) error { return s.err }

// Attachments answers the attachment for a stack's read alone, so an
// attach in the tests finds neither side taken, and for the integration.
func (s *azureStore) Attachments(_ context.Context, _ kernel.AccountID, _ []kernel.ULID, byStack bool) (map[kernel.ULID][]core.Attachment, error) {
	if byStack {
		return nil, s.err
	}
	return map[kernel.ULID][]core.Attachment{s.i.ID: {s.a}}, s.err
}

func (s *azureStore) AttachedStacks(context.Context, kernel.AccountID) ([]kernel.ULID, error) {
	return nil, s.err
}

func (s *azureStore) DeleteStack(context.Context, kernel.AccountID, kernel.ULID) error {
	return s.err
}

// graph is a fake Graph failing with err.
type graph struct{ err error }

func (g graph) Create(context.Context, string) (core.Application, error) {
	return core.Application{AppID: "app", ObjectID: "obj"}, g.err
}

func (g graph) Delete(context.Context, string) error { return g.err }

// azureFixture is a server over world's fakes with one Azure integration
// in the top space attached to stack infra, and Graph answering apps;
// nil apps is an installation without cloud.azure.
type azureFixture struct {
	srv   Server
	azure *azureStore
	ctx   context.Context
	id    string
}

func newAzureFixture(az *azureStore, apps core.Applications, mutate func(*store, *spaces, *stacks)) azureFixture {
	st, sp, sk := world()
	if mutate != nil {
		mutate(st, &sp, &sk)
	}
	acct := st.i.Account
	if az == nil {
		az = &azureStore{}
	}
	az.i = core.AzureIntegration{
		Account: acct, ID: kernel.NewULID(), Space: acct.ULID, Name: "prod",
		TenantID: "99999999-8888-7777-6666-555555555555", ApplicationID: "app", ObjectID: "obj",
		DisplayName: "infraweave-acme-x", Labels: nil, Version: 1,
		CreatedAt: time.Unix(1_800_000_000, 0), UpdatedAt: time.Unix(1_800_000_060, 0),
	}
	az.a = core.Attachment{
		Account: acct, ID: kernel.NewULID(), Integration: az.i.ID, Stack: sk.st.ID, Read: true,
		Subscription: new("11111111-1111-1111-1111-111111111111"),
	}
	sv := core.New(core.Deps{
		Store: st, Azure: az, Applications: apps, DB: db{}, Spaces: sp, Accounts: accounts{},
		Stacks: sk, Authz: authz{}, Roles: roles{},
		Enabled: func(context.Context, kernel.AccountID) (bool, error) { return true, nil },
	})
	p := kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID(), AccountID: acct, Login: "admin"}
	return azureFixture{
		srv: New(sv, sp, sk), azure: az, ctx: kernel.WithPrincipal(context.Background(), p),
		id: az.i.ID.String(),
	}
}

// azureHandlers call each Azure operation of the partial with a request
// naming f's integration, its attachment and the stack infra.
func azureHandlers(f azureFixture) map[string]func() error {
	ctx, s, id := f.ctx, f.srv, f.id
	att := f.azure.a.ID.String()
	update := &api.AzureIntegrationUpdateRequest{Name: "prod", Labels: []string{}}
	patch := api.AzureIntegrationPatchApplicationMergePatchPlusJSONRequestBody{"name": "next"}
	return map[string]func() error{
		"AzureIntegrations": func() error {
			_, err := s.AzureIntegrations(ctx, api.AzureIntegrationsRequestObject{})
			return err
		},
		"AttachableAzureIntegrationsInSpace": func() error {
			_, err := s.AttachableAzureIntegrationsInSpace(ctx, api.AttachableAzureIntegrationsInSpaceRequestObject{
				Params: api.AttachableAzureIntegrationsInSpaceParams{Id: "acme"},
			})
			return err
		},
		"StackAttachableAzureIntegrations": func() error {
			_, err := s.StackAttachableAzureIntegrations(ctx,
				api.StackAttachableAzureIntegrationsRequestObject{StackId: "infra"})
			return err
		},
		"AzureIntegration": func() error {
			_, err := s.AzureIntegration(ctx, api.AzureIntegrationRequestObject{AzureIntegrationId: id})
			return err
		},
		"AzureIntegrationByName": func() error {
			_, err := s.AzureIntegrationByName(ctx, api.AzureIntegrationByNameRequestObject{
				Params: api.AzureIntegrationByNameParams{Name: "prod"},
			})
			return err
		},
		"AzureIntegrationCreate": func() error {
			_, err := s.AzureIntegrationCreate(ctx, api.AzureIntegrationCreateRequestObject{
				Body: &api.AzureIntegrationCreateJSONRequestBody{
					Name: "next", TenantID: "99999999-8888-7777-6666-555555555555", Labels: []string{},
				},
			})
			return err
		},
		"AzureIntegrationUpdate": func() error {
			_, err := s.AzureIntegrationUpdate(ctx, api.AzureIntegrationUpdateRequestObject{
				AzureIntegrationId: id, Body: update,
			})
			return err
		},
		"AzureIntegrationPatch": func() error {
			_, err := s.AzureIntegrationPatch(ctx, api.AzureIntegrationPatchRequestObject{
				AzureIntegrationId: id, Body: &patch,
			})
			return err
		},
		"AzureIntegrationDelete": func() error {
			_, err := s.AzureIntegrationDelete(ctx, api.AzureIntegrationDeleteRequestObject{AzureIntegrationId: id})
			return err
		},
		"AzureIntegrationsEnabled": func() error {
			_, err := s.AzureIntegrationsEnabled(ctx, api.AzureIntegrationsEnabledRequestObject{})
			return err
		},
		"AzureIntegrationAttachedStack": func() error {
			_, err := s.AzureIntegrationAttachedStack(ctx, api.AzureIntegrationAttachedStackRequestObject{
				AzureIntegrationId: id, Params: api.AzureIntegrationAttachedStackParams{Id: att},
			})
			return err
		},
		"AzureIntegrationAttach": func() error {
			_, err := s.AzureIntegrationAttach(ctx, api.AzureIntegrationAttachRequestObject{
				StackId: "infra", AzureIntegrationId: id,
				Body: &api.AzureIntegrationAttachJSONRequestBody{Write: true},
			})
			return err
		},
		"AzureIntegrationAttachmentUpdate": func() error {
			_, err := s.AzureIntegrationAttachmentUpdate(ctx, api.AzureIntegrationAttachmentUpdateRequestObject{
				AttachmentId: att, Body: &api.AzureIntegrationAttachmentUpdateJSONRequestBody{Read: true},
			})
			return err
		},
		"AzureIntegrationDetach": func() error {
			_, err := s.AzureIntegrationDetach(ctx, api.AzureIntegrationDetachRequestObject{AttachmentId: att})
			return err
		},
		"AzureIntegrationConsentConfirm": func() error {
			_, err := s.AzureIntegrationConsentConfirm(ctx, api.AzureIntegrationConsentConfirmRequestObject{
				AzureIntegrationId: id,
			})
			return err
		},
		"AzureIntegrationConsentClear": func() error {
			_, err := s.AzureIntegrationConsentClear(ctx, api.AzureIntegrationConsentClearRequestObject{
				AzureIntegrationId: id,
			})
			return err
		},
	}
}

func TestUnitAzureUnauthenticated(t *testing.T) {
	f := newAzureFixture(nil, graph{}, nil)
	f.ctx = context.Background()
	for name, call := range azureHandlers(f) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s without a principal = %v", name, err)
		}
	}
}

func TestUnitAzureAnswers(t *testing.T) {
	f := newAzureFixture(nil, graph{}, nil)
	for name, call := range azureHandlers(f) {
		if err := call(); err != nil {
			t.Errorf("%s = %v", name, err)
		}
	}
	res, err := f.srv.AzureIntegration(f.ctx, api.AzureIntegrationRequestObject{AzureIntegrationId: f.id})
	got, _ := res.(api.AzureIntegration200JSONResponse)
	v := got.Body
	if err != nil || v.AdminConsentURL != "https://login.microsoftonline.com/99999999-8888-7777-6666-555555555555/adminconsent?client_id=app" ||
		v.CreatedAt != 1_800_000_000 || v.UpdatedAt != 1_800_000_060 || v.Labels == nil ||
		v.ServicePrincipalObjectId != nil || len(v.AttachedStacks) != 1 || !v.HasAttachedStacks ||
		*v.AttachedStacks[0].SubscriptionId != "11111111-1111-1111-1111-111111111111" ||
		v.AttachedStacks[0].IsModule || *got.Headers.ETag == "" {
		t.Fatalf("view %+v, %v", got, err)
	}
	list, err := f.srv.AzureIntegrations(f.ctx, api.AzureIntegrationsRequestObject{})
	page, _ := list.(api.AzureIntegrations200JSONResponse)
	if err != nil || page.Body.NextCursor == nil || len(page.Body.Items) != 1 {
		t.Fatalf("page %+v, %v", page, err)
	}
	enabled, err := f.srv.AzureIntegrationsEnabled(f.ctx, api.AzureIntegrationsEnabledRequestObject{})
	if on, _ := enabled.(api.AzureIntegrationsEnabled200JSONResponse); err != nil || !on.Body {
		t.Fatalf("enabled %+v, %v", enabled, err)
	}
	confirm, err := f.srv.AzureIntegrationConsentConfirm(f.ctx, api.AzureIntegrationConsentConfirmRequestObject{AzureIntegrationId: f.id})
	if c, _ := confirm.(api.AzureIntegrationConsentConfirm200JSONResponse); err != nil || !c.Body.AdminConsentProvided {
		t.Fatalf("confirm %+v, %v", confirm, err)
	}
	// Without cloud.azure, Azure integrations are disabled and Graph's
	// operations unsupported.
	f = newAzureFixture(nil, nil, nil)
	enabled, err = f.srv.AzureIntegrationsEnabled(f.ctx, api.AzureIntegrationsEnabledRequestObject{})
	if on, _ := enabled.(api.AzureIntegrationsEnabled200JSONResponse); err != nil || on.Body {
		t.Fatalf("enabled without cloud.azure %+v, %v", enabled, err)
	}
	for _, name := range []string{"AzureIntegrationCreate", "AzureIntegrationDelete"} {
		if err := azureHandlers(f)[name](); !is(err, problem.InstallationUnsupported) {
			t.Errorf("%s without cloud.azure = %v", name, err)
		}
	}
	// Graph failing is a 503.
	f = newAzureFixture(nil, graph{err: &core.GraphFailure{Status: 503, Code: "serviceNotAvailable", Err: errBroken}}, nil)
	for _, name := range []string{"AzureIntegrationCreate", "AzureIntegrationDelete"} {
		if err := azureHandlers(f)[name](); !is(err, problem.ServiceUnavailable) {
			t.Errorf("%s with Graph down = %v", name, err)
		}
	}
}

func TestUnitAzureFailures(t *testing.T) {
	f := newAzureFixture(&azureStore{err: errBroken}, graph{}, nil)
	for name, call := range azureHandlers(f) {
		if name == "AzureIntegrationsEnabled" {
			continue
		}
		if err := call(); err == nil {
			t.Errorf("%s over a broken store succeeded", name)
		}
	}
	f = newAzureFixture(nil, graph{}, func(_ *store, sp *spaces, _ *stacks) { sp.slugsErr = errBroken })
	for name, call := range azureHandlers(f) {
		switch name {
		case "AzureIntegrationsEnabled", "AzureIntegrationConsentConfirm", "AzureIntegrationConsentClear":
			continue
		}
		if err := call(); !errors.Is(err, errBroken) {
			t.Errorf("%s with the spaces down = %v", name, err)
		}
	}
	f = newAzureFixture(nil, graph{}, func(_ *store, _ *spaces, sk *stacks) { sk.err = errBroken })
	for _, name := range []string{"AzureIntegration", "AzureIntegrationAttachedStack"} {
		if err := azureHandlers(f)[name](); !errors.Is(err, errBroken) {
			t.Errorf("%s with the stacks down = %v", name, err)
		}
	}
	// A disabled account's switch failing fails the answer.
	st, sp, sk := world()
	sv := core.New(core.Deps{
		Store: st, Azure: &azureStore{}, Applications: graph{}, DB: db{}, Spaces: sp, Accounts: accounts{},
		Stacks: sk, Authz: authz{}, Roles: roles{},
		Enabled: func(context.Context, kernel.AccountID) (bool, error) { return false, errBroken },
	})
	ctx := kernel.WithPrincipal(context.Background(), kernel.Principal{AccountID: st.i.Account, Login: "admin"})
	if _, err := New(sv, sp, sk).AzureIntegrationsEnabled(ctx, api.AzureIntegrationsEnabledRequestObject{}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	// A taken name is a conflict naming the Azure integration.
	f = newAzureFixture(nil, graph{}, nil)
	_, err := f.srv.AzureIntegrationCreate(f.ctx, api.AzureIntegrationCreateRequestObject{
		Body: &api.AzureIntegrationCreateJSONRequestBody{
			Name: "prod", TenantID: "99999999-8888-7777-6666-555555555555", Labels: []string{},
		},
	})
	if !is(err, problem.AlreadyExists) {
		t.Fatalf("a taken name = %v", err)
	}
}

func TestUnitGraphDetail(t *testing.T) {
	for g, want := range map[*core.GraphFailure]string{
		{Err: errBroken}: "Microsoft Graph could not be reached; retry later.",
		{Status: 503, Code: "serviceNotAvailable", Err: errBroken}: "Microsoft Graph failed: 503 serviceNotAvailable; retry later.",
		{Status: 502, Err: errBroken}:                              "Microsoft Graph failed: 502; retry later.",
	} {
		if got := graphDetail(g); got != want {
			t.Errorf("%v: %q", g, got)
		}
	}
	if err := cloudError(fmt.Errorf("x: %w", core.ErrAzureUnsupported)); !is(err, problem.InstallationUnsupported) {
		t.Errorf("unsupported answers %v", err)
	}
	if err := cloudError(&core.NameTaken{Kind: "Azure integration", Name: "x"}); err == nil ||
		!is(err, problem.AlreadyExists) {
		t.Errorf("a taken name answers %v", err)
	}
}
