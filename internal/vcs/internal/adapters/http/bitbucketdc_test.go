package http_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// bitbucketDCCalls are the Bitbucket Data Center operations on f's Bitbucket
// Data Center integration, each answering only its error.
func (f fixture) bitbucketDCCalls(ctx context.Context) map[string]func() error {
	s, id := f.srv, f.dc
	return map[string]func() error{
		"create": func() error {
			_, err := s.BitbucketDatacenterIntegrationCreate(ctx,
				api.BitbucketDatacenterIntegrationCreateRequestObject{
					Body: &api.BitbucketDatacenterIntegrationCreateJSONRequestBody{},
				})
			return err
		},
		"read": func() error {
			_, err := s.BitbucketDatacenterIntegration(
				ctx,
				api.BitbucketDatacenterIntegrationRequestObject{
					BitbucketDatacenterIntegrationId: id,
				},
			)
			return err
		},
		"update": func() error {
			_, err := s.BitbucketDatacenterIntegrationUpdate(ctx,
				api.BitbucketDatacenterIntegrationUpdateRequestObject{
					BitbucketDatacenterIntegrationId: id,
					Body:                             &api.BitbucketDatacenterIntegrationUpdateJSONRequestBody{},
				})
			return err
		},
		"delete": func() error {
			_, err := s.BitbucketDatacenterIntegrationDelete(
				ctx,
				api.BitbucketDatacenterIntegrationDeleteRequestObject{
					BitbucketDatacenterIntegrationId: id,
				},
			)
			return err
		},
		"test": func() error {
			_, err := s.BitbucketDatacenterIntegrationTest(
				ctx,
				api.BitbucketDatacenterIntegrationTestRequestObject{
					BitbucketDatacenterIntegrationId: id,
				},
			)
			return err
		},
	}
}

// TestUnitBitbucketDC checks that an admin reads a Bitbucket Data Center
// integration with its username and webhook URL, and as a
// generic integration's details, and tests it, failed when a check fails;
// that no principal is unauthenticated; and that failing slugs and a
// failing store answer their error.
func TestUnitBitbucketDC(t *testing.T) {
	f := newFixture(t)
	got, err := f.srv.BitbucketDatacenterIntegration(
		f.ctx,
		api.BitbucketDatacenterIntegrationRequestObject{
			BitbucketDatacenterIntegrationId: f.dc,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(api.BitbucketDatacenterIntegration200JSONResponse).Body; v.WebhookURL == "" ||
		v.Username != "infraweave" || v.Name != "DC" {
		t.Errorf("read = %+v", v)
	}
	generic, err := f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
		VcsIntegrationId: f.dc,
		Params:           api.VcsIntegrationParams{Provider: api.VCSProviderBITBUCKETDATACENTER},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := generic.(api.VcsIntegration200JSONResponse).Body.Details; d == nil {
		t.Error("a generic read has no details")
	} else if v, err := d.AsBitbucketDatacenterIntegration(); err != nil || v.Username != "infraweave" {
		t.Errorf("details = %+v, %v", v, err)
	}
	res, err := f.srv.BitbucketDatacenterIntegrationTest(f.ctx,
		api.BitbucketDatacenterIntegrationTestRequestObject{BitbucketDatacenterIntegrationId: f.dc})
	if err != nil || bool(res.(api.BitbucketDatacenterIntegrationTest200JSONResponse).Body) {
		t.Errorf("test with a failed check = %+v, %v", res, err)
	}
	for name, call := range f.bitbucketDCCalls(t.Context()) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
	f.spaces.fail = errBoom
	if err := f.bitbucketDCCalls(f.ctx)["read"](); !errors.Is(err, errBoom) {
		t.Errorf("read with failing slugs = %v", err)
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for _, name := range []string{"read", "update", "test"} {
		if err := f.bitbucketDCCalls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
