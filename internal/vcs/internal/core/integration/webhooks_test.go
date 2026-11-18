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
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
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
		time.Sleep(2 * time.Hour)
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
