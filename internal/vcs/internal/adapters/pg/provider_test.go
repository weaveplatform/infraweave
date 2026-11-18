package pg_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestAccGitHubAccountDefault(t *testing.T) {
	f := newFixture(t)
	create := func(name, app string) integration.Integration {
		t.Helper()
		in, err := f.svc.Create(t.Context(), f.admin, integration.Input{
			Provider: integration.GitHubEnterprise, Name: name, Space: "acme", IsDefault: true,
			Settings: port.Settings{APIHost: "https://api.github.com", AppID: app},
			Secrets:  &integration.Secrets{Credential: appKey(), WebhookSecret: []byte("hook")},
		})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	first := create("First App", "42")
	if ok, err := f.svc.DefaultAvailable(t.Context(), f.acct, integration.GitHub); err != nil || !ok {
		t.Fatalf("persisted default unavailable: %t %v", ok, err)
	}
	for _, id := range []*kernel.ULID{nil, &first.ID} {
		use, ok, err := f.svc.Use(t.Context(), f.acct, integration.GitHub, id)
		if err != nil || !ok || use.ID != first.ID || use.Provider != integration.GitHubEnterprise || !bytes.Equal(use.Credential, appKey()) {
			t.Fatalf("account App was not opened: id=%s ok=%t err=%v", use.ID, ok, err)
		}
	}
	if _, ok, err := f.svc.Use(t.Context(), kernel.NewAccountID(), integration.GitHub, &first.ID); err != nil || ok {
		t.Fatalf("another account reached the stored App: %t %v", ok, err)
	}
	other, err := f.svc.Create(t.Context(), f.admin, gitlab("GitLab", "team", false))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.svc.Use(t.Context(), f.acct, integration.GitHub, &other.ID); ok || !errors.Is(err, integration.ErrNotFound) {
		t.Fatalf("alias selected another provider: %t %v", ok, err)
	}
	if _, err := f.svc.Delete(t.Context(), f.admin, first.ID.String(), integration.GitHubEnterprise); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.svc.Use(t.Context(), f.acct, integration.GitHub, nil); ok {
		t.Fatal("deleted default still resolves")
	} else if inv, valid := errors.AsType[*kernel.Invalid](err); !valid || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/provider" {
		t.Fatalf("missing-default diagnostic lost: %v", err)
	}
	replacement := create("Replacement App", "43")
	if use, ok, err := f.svc.Use(t.Context(), f.acct, integration.GitHub, nil); err != nil || !ok || use.ID != replacement.ID {
		t.Fatalf("fresh default was not resolved: %s %t %v", use.ID, ok, err)
	}
}
