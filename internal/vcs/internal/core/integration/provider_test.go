package integration_test

import (
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

type providerStore struct {
	integration.Store
	rows        []integration.Integration
	fail        error
	secretReads int
}

func (s *providerStore) Default(_ context.Context, acct kernel.AccountID, provider string) (integration.Integration, error) {
	if s.fail != nil {
		return integration.Integration{}, s.fail
	}
	for _, in := range s.rows {
		if in.Account == acct && in.Provider == provider && in.IsDefault {
			return in, nil
		}
	}
	return integration.Integration{}, integration.ErrNotFound
}

func (s *providerStore) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID, _ bool) (integration.Integration, error) {
	if s.fail != nil {
		return integration.Integration{}, s.fail
	}
	for _, in := range s.rows {
		if in.Account == acct && in.ID == id {
			return in, nil
		}
	}
	return integration.Integration{}, integration.ErrNotFound
}

func (s *providerStore) Secrets(context.Context, kernel.AccountID, kernel.ULID) (integration.Secrets, error) {
	s.secretReads++
	return integration.Secrets{Credential: []byte("account-key")}, nil
}

type providerSpaces struct {
	accessapi.Spaces
	nodes []accessapi.SpaceNode
}

func (s providerSpaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return s.nodes, nil
}

func TestUnitGitHubProviderAlias(t *testing.T) {
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	app, named, wrong := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	store := &providerStore{rows: []integration.Integration{
		{Account: acct, ID: app, Provider: integration.GitHubEnterprise, Space: acct.ULID, IsDefault: true},
		{Account: acct, ID: named, Provider: integration.GitHubEnterprise, Space: acct.ULID},
		{Account: acct, ID: wrong, Provider: integration.GitLab, Space: acct.ULID},
	}}
	svc := integration.New(integration.Deps{
		Store: store, Providers: []string{integration.GitHubEnterprise, integration.GitLab},
		Spaces: providerSpaces{nodes: []accessapi.SpaceNode{{ID: acct.ULID}}},
	})
	for _, id := range []*kernel.ULID{nil, &named} {
		use, ok, err := svc.Use(t.Context(), acct, integration.GitHub, id)
		if err != nil || !ok || use.Provider != integration.GitHubEnterprise || string(use.Credential) != "account-key" {
			t.Fatalf("account alias resolution: %+v %t %v", use, ok, err)
		}
		want := app
		if id != nil {
			want = *id
		}
		if use.ID != want {
			t.Fatalf("alias selected %s, want %s", use.ID, want)
		}
	}
	store.secretReads = 0
	if _, ok, err := svc.Use(t.Context(), acct, integration.GitHub, &wrong); ok || !errors.Is(err, integration.ErrNotFound) || store.secretReads != 0 {
		t.Fatalf("another provider's credential reached: ok=%t err=%v reads=%d", ok, err, store.secretReads)
	}
	if _, ok, err := svc.Use(t.Context(), other, integration.GitHub, &named); err != nil || ok || store.secretReads != 0 {
		t.Fatalf("another account's App reached: ok=%t err=%v reads=%d", ok, err, store.secretReads)
	}
	if _, ok, err := svc.Use(t.Context(), other, integration.GitHub, nil); ok {
		t.Fatal("an account with no default resolved an App")
	} else if inv, valid := errors.AsType[*kernel.Invalid](err); !valid || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/provider" {
		t.Fatalf("missing default has no source-resolution diagnostic: %v", err)
	}
	for _, tc := range []struct {
		id   kernel.ULID
		want error
	}{{app, nil}, {named, nil}, {wrong, stacksapi.ErrNoIntegration}} {
		if err := svc.Bindable(t.Context(), acct, acct.ULID, integration.GitHub, tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("bind alias %s: %v, want %v", tc.id, err, tc.want)
		}
	}
	if store.secretReads != 0 {
		t.Fatal("binding opened credentials")
	}
}

func TestUnitDefaultAvailability(t *testing.T) {
	acct := kernel.NewAccountID()
	app := integration.Integration{
		Account: acct, ID: kernel.NewULID(), Provider: integration.GitHubEnterprise, IsDefault: true,
	}
	store := &providerStore{rows: []integration.Integration{app}}
	svc := integration.New(integration.Deps{Store: store})
	if ok, err := svc.DefaultAvailable(t.Context(), acct, integration.GitHub); err != nil || !ok {
		t.Fatalf("default alias unavailable: %t %v", ok, err)
	}
	if ok, err := svc.DefaultAvailable(t.Context(), kernel.NewAccountID(), integration.GitHub); err != nil || ok {
		t.Fatalf("foreign default available: %t %v", ok, err)
	}
	store.fail = errors.New("lookup failed")
	if _, err := svc.DefaultAvailable(t.Context(), acct, integration.GitHub); !errors.Is(err, store.fail) {
		t.Fatalf("lookup failure lost: %v", err)
	}
	if store.secretReads != 0 {
		t.Fatal("availability opened credentials")
	}
}
