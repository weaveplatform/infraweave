package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type optionCheck struct {
	err   error
	org   kernel.OrganizationID
	acct  kernel.AccountID
	name  string
	calls int
}

func (c *optionCheck) AllowAccount(_ context.Context, org kernel.OrganizationID, acct kernel.AccountID, name string) error {
	c.calls++
	c.org, c.acct, c.name = org, acct, name
	return c.err
}

type unopenedDB struct{ calls int }

var errBegin = errors.New("transaction boundary")

func (d *unopenedDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	d.calls++
	return nil, errBegin
}

// Every provider reaches the same check before a transaction or a secret
// write. A default does not need the option even when it is disabled.
func TestUnitCreateOption(t *testing.T) {
	p := kernel.Principal{AccountID: kernel.NewAccountID(), OrganizationID: kernel.NewOrganizationID()}
	for _, provider := range []string{
		integration.GitHubEnterprise, integration.GitLab, integration.BitbucketCloud,
		integration.BitbucketDC, integration.AzureDevOps,
	} {
		for _, isDefault := range []bool{false, true} {
			t.Run(provider+"/"+map[bool]string{false: "non-default", true: "default"}[isDefault], func(t *testing.T) {
				refused := &featuresapi.Refusal{Feature: "multiple-vcs-integrations", Disabled: true}
				check, db := &optionCheck{err: refused}, &unopenedDB{}
				svc := integration.New(integration.Deps{DB: db, Features: check, Providers: []string{provider}})
				_, err := svc.Create(t.Context(), p, integration.Input{
					Provider: provider, Name: "Main", IsDefault: isDefault,
					Settings: port.Settings{Email: "dev@acme.example", OrganizationURL: "https://dev.azure.com/acme"},
				})
				if isDefault {
					if !errors.Is(err, errBegin) || check.calls != 0 || db.calls != 1 {
						t.Fatalf("default: %v, check=%+v, transactions=%d", err, check, db.calls)
					}
				} else if !errors.Is(err, refused) || check.calls != 1 || check.org != p.OrganizationID ||
					check.acct != p.AccountID || check.name != "multiple-vcs-integrations" || db.calls != 0 {
					t.Fatalf("non-default: %v, check=%+v, transactions=%d", err, check, db.calls)
				}
			})
		}
	}
	for _, cause := range []error{nil, errors.New("entitlement unavailable")} {
		check, db := &optionCheck{err: cause}, &unopenedDB{}
		svc := integration.New(integration.Deps{DB: db, Features: check, Providers: []string{integration.GitLab}})
		_, err := svc.Create(t.Context(), p, integration.Input{Provider: integration.GitLab, Name: "Main"})
		want, begins := cause, 0
		if cause == nil {
			want, begins = errBegin, 1
		}
		if !errors.Is(err, want) || db.calls != begins || check.calls != 1 {
			t.Fatalf("entitlement result: %v, check=%+v, transactions=%d", err, check, db.calls)
		}
	}
	db := &unopenedDB{}
	svc := integration.New(integration.Deps{DB: db, Providers: []string{integration.GitLab}})
	if _, err := svc.Create(t.Context(), p, integration.Input{Provider: integration.GitLab, Name: "Main"}); !errors.Is(err, kernel.ErrUnavailable) || db.calls != 0 {
		t.Fatalf("missing policy admitted create: %v, transactions=%d", err, db.calls)
	}
}

type manifestDB struct{ calls int }

type manifestTx struct{ pgx.Tx }

func (manifestTx) Commit(context.Context) error   { return nil }
func (manifestTx) Rollback(context.Context) error { return nil }

func (d *manifestDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	d.calls++
	if d.calls == 1 {
		return manifestTx{}, nil
	}
	return nil, errBegin
}

type manifestStore struct {
	integration.Store
	existing bool
}

func (manifestStore) TakeManifest(_ context.Context, _ kernel.AccountID, id kernel.ULID, _ time.Time) (integration.Manifest, error) {
	return integration.Manifest{ID: id}, nil
}

func (s manifestStore) Default(context.Context, kernel.AccountID, string) (integration.Integration, error) {
	if s.existing {
		return integration.Integration{}, nil
	}
	return integration.Integration{}, integration.ErrNotFound
}

type manifestApps struct{ port.Apps }

func (manifestApps) Convert(context.Context, string, string) (port.Created, error) {
	return port.Created{Name: "App"}, nil
}

func TestUnitManifestOption(t *testing.T) {
	for _, existing := range []bool{false, true} {
		refused := &featuresapi.Refusal{Feature: "multiple-vcs-integrations"}
		check, db := &optionCheck{err: refused}, &manifestDB{}
		svc := integration.New(integration.Deps{
			Store: manifestStore{existing: existing}, DB: db, Apps: manifestApps{}, Features: check,
			Providers: []string{integration.GitHubEnterprise},
		})
		_, err := svc.FromManifest(t.Context(), kernel.Principal{}, "code", kernel.NewULID().String())
		if existing {
			if !errors.Is(err, refused) || check.calls != 1 || db.calls != 1 {
				t.Fatalf("second manifest: %v, calls=%d, transactions=%d", err, check.calls, db.calls)
			}
		} else if !errors.Is(err, errBegin) || check.calls != 0 || db.calls != 2 {
			t.Fatalf("first manifest: %v, calls=%d, transactions=%d", err, check.calls, db.calls)
		}
	}
}
