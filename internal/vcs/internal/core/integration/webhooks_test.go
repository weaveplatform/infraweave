package integration_test

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type (
	rotationDB struct{ fail error }
	rotationTx struct{ pgx.Tx }
)

func (rotationTx) Commit(context.Context) error   { return nil }
func (rotationTx) Rollback(context.Context) error { return nil }

func (d rotationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return rotationTx{}, d.fail
}

type rotationStore struct {
	integration.Store
	row                            integration.Integration
	current, previous              []byte
	until                          time.Time
	audits                         []kernel.AuditEntry
	failGet, failRotate, failAudit error
}

func (s *rotationStore) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (integration.Integration, error) {
	if !lock {
		panic("rotation must lock its integration")
	}
	if s.failGet != nil {
		return integration.Integration{}, s.failGet
	}
	if acct != s.row.Account || id != s.row.ID {
		return integration.Integration{}, integration.ErrNotFound
	}
	return s.row, nil
}

func (s *rotationStore) RotateWebhookSecret(_ context.Context, in integration.Integration, secret []byte, until time.Time) error {
	if s.failRotate != nil {
		return s.failRotate
	}
	s.previous, s.current = slices.Clone(s.current), slices.Clone(secret)
	s.row, s.until = in, until
	return nil
}

func (s *rotationStore) Audit(_ context.Context, entry kernel.AuditEntry) error {
	if s.failAudit != nil {
		return s.failAudit
	}
	s.audits = append(s.audits, entry)
	return nil
}

func TestUnitWebhookRotationClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := kernel.Principal{AccountID: kernel.NewAccountID(), Login: "admin", ActorID: kernel.NewULID()}
		id := kernel.NewULID()
		store := &rotationStore{
			row:     integration.Integration{Account: p.AccountID, ID: id, Provider: integration.GitLab, Version: 1},
			current: []byte("old-secret"),
		}
		svc := integration.New(integration.Deps{Store: store, DB: rotationDB{}})
		start := time.Now()
		first, err := svc.RotateWebhookSecret(t.Context(), p, id.String(), integration.GitLab, "192.0.2.1")
		entropy, decode := hex.DecodeString(string(store.current))
		if err != nil || decode != nil || len(entropy) != 32 || string(store.previous) != "old-secret" ||
			first.Version != 2 || !first.UpdatedAt.Equal(start) || !store.until.Equal(start.Add(24*time.Hour)) {
			t.Fatalf("first rotation: %+v %v, entropy=%d until=%s", first, err, len(entropy), store.until)
		}
		firstSecret := slices.Clone(store.current)
		time.Sleep(2 * time.Hour) //nolint:forbidigo // synctest advances virtual time without a wall-clock sleep.
		if _, err := svc.RotateWebhookSecret(t.Context(), p, id.String(), integration.GitLab, "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(store.previous, firstSecret) || slices.Equal(store.current, firstSecret) ||
			!store.until.Equal(start.Add(26*time.Hour)) || store.row.Version != 3 || len(store.audits) != 2 {
			t.Fatalf("second rotation did not replace the overlap: until=%s version=%d audits=%d", store.until, store.row.Version, len(store.audits))
		}
		for _, entry := range store.audits {
			if entry.Actor.Username != "admin" || entry.RemoteIP != "192.0.2.1" || entry.AccountID != p.AccountID.ULID ||
				entry.Action != "vcs_integration.rotate_webhook_secret" || entry.EventType != "UPDATE" ||
				entry.AffectedResource.Type != "VCS_INTEGRATION" || entry.AffectedResource.ID != id.String() ||
				len(entry.AffectedResource.Metadata) != 0 || len(entry.Body) != 0 {
				t.Fatalf("rotation audit: %+v", entry)
			}
		}
		p.Kind, p.Login = kernel.PrincipalRun, ""
		svc = integration.New(integration.Deps{Store: store, DB: rotationDB{}, SecretOverlap: 90 * time.Minute})
		if _, err := svc.RotateWebhookSecret(t.Context(), p, id.String(), integration.GitLab, ""); err != nil {
			t.Fatal(err)
		}
		if !store.until.Equal(time.Now().Add(90*time.Minute)) || store.audits[2].Actor != (kernel.AuditActor{Username: "infraweave", Run: p.ActorID}) {
			t.Fatal("configured overlap or run actor was lost")
		}
	})
}

func TestUnitWebhookRotationFailures(t *testing.T) {
	cause := errors.New("storage unavailable")
	for _, where := range []string{"begin", "read", "rotate", "audit", "missing", "provider", "bad-id"} {
		t.Run(where, func(t *testing.T) {
			p := kernel.Principal{AccountID: kernel.NewAccountID(), ActorID: kernel.NewULID()}
			rowID := kernel.NewULID()
			id := rowID.String()
			store := &rotationStore{row: integration.Integration{Account: p.AccountID, ID: rowID, Provider: integration.GitLab}}
			db := rotationDB{}
			provider, want := integration.GitLab, cause
			switch where {
			case "begin":
				db.fail = cause
			case "read":
				store.failGet = cause
			case "rotate":
				store.failRotate = cause
			case "audit":
				store.failAudit = cause
			case "missing":
				id, want = kernel.NewULID().String(), integration.ErrNotFound
			case "provider":
				provider, want = integration.BitbucketCloud, integration.ErrNotFound
			case "bad-id":
				id, want = "bad", integration.ErrNotFound
			}
			svc := integration.New(integration.Deps{Store: store, DB: db})
			if _, err := svc.RotateWebhookSecret(t.Context(), p, id, provider, ""); !errors.Is(err, want) {
				t.Fatalf("%s: %v", where, err)
			}
			if len(store.audits) != 0 {
				t.Fatal("failed rotation recorded an audit")
			}
		})
	}
}

