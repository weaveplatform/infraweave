package http_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// gitlabCalls are the GitLab operations on f's GitLab integration, each
// answering only its error.
func (f fixture) gitlabCalls(ctx context.Context) map[string]func() error {
	s, id := f.srv, f.gitlab
	return map[string]func() error{
		"create": func() error {
			_, err := s.GitlabIntegrationCreate(ctx, api.GitlabIntegrationCreateRequestObject{
				Body: &api.GitlabIntegrationCreateJSONRequestBody{},
			})
			return err
		},
		"read": func() error {
			_, err := s.GitlabIntegration(
				ctx,
				api.GitlabIntegrationRequestObject{GitlabIntegrationId: id},
			)
			return err
		},
		"update": func() error {
			_, err := s.GitlabIntegrationUpdate(ctx, api.GitlabIntegrationUpdateRequestObject{
				GitlabIntegrationId: id, Body: &api.GitlabIntegrationUpdateJSONRequestBody{},
			})
			return err
		},
		"delete": func() error {
			_, err := s.GitlabIntegrationDelete(ctx,
				api.GitlabIntegrationDeleteRequestObject{GitlabIntegrationId: id})
			return err
		},
		"test": func() error {
			_, err := s.GitlabIntegrationTest(ctx,
				api.GitlabIntegrationTestRequestObject{GitlabIntegrationId: id})
			return err
		},
	}
}

// TestUnitGitLab checks that an admin reads a GitLab integration with its
// webhook URL and tests it, failed when a check fails; that no principal
// is unauthenticated; and that failing slugs and a failing store answer
// their error.
func TestUnitGitLab(t *testing.T) {
	f := newFixture(t)
	got, err := f.srv.GitlabIntegration(f.ctx, api.GitlabIntegrationRequestObject{
		GitlabIntegrationId: f.gitlab,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(api.GitlabIntegration200JSONResponse).Body; v.WebhookUrl == "" ||
		v.Name != "Main" {
		t.Errorf("read = %+v", v)
	}
	res, err := f.srv.GitlabIntegrationTest(f.ctx, api.GitlabIntegrationTestRequestObject{
		GitlabIntegrationId: f.gitlab,
	})
	if err != nil || bool(res.(api.GitlabIntegrationTest200JSONResponse).Body) {
		t.Errorf("test with a failed check = %+v, %v", res, err)
	}
	for name, call := range f.gitlabCalls(t.Context()) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
	f.spaces.fail = errBoom
	if err := f.gitlabCalls(f.ctx)["read"](); !errors.Is(err, errBoom) {
		t.Errorf("read with failing slugs = %v", err)
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for _, name := range []string{"read", "update", "test"} {
		if err := f.gitlabCalls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
