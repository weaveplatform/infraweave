package http_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// azureDevOpsCalls are the Azure DevOps operations on f's Azure
// DevOps integration, each answering only its error.
func (f fixture) azureDevOpsCalls(ctx context.Context) map[string]func() error {
	s, id := f.srv, f.azure
	return map[string]func() error{
		"create": func() error {
			_, err := s.AzureDevOpsRepoIntegrationCreate(ctx,
				api.AzureDevOpsRepoIntegrationCreateRequestObject{
					Body: &api.AzureDevOpsRepoIntegrationCreateJSONRequestBody{},
				})
			return err
		},
		"read": func() error {
			_, err := s.AzureDevOpsRepoIntegration(
				ctx,
				api.AzureDevOpsRepoIntegrationRequestObject{
					AzureDevOpsRepoIntegrationId: id,
				},
			)
			return err
		},
		"update": func() error {
			_, err := s.AzureDevOpsRepoIntegrationUpdate(ctx,
				api.AzureDevOpsRepoIntegrationUpdateRequestObject{
					AzureDevOpsRepoIntegrationId: id,
					Body:                         &api.AzureDevOpsRepoIntegrationUpdateJSONRequestBody{},
				})
			return err
		},
		"delete": func() error {
			_, err := s.AzureDevOpsRepoIntegrationDelete(
				ctx,
				api.AzureDevOpsRepoIntegrationDeleteRequestObject{
					AzureDevOpsRepoIntegrationId: id,
				},
			)
			return err
		},
		"test": func() error {
			_, err := s.AzureDevOpsRepoIntegrationTest(
				ctx,
				api.AzureDevOpsRepoIntegrationTestRequestObject{
					AzureDevOpsRepoIntegrationId: id,
				},
			)
			return err
		},
	}
}

// TestUnitAzureDevOps checks that an admin reads an Azure DevOps
// integration with its organization, projects and webhook URL, and as a
// generic integration's details, and tests it, failed when a check fails;
// that no principal is unauthenticated; and that failing slugs and a
// failing store answer their error.
func TestUnitAzureDevOps(t *testing.T) {
	f := newFixture(t)
	got, err := f.srv.AzureDevOpsRepoIntegration(
		f.ctx,
		api.AzureDevOpsRepoIntegrationRequestObject{
			AzureDevOpsRepoIntegrationId: f.azure,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(api.AzureDevOpsRepoIntegration200JSONResponse).Body; v.WebhookUrl == "" ||
		v.OrganizationURL != "https://dev.azure.example/acme" ||
		len(v.AccessibleProjects) != 1 || v.Name != "Azure" {
		t.Errorf("read = %+v", v)
	}
	generic, err := f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
		VcsIntegrationId: f.azure,
		Params:           api.VcsIntegrationParams{Provider: api.VCSProviderAZUREDEVOPS},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := generic.(api.VcsIntegration200JSONResponse).Body.Details; d == nil {
		t.Error("a generic read has no details")
	} else if v, err := d.AsAzureDevOpsRepoIntegration(); err != nil || v.OrganizationURL != "https://dev.azure.example/acme" {
		t.Errorf("details = %+v, %v", v, err)
	}
	res, err := f.srv.AzureDevOpsRepoIntegrationTest(f.ctx,
		api.AzureDevOpsRepoIntegrationTestRequestObject{AzureDevOpsRepoIntegrationId: f.azure})
	if err != nil || bool(res.(api.AzureDevOpsRepoIntegrationTest200JSONResponse).Body) {
		t.Errorf("test with a failed check = %+v, %v", res, err)
	}
	for name, call := range f.azureDevOpsCalls(t.Context()) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
	f.spaces.fail = errBoom
	if err := f.azureDevOpsCalls(f.ctx)["read"](); !errors.Is(err, errBoom) {
		t.Errorf("read with failing slugs = %v", err)
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for _, name := range []string{"read", "update", "test"} {
		if err := f.azureDevOpsCalls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