func TestUnitWebhookEndpoints(t *testing.T) {
	account := kernel.NewAccountID()
	store := &providerStore{}
	svc := integration.New(integration.Deps{Store: store, Public: "https://iw.example/"})
	for _, provider := range []string{integration.GitLab, integration.BitbucketCloud, integration.BitbucketDC, integration.AzureDevOps, integration.GitHubEnterprise} {
		id := kernel.NewULID()
		store.rows = append(store.rows, integration.Integration{Account: account, ID: id, Provider: provider, IsDefault: true})
		endpoint, err := svc.WebhookEndpoint(t.Context(), kernel.Principal{AccountID: account}, provider, "")
		if err != nil || endpoint != svc.WebhookURL(provider, id) {
			t.Fatalf("%s default URL: %s %v", provider, endpoint, err)
		}
		if _, err := svc.WebhookEndpoint(t.Context(), kernel.Principal{AccountID: kernel.NewAccountID()}, provider, ""); !errors.Is(err, integration.ErrNotFound) {
			t.Fatal("foreign default", err)
		}
	}
	named := kernel.NewULID()
	store.rows = append(store.rows, integration.Integration{Account: account, ID: named, Provider: integration.GitHubEnterprise})
	p := kernel.Principal{AccountID: account}
	if got, err := svc.WebhookEndpoint(t.Context(), p, integration.GitHubEnterprise, named.String()); err != nil || got != svc.WebhookURL(integration.GitHubEnterprise, named) {
		t.Fatal("named App URL", got, err)
	}
	if _, err := svc.WebhookEndpoint(t.Context(), p, integration.GitLab, named.String()); !errors.Is(err, integration.ErrNotFound) {
		t.Fatal("wrong provider", err)
	}
	store.fail = errors.New("lookup failed")
	if _, err := svc.WebhookEndpoint(t.Context(), p, integration.GitLab, ""); !errors.Is(err, store.fail) {
		t.Fatal("default lookup failure", err)
	}
	if store.secretReads != 0 {
		t.Fatal("URL lookup opened a secret")
	}
}

type webhookProbe struct {
	input port.WebhookInput
	err   error
	calls int
}

func (*webhookProbe) Test(context.Context, port.Integration) ([]port.Check, error) {
	return []port.Check{{Type: port.CheckConnection, Passed: true}}, nil
}

func (p *webhookProbe) TestWebhooks(_ context.Context, _ port.Integration, input port.WebhookInput) (port.Check, error) {
	p.input = input
	p.calls++
	return port.Check{Type: port.CheckWebhook, Passed: true}, p.err
}

type webhookRepositories struct {
	stacksapi.Stacks
	account kernel.AccountID
	use     stacksapi.Use
	err     error
}

func (s *webhookRepositories) VCSRepositories(_ context.Context, account kernel.AccountID, use stacksapi.Use) ([]stacksapi.VCSRepository, error) {
	s.account, s.use = account, use
	return []stacksapi.VCSRepository{{Namespace: "acme", Name: "private"}, {Namespace: "acme", Name: "public"}}, s.err
}

func TestUnitWebhookTestRepositoryScope(t *testing.T) {
	account, id := kernel.NewAccountID(), kernel.NewULID()
	store := &providerStore{rows: []integration.Integration{{Account: account, ID: id, Provider: integration.GitHubEnterprise, IsDefault: true}}}
	probe, repositories := &webhookProbe{}, &webhookRepositories{}
	svc := integration.New(integration.Deps{Store: store, Stacks: repositories, Public: "https://iw.example", Testers: map[string]port.Tester{integration.GitHubEnterprise: probe}})
	p := kernel.Principal{AccountID: account}
	checks, err := svc.Test(t.Context(), p, id.String(), integration.GitHubEnterprise)
	if err != nil || len(checks) != 2 || checks[1].Type != port.CheckWebhook || !checks[1].Passed || repositories.account != account ||
		repositories.use.Integration != id || !repositories.use.Default || !slices.Equal(repositories.use.Aliases, []string{integration.GitHub}) ||
		probe.input.URL != svc.WebhookURL(integration.GitHubEnterprise, id) || len(probe.input.Repositories) != 2 || probe.input.Repositories[0].Name != "private" {
		t.Fatalf("repository scope: checks=%+v account=%s use=%+v input=%+v %v", checks, repositories.account, repositories.use, probe.input, err)
	}
	repositories.err = errors.New("repository read failed")
	if _, err := svc.Test(t.Context(), p, id.String(), integration.GitHubEnterprise); !errors.Is(err, repositories.err) || probe.calls != 1 {
		t.Fatal("repository failure reached provider", err)
	}
	repositories.err = nil
	probe.err = errors.New("hook check failed")
	if _, err := svc.Test(t.Context(), p, id.String(), integration.GitHubEnterprise); !errors.Is(err, probe.err) {
		t.Fatal("provider failure lost", err)
	}
}
