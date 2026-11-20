package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	policyhttp "github.com/weaveplatform/infraweave/internal/policies/internal/http"
)

func handlers(ctx context.Context, s policyhttp.Server, id string) map[string]func() error {
	create := api.PolicyCreateInput{Name: "New", Type: api.PolicyTypePLAN, Body: "package infraweave\n"}
	update := api.PolicyUpdateInput{Name: "Changed", Body: create.Body}
	return map[string]func() error{
		"create": func() error {
			_, err := s.PolicyCreatev2(ctx, api.PolicyCreatev2RequestObject{Body: &create})
			return err
		},
		"get": func() error { _, err := s.Policy(ctx, api.PolicyRequestObject{PolicyId: id}); return err },
		"update": func() error {
			_, err := s.PolicyUpdatev2(ctx, api.PolicyUpdatev2RequestObject{PolicyId: id, Body: &update})
			return err
		},
		"patch": func() error {
			_, err := s.PolicyUpdatev2Patch(ctx, api.PolicyUpdatev2PatchRequestObject{PolicyId: id, Body: &api.MergePatch{"name": "Patched"}})
			return err
		},
		"delete": func() error { _, err := s.PolicyDelete(ctx, api.PolicyDeleteRequestObject{PolicyId: id}); return err },
		"list":   func() error { _, err := s.Policies(ctx, api.PoliciesRequestObject{}); return err },
		"search": func() error {
			_, err := s.SearchPolicies(ctx, api.SearchPoliciesRequestObject{Body: &api.SearchInput{}})
			return err
		},
		"suggest": func() error {
			_, err := s.SearchPoliciesSuggestions(ctx, api.SearchPoliciesSuggestionsRequestObject{Body: &api.SearchSuggestionsInput{}})
			return err
		},
		"space-search": func() error {
			_, err := s.SearchSpacePolicies(ctx, api.SearchSpacePoliciesRequestObject{Body: &api.SearchSpacePoliciesRequest{Space: "team"}})
			return err
		},
		"space-suggest": func() error {
			_, err := s.SearchSpacePoliciesSuggestions(ctx, api.SearchSpacePoliciesSuggestionsRequestObject{Body: &api.SearchSpacePoliciesSuggestionsRequest{Space: "team"}})
			return err
		},
	}
}

func TestAccPolicyHandlersRequirePrincipal(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Guard")
	for name, call := range handlers(t.Context(), f.http, p.ID.String()) {
		var public *problem.Error
		if err := call(); !errors.As(err, &public) || public.Problem.Status != 401 {
			t.Errorf("%s without principal: %v", name, err)
		}
	}
}

func TestAccPolicyHandlersMapConcealmentAndWriteDenial(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Guard")
	ctx := kernel.WithPrincipal(t.Context(), f.principal)
	for _, id := range []string{"invalid", kernel.NewULID().String()} {
		all := handlers(ctx, f.http, id)
		for _, name := range []string{"get", "update", "patch", "delete"} {
			var public *problem.Error
			if err := all[name](); !errors.As(err, &public) || public.Problem.Status != 404 {
				t.Errorf("%s disclosed missing policy: %v", name, err)
			}
		}
	}
	f.authz.denied = true
	all := handlers(ctx, f.http, p.ID.String())
	for _, name := range []string{"create", "update", "patch", "delete"} {
		var public *problem.Error
		if err := all[name](); !errors.As(err, &public) || public.Problem.Status != 403 || public.Problem.Action != "SPACE_ADMIN" {
			t.Errorf("%s dropped write denial: %v", name, err)
		}
	}
	if f.count(t, "policies") != 1 || f.count(t, "events") != 1 {
		t.Fatal("denied handler persisted changes")
	}
}

func TestAccPolicyViewsRefuseSpaceNamingFailures(t *testing.T) {
	f := newFixture(t)
	ctx := kernel.WithPrincipal(t.Context(), f.principal)
	down := errors.New("space naming unavailable")
	for _, name := range []string{"get", "create", "update", "patch", "delete", "list"} {
		p := f.create(t, "Guard "+name)
		f.spaces.slugsErr = down
		err := handlers(ctx, f.http, p.ID.String())[name]()
		if !errors.Is(err, down) {
			t.Errorf("%s returned an unnamed/empty policy view: %v", name, err)
		}
		f.spaces.slugsErr = nil
	}
}
