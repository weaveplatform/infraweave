package http

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/features"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// previewed is a test catalogue's features in preview: one with an image
// and one without; the compiled catalogue marks none.
func previewed() []features.Feature {
	return []features.Feature{
		{Name: "insights", Kind: features.Capability, Preview: &features.Preview{
			Name: "Insights", Description: "Stack insights.", DocsURL: "https://docs.example/insights",
			ImageURL: "https://docs.example/insights.png",
		}},
		{Name: "vendor-preview", Kind: features.Option, Preview: &features.Preview{
			Name: "Preview vendor", Description: "A vendor.", DocsURL: "https://docs.example/vendor",
		}},
	}
}

// TestAccFeaturePreviewRoutes checks the three routes on PostgreSQL with
// billing off: the read and both writes answer the same object, the
// account's write applies at once on this replica, canManage follows the
// principal's admin, personal appears only for a member, and the personal
// write refuses an API key that is not personal and a run with 403
// forbidden.
func TestAccFeaturePreviewRoutes(t *testing.T) {
	svc, _, acme := seeded(t)
	s := NewServer(svc.WithPreviews(previewed), nil)
	acct := kernel.NewAccountID()
	admin := kernel.WithPrincipal(t.Context(), kernel.Principal{
		Kind: kernel.PrincipalAPIKey, OrganizationID: acme, AccountID: acct, Admin: true,
	})
	member := kernel.WithPrincipal(t.Context(), kernel.Principal{
		Kind: kernel.PrincipalUser, OrganizationID: acme, AccountID: acct, MemberID: kernel.NewMemberID(),
	})
	run := kernel.WithPrincipal(t.Context(), kernel.Principal{
		Kind: kernel.PrincipalRun, OrganizationID: acme, AccountID: acct,
	})
	image := "https://docs.example/insights.png"
	want := func(enabled, canManage bool, personal *bool, active bool) api.FeaturePreview {
		body := api.FeaturePreview{
			Account: api.FeaturePreviewAccountSettings{Enabled: enabled, CanManage: canManage},
			Features: []api.FeaturePreviewFeature{
				{
					Key: "insights", Name: "Insights", Description: "Stack insights.",
					DocsUrl: "https://docs.example/insights", ImageUrl: &image, Active: active,
				},
				{
					Key: "vendor-preview", Name: "Preview vendor", Description: "A vendor.",
					DocsUrl: "https://docs.example/vendor", Active: active,
				},
			},
			Visible: true,
		}
		if personal != nil {
			body.Personal = &api.FeaturePreviewPersonalSettings{Enabled: *personal}
		}
		return body
	}
	on, off := true, false
	read := func(ctx context.Context) api.FeaturePreview {
		t.Helper()
		r, err := s.FeaturePreview(ctx, api.FeaturePreviewRequestObject{})
		got, ok := r.(api.FeaturePreview200JSONResponse)
		if err != nil || !ok {
			t.Fatalf("featurePreview = %+v, %v", r, err)
		}
		return got.Body
	}
	check := func(what string, got, want api.FeaturePreview) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %+v, want %+v", what, got, want)
		}
	}

	check("the admin's read", read(admin), want(false, true, nil, false))
	check("the member's read", read(member), want(false, false, &on, false))

	r, err := s.AccountSetFeaturePreviewsEnabled(admin, api.AccountSetFeaturePreviewsEnabledRequestObject{
		Body: &api.AccountSetFeaturePreviewsEnabledJSONRequestBody{Enabled: true},
	})
	if got, ok := r.(api.AccountSetFeaturePreviewsEnabled200JSONResponse); err != nil || !ok {
		t.Fatalf("accountSetFeaturePreviewsEnabled = %+v, %v", r, err)
	} else {
		check("the opt-in's answer", got.Body, want(true, true, nil, true))
	}
	check("the member's read after the opt-in", read(member), want(true, false, &on, true))

	p, err := s.FeaturePreviewSetPersonalEnabled(member, api.FeaturePreviewSetPersonalEnabledRequestObject{
		Body: &api.FeaturePreviewSetPersonalEnabledJSONRequestBody{Enabled: false},
	})
	if got, ok := p.(api.FeaturePreviewSetPersonalEnabled200JSONResponse); err != nil || !ok {
		t.Fatalf("featurePreviewSetPersonalEnabled = %+v, %v", p, err)
	} else {
		check("the opt-out's answer", got.Body, want(true, false, &off, false))
	}
	check("the member's read after the opt-out", read(member), want(true, false, &off, false))
	check("the admin's read after the opt-out", read(admin), want(true, true, nil, true))

	for name, ctx := range map[string]context.Context{"an API key": admin, "a run": run} {
		_, err := s.FeaturePreviewSetPersonalEnabled(ctx, api.FeaturePreviewSetPersonalEnabledRequestObject{
			Body: &api.FeaturePreviewSetPersonalEnabledJSONRequestBody{Enabled: false},
		})
		wantType(t, name+"'s personal write", err, problem.Forbidden)
	}
}

// TestAccFeaturePreviewRoutesRefused checks that each route answers 401
// without a principal and fails with the database.
func TestAccFeaturePreviewRoutesRefused(t *testing.T) {
	svc, _, acme := seeded(t)
	s := NewServer(svc, nil)
	p := kernel.Principal{
		Kind: kernel.PrincipalUser, OrganizationID: acme, AccountID: kernel.NewAccountID(),
		MemberID: kernel.NewMemberID(),
	}
	down, cancel := context.WithCancel(kernel.WithPrincipal(t.Context(), p))
	cancel()
	routes := map[string]func(context.Context) error{
		"featurePreview": func(ctx context.Context) error {
			_, err := s.FeaturePreview(ctx, api.FeaturePreviewRequestObject{})
			return err
		},
		"accountSetFeaturePreviewsEnabled": func(ctx context.Context) error {
			_, err := s.AccountSetFeaturePreviewsEnabled(ctx, api.AccountSetFeaturePreviewsEnabledRequestObject{
				Body: &api.AccountSetFeaturePreviewsEnabledJSONRequestBody{Enabled: true},
			})
			return err
		},
		"featurePreviewSetPersonalEnabled": func(ctx context.Context) error {
			_, err := s.FeaturePreviewSetPersonalEnabled(ctx, api.FeaturePreviewSetPersonalEnabledRequestObject{
				Body: &api.FeaturePreviewSetPersonalEnabledJSONRequestBody{Enabled: true},
			})
			return err
		},
	}
	for name, route := range routes {
		wantType(t, name+" without a principal", route(t.Context()), problem.Unauthenticated)
		err := route(down)
		if _, answered := errors.AsType[*problem.Error](err); err == nil || answered {
			t.Errorf("%s with the database unreachable = %v", name, err)
		}
	}
}
