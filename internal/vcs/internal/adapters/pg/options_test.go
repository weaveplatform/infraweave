package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

type optionPolicy struct {
	err   error
	calls int
}

func (p *optionPolicy) AllowOrganization(context.Context, kernel.OrganizationID, string) error {
	p.calls++
	return p.err
}

func TestAccIntegrationOption(t *testing.T) {
	f := newFixture(t)
	policy := &optionPolicy{}
	f.deps.Features = policy
	svc := integration.New(f.deps)
	ctx := t.Context()
	in, err := svc.Create(ctx, f.admin, gitlab("Existing", "team", false))
	if err != nil || policy.calls != 1 {
		t.Fatalf("entitled create: %+v %v, calls=%d", in, err, policy.calls)
	}
	for _, refused := range []*featuresapi.Refusal{
		{Feature: "multiple-vcs-integrations", Tiers: []string{"Enterprise"}},
		{Feature: "multiple-vcs-integrations", Disabled: true},
	} {
		policy.err = refused
		if _, err := svc.Create(ctx, f.admin, gitlab("Refused", "team", false)); !errors.Is(err, refused) {
			t.Fatalf("refused create: %v", err)
		}
		var count int
		if err := f.db.QueryRow(ctx, `SELECT count(*) FROM vcs_integrations WHERE account_id=$1`, f.acct).Scan(&count); err != nil || count != 1 {
			t.Fatalf("refused create stored a row: count=%d %v", count, err)
		}
	}
	before := policy.calls
	def, err := svc.Create(ctx, f.admin, gitlab("Default", "", true))
	if err != nil || !def.IsDefault || policy.calls != before {
		t.Fatalf("free default: %+v %v, calls=%d", def, err, policy.calls)
	}
	if _, _, err := svc.Read(ctx, f.admin, in.ID.String(), integration.GitLab); err != nil {
		t.Fatal(err)
	}
	update := gitlab("", "team", false)
	update.Description = "Still usable after a tier drop"
	in, err = svc.Update(ctx, f.admin, in.ID.String(), kernel.Expect{}, update)
	if err != nil || in.Description != update.Description {
		t.Fatalf("update after tier drop: %+v %v", in, err)
	}
	checks, err := svc.Test(ctx, f.admin, in.ID.String(), integration.GitLab)
	if err != nil || len(checks) != 1 || !checks[0].Passed {
		t.Fatalf("test after tier drop: %+v %v", checks, err)
	}
	use, ok, err := svc.Use(ctx, f.acct, integration.GitLab, &in.ID)
	if err != nil || !ok || string(use.Credential) != "token" {
		t.Fatalf("source use after tier drop: %+v %t %v", use, ok, err)
	}
	if _, err := svc.Delete(ctx, f.admin, in.ID.String(), integration.GitLab); err != nil {
		t.Fatal(err)
	}
	if policy.calls != before {
		t.Fatalf("existing integration consulted create policy: %d -> %d", before, policy.calls)
	}
}

func TestAccManifestOption(t *testing.T) {
	f := newFixture(t)
	refused := &featuresapi.Refusal{Feature: "multiple-vcs-integrations", Tiers: []string{"Enterprise"}}
	policy := &optionPolicy{err: refused}
	f.deps.Features = policy
	svc := integration.New(f.deps)
	ctx := t.Context()
	complete := func(host string) (integration.Integration, error) {
		form, err := svc.OpenManifest(ctx, f.admin, host, host, "acme")
		if err != nil {
			return integration.Integration{}, err
		}
		state := form.Endpoint[strings.LastIndex(form.Endpoint, "=")+1:]
		return svc.FromManifest(ctx, f.admin, "good", state)
	}
	first, err := complete("https://first.example")
	if err != nil || !first.IsDefault || policy.calls != 0 {
		t.Fatalf("first manifest: %+v %v, calls=%d", first, err, policy.calls)
	}
	if _, err := complete("https://denied.example"); !errors.Is(err, refused) || policy.calls != 1 {
		t.Fatalf("second manifest: %v, calls=%d", err, policy.calls)
	}
	var count int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM vcs_integrations WHERE account_id=$1`, f.acct).Scan(&count); err != nil || count != 1 {
		t.Fatalf("refused manifest stored a row: count=%d %v", count, err)
	}
	policy.err = nil
	second, err := complete("https://allowed.example")
	if err != nil || second.IsDefault || second.ID == first.ID || policy.calls != 2 {
		t.Fatalf("entitled second manifest: %+v %v, calls=%d", second, err, policy.calls)
	}
}
