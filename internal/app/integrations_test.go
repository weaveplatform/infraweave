package app

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// TestUnitIntegrationsCount composes the domains' counters, each setting
// its own kinds, and fails with the first that fails.
func TestUnitIntegrationsCount(t *testing.T) {
	if len(counters(Domains{})) != 0 {
		t.Fatal("a test's nil domains count")
	}
	acct := kernel.NewAccountID()
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{AccountID: acct})
	cloud := func(_ context.Context, got kernel.AccountID, into *api.IntegrationsCount) error {
		if got != acct {
			t.Errorf("account %v", got)
		}
		into.Aws, into.Azure = 1, 2
		return nil
	}
	vcs := func(_ context.Context, _ kernel.AccountID, into *api.IntegrationsCount) error {
		into.Github, into.AzureDevOps = 3, 4
		return nil
	}
	res, err := apiServer{counters: []Counter{cloud, vcs}}.IntegrationsCount(ctx, api.IntegrationsCountRequestObject{})
	got, _ := res.(api.IntegrationsCount200JSONResponse)
	if err != nil || got.Body != (api.IntegrationsCount{Aws: 1, Azure: 2, Github: 3, AzureDevOps: 4}) {
		t.Fatalf("count %+v, %v", got.Body, err)
	}
	broken := errors.New("broken")
	failing := func(context.Context, kernel.AccountID, *api.IntegrationsCount) error { return broken }
	if _, err := (apiServer{counters: []Counter{cloud, failing}}).IntegrationsCount(ctx, api.IntegrationsCountRequestObject{}); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
	_, err = apiServer{}.IntegrationsCount(t.Context(), api.IntegrationsCountRequestObject{})
	if e, ok := errors.AsType[*problem.Error](err); !ok || e.Problem.Status != problem.Unauthenticated.Status {
		t.Fatalf("without a principal: %v", err)
	}
}
