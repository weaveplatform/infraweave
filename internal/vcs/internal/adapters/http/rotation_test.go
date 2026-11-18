package http_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	vcshttp "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type responseRotationDB struct{ active bool }

func (d *responseRotationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if d.active {
		return nil, fmt.Errorf("%w: rotation kept its transaction open during view rendering", errBoom)
	}
	d.active = true
	return responseRotationTx{db: d}, nil
}

type responseRotationTx struct {
	pgx.Tx
	db *responseRotationDB
}

func (t responseRotationTx) Commit(context.Context) error   { t.db.active = false; return nil }
func (t responseRotationTx) Rollback(context.Context) error { t.db.active = false; return nil }

type responseRotationStore struct {
	integration.Store
	row        integration.Integration
	current    []byte
	first      []byte
	reads      int
	credential []byte
}

func (s *responseRotationStore) Get(context.Context, kernel.AccountID, kernel.ULID, bool) (integration.Integration, error) {
	return s.row, nil
}

func (s *responseRotationStore) RotateWebhookSecret(_ context.Context, row integration.Integration, secret []byte, _ time.Time) error {
	s.row, s.current = row, slices.Clone(secret)
	if s.first == nil {
		s.first = slices.Clone(secret)
	}
	return nil
}

func (*responseRotationStore) Audit(context.Context, kernel.AuditEntry) error { return nil }

func (s *responseRotationStore) Secrets(context.Context, kernel.AccountID, kernel.ULID) (integration.Secrets, error) {
	s.reads++
	return integration.Secrets{Credential: slices.Clone(s.credential), WebhookSecret: slices.Clone(s.current)}, nil
}

type responseRotationSpaces struct {
	accessapi.Spaces
	rotate func()
	top    kernel.ULID
}

func (s responseRotationSpaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	s.rotate()
	return map[kernel.ULID]string{s.top: "acme"}, nil
}

type responseRotationApps struct {
	port.Apps
	t     *testing.T
	db    *responseRotationDB
	store *responseRotationStore
}

func (a responseRotationApps) App(_ context.Context, in port.Integration) port.App {
	if a.db.active || string(in.Credential) != "original-app-key" || !slices.Equal(in.WebhookSecret, a.store.first) {
		a.t.Error("App view lost the committed credential/secret snapshot or kept its transaction open")
	}
	return port.App{Status: port.AppOK, URL: "https://github.example/apps/test"}
}

func TestUnitWebhookRotationResponseKeepsOwnSecret(t *testing.T) {
	for _, provider := range []string{integration.GitLab, integration.BitbucketCloud, integration.BitbucketDC, integration.AzureDevOps, integration.GitHubEnterprise} {
		t.Run(provider, func(t *testing.T) {
			p := kernel.Principal{AccountID: kernel.NewAccountID(), Login: "admin"}
			row := integration.Integration{Account: p.AccountID, ID: kernel.NewULID(), Space: kernel.NewULID(), Provider: provider}
			if provider == integration.GitHubEnterprise {
				row.Settings.AppID = "1"
			}
			st := &responseRotationStore{row: row, current: []byte("initial-secret"), credential: []byte("original-app-key")}
			db := &responseRotationDB{}
			var svc *integration.Service
			sp := responseRotationSpaces{top: row.Space, rotate: func() {
				rotated, err := svc.RotateWebhookSecret(t.Context(), p, row.ID.String(), provider, "")
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range []string{fmt.Sprint(rotated), fmt.Sprintf("%+v", rotated), fmt.Sprintf("%#v", rotated)} {
					if value != "[redacted]" {
						t.Fatal("rotation diagnostics must redact both snapshots")
					}
				}
				st.credential = []byte("newer-app-key")
			}}
			// App runs after the second rotation, but must see the first one's snapshot.
			app := responseRotationApps{t: t, db: db, store: st}
			svc = integration.New(integration.Deps{Store: st, DB: db, Apps: app})
			s := vcshttp.New(svc, sp, nil)
			ctx, id := kernel.WithPrincipal(t.Context(), p), row.ID.String()
			var secret string
			switch provider {
			case integration.GitLab:
				out, err := s.GitlabIntegrationRegenerateWebhookSecret(ctx, api.GitlabIntegrationRegenerateWebhookSecretRequestObject{GitlabIntegrationId: id})
				if err != nil {
					t.Fatal(err)
				}
				secret = out.(api.GitlabIntegrationRegenerateWebhookSecret200JSONResponse).Body.WebhookSecret
			case integration.BitbucketCloud:
				out, err := s.BitbucketCloudIntegrationRegenerateWebhookSecret(
					ctx,
					api.BitbucketCloudIntegrationRegenerateWebhookSecretRequestObject{BitbucketCloudIntegrationId: id},
				)
				if err != nil {
					t.Fatal(err)
				}
				secret = out.(api.BitbucketCloudIntegrationRegenerateWebhookSecret200JSONResponse).Body.WebhookSecret
			case integration.BitbucketDC:
				out, err := s.BitbucketDatacenterIntegrationRegenerateWebhookSecret(
					ctx,
					api.BitbucketDatacenterIntegrationRegenerateWebhookSecretRequestObject{BitbucketDatacenterIntegrationId: id},
				)
				if err != nil {
					t.Fatal(err)
				}
				secret = out.(api.BitbucketDatacenterIntegrationRegenerateWebhookSecret200JSONResponse).Body.WebhookSecret
			case integration.AzureDevOps:
				out, err := s.AzureDevOpsRepoIntegrationRegenerateWebhookSecret(
					ctx,
					api.AzureDevOpsRepoIntegrationRegenerateWebhookSecretRequestObject{AzureDevOpsRepoIntegrationId: id},
				)
				if err != nil {
					t.Fatal(err)
				}
				secret = out.(api.AzureDevOpsRepoIntegrationRegenerateWebhookSecret200JSONResponse).Body.WebhookPassword
			case integration.GitHubEnterprise:
				out, err := s.GithubEnterpriseIntegrationRegenerateWebhookSecret(
					ctx,
					api.GithubEnterpriseIntegrationRegenerateWebhookSecretRequestObject{GithubEnterpriseIntegrationId: id},
				)
				if err != nil {
					t.Fatal(err)
				}
				body := out.(api.GithubEnterpriseIntegrationRegenerateWebhookSecret200JSONResponse).Body
				secret = body.WebhookSecret
				if !body.GithubAppExists || body.AppUrl == "" {
					t.Fatal("App details disappeared")
				}
			}
			if secret != string(st.first) || secret == string(st.current) || st.row.Version != 2 {
				t.Fatal("rotation A answered rotation B's secret instead of its own committed value")
			}
			wantReads := 0
			if provider == integration.GitHubEnterprise {
				wantReads = 2
			}
			if st.reads != wantReads {
				t.Fatalf("secret reads: got %d, want %d transaction-local App credential snapshots", st.reads, wantReads)
			}
		})
	}
}
