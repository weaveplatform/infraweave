package http_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// bitbucketCloudCalls are the Bitbucket Cloud operations on f's Bitbucket
// Cloud integration, each answering only its error.
func (f fixture) bitbucketCloudCalls(ctx context.Context) map[string]func() error {
	s, id := f.srv, f.cloud
	return map[string]func() error{
		"create": func() error {
			_, err := s.BitbucketCloudIntegrationCreateV2(ctx,
				api.BitbucketCloudIntegrationCreateV2RequestObject{
					Body: &api.BitbucketCloudIntegrationCreateV2JSONRequestBody{},
				})
			return err
		},
		"read": func() error {
			_, err := s.BitbucketCloudIntegration(ctx,
				api.BitbucketCloudIntegrationRequestObject{BitbucketCloudIntegrationId: id})
			return err
		},
		"update": func() error {
			_, err := s.BitbucketCloudIntegrationUpdateV2(ctx,
				api.BitbucketCloudIntegrationUpdateV2RequestObject{
					BitbucketCloudIntegrationId: id,
					Body:                        &api.BitbucketCloudIntegrationUpdateV2JSONRequestBody{},
				})
			return err
		},
		"delete": func() error {
			_, err := s.BitbucketCloudIntegrationDelete(ctx,
				api.BitbucketCloudIntegrationDeleteRequestObject{BitbucketCloudIntegrationId: id})
			return err
		},
		"test": func() error {
			_, err := s.BitbucketCloudIntegrationTest(ctx,
				api.BitbucketCloudIntegrationTestRequestObject{BitbucketCloudIntegrationId: id})
			return err
		},
	}
}

// TestUnitBitbucketCloud checks that an admin reads a Bitbucket Cloud
// integration with its email, git checkout and webhook URL, and as a
// generic integration's details, and tests it, failed when a check fails;
// that no principal is unauthenticated; and that failing slugs and a
// failing store answer their error.
func TestUnitBitbucketCloud(t *testing.T) {
	f := newFixture(t)
	got, err := f.srv.BitbucketCloudIntegration(f.ctx, api.BitbucketCloudIntegrationRequestObject{
		BitbucketCloudIntegrationId: f.cloud,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(api.BitbucketCloudIntegration200JSONResponse).Body; v.WebhookUrl == "" ||
		v.Email != "dev@acme.example" || !v.UseGitCheckout || v.Name != "Cloud" {
		t.Errorf("read = %+v", v)
	}
	generic, err := f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
		VcsIntegrationId: f.cloud,
		Params:           api.VcsIntegrationParams{Provider: api.VCSProviderBITBUCKETCLOUD},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := generic.(api.VcsIntegration200JSONResponse).Body.Details; d == nil {
		t.Error("a generic read has no details")
	} else if v, err := d.AsBitbucketCloudIntegration(); err != nil || v.Email != "dev@acme.example" {
		t.Errorf("details = %+v, %v", v, err)
	}
	res, err := f.srv.BitbucketCloudIntegrationTest(f.ctx,
		api.BitbucketCloudIntegrationTestRequestObject{BitbucketCloudIntegrationId: f.cloud})
	if err != nil || bool(res.(api.BitbucketCloudIntegrationTest200JSONResponse).Body) {
		t.Errorf("test with a failed check = %+v, %v", res, err)
	}
	for name, call := range f.bitbucketCloudCalls(t.Context()) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
	f.spaces.fail = errBoom
	if err := f.bitbucketCloudCalls(f.ctx)["read"](); !errors.Is(err, errBoom) {
		t.Errorf("read with failing slugs = %v", err)
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for _, name := range []string{"read", "update", "test"} {
		if err := f.bitbucketCloudCalls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
