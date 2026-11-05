package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

func full() kernel.AuditEntry {
	return kernel.AuditEntry{
		AccountID: kernel.NewULID(),
		Actor:     kernel.AuditActor{Username: "infraweave", Run: kernel.NewULID()},
		Action:    "stack.update",
		EventType: "UPDATE",
		AffectedResource: kernel.AuditResource{
			Type: "STACK", ID: "prod-network", Metadata: json.RawMessage(`{"name":"Prod network"}`),
		},
		RelatedResource: &kernel.AuditResource{
			Type: "SPACE", ID: "root", Metadata: json.RawMessage(`{"name":"root"}`),
		},
		Body:         json.RawMessage(`{"description":"VPCs"}`),
		RemoteIP:     "192.0.2.10",
		IdentityULID: kernel.NewULID(),
		OwnerLogin:   "acme",
		CreatedAt:    time.Date(2026, 11, 5, 12, 0, 0, 123456000, time.UTC),
	}
}

// minimal has only the required fields.
func minimal() kernel.AuditEntry {
	return kernel.AuditEntry{
		AccountID:        kernel.NewULID(),
		Actor:            kernel.AuditActor{Username: "alice"},
		Action:           "context.delete",
		EventType:        "DELETE",
		AffectedResource: kernel.AuditResource{Type: "CONTEXT", ID: "shared"},
	}
}

func record(
	t *testing.T,
	pool *pgxpool.Pool,
	e kernel.AuditEntry,
	fail error,
) (kernel.AuditEntry, error) {
	t.Helper()
	var stored kernel.AuditEntry
	err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		var err error
		if stored, err = audit.Record(ctx, e); err != nil {
			return err
		}
		return fail
	})
	return stored, err
}

func read(t *testing.T, pool *pgxpool.Pool, account, id kernel.ULID) (kernel.AuditEntry, bool) {
	t.Helper()
	var (
		e                               kernel.AuditEntry
		relatedType, relatedID          *string
		remoteIP, owner                 *string
		affectedMeta, relatedMeta, body []byte
	)
	err := pool.QueryRow(t.Context(), `
SELECT account_id, id, actor_username, actor_run, action, event_type, affected_type, affected_id,
    affected_metadata, related_type, related_id, related_metadata, body, remote_ip, identity_ulid,
    owner_login, created_at
FROM audit_trail_entries WHERE account_id = $1 AND id = $2`, account, id).Scan(
		&e.AccountID, &e.ID, &e.Actor.Username, &e.Actor.Run, &e.Action, &e.EventType,
		&e.AffectedResource.Type, &e.AffectedResource.ID, &affectedMeta, &relatedType, &relatedID,
		&relatedMeta, &body, &remoteIP, &e.IdentityULID, &owner, &e.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return kernel.AuditEntry{}, false
		}
		t.Fatal(err)
	}
	e.AffectedResource.Metadata = affectedMeta
	if relatedType != nil {
		e.RelatedResource = &kernel.AuditResource{
			Type:     *relatedType,
			ID:       *relatedID,
			Metadata: relatedMeta,
		}
	}
	e.Body = body
	if remoteIP != nil {
		e.RemoteIP = *remoteIP
	}
	if owner != nil {
		e.OwnerLogin = *owner
	}
	e.CreatedAt = e.CreatedAt.UTC()
	return e, true
}

func TestAccAuditRecord(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	for name, e := range map[string]kernel.AuditEntry{"every field": full(), "required fields": minimal()} {
		t.Run(name, func(t *testing.T) {
			stored, err := record(t, pool, e, nil)
			if err != nil {
				t.Fatal(err)
			}
			if stored.ID.IsZero() || stored.CreatedAt.IsZero() {
				t.Fatalf("stored entry has ID %s and CreatedAt %s", stored.ID, stored.CreatedAt)
			}
			if !e.CreatedAt.IsZero() && !stored.CreatedAt.Equal(e.CreatedAt) {
				t.Errorf("CreatedAt %s, want the caller's %s", stored.CreatedAt, e.CreatedAt)
			}
			got, ok := read(t, pool, e.AccountID, stored.ID)
			if !ok {
				t.Fatal("committed entry not stored")
			}
			want := stored
			want.CreatedAt = want.CreatedAt.UTC().Truncate(time.Microsecond)
			if g, w := normal(t, got), normal(t, want); !reflect.DeepEqual(g, w) {
				t.Errorf("stored\n%+v\nwant\n%+v", g, w)
			}
		})
	}

	t.Run("rollback", func(t *testing.T) {
		boom := errors.New("the change failed")
		e := minimal()
		stored, err := record(t, pool, e, boom)
		if !errors.Is(err, boom) {
			t.Fatalf("tx.Do = %v, want the change's error", err)
		}
		if _, ok := read(t, pool, e.AccountID, stored.ID); ok {
			t.Error("an entry of a rolled-back change is stored")
		}
	})
}

// normal puts e's JSON documents in compact form, since jsonb stores them
// in its own, so that entries compare with reflect.DeepEqual.
func normal(t *testing.T, e kernel.AuditEntry) kernel.AuditEntry {
	t.Helper()
	compact := func(doc json.RawMessage) json.RawMessage {
		if doc == nil {
			return nil
		}
		var b bytes.Buffer
		if err := json.Compact(&b, doc); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	e.Body = compact(e.Body)
	e.AffectedResource.Metadata = compact(e.AffectedResource.Metadata)
	if r := e.RelatedResource; r != nil {
		e.RelatedResource = &kernel.AuditResource{
			Type:     r.Type,
			ID:       r.ID,
			Metadata: compact(r.Metadata),
		}
	}
	return e
}

func TestUnitRecordNeedsTransaction(t *testing.T) {
	if _, err := audit.Record(t.Context(), minimal()); !errors.Is(err, audit.ErrNoTransaction) {
		t.Errorf("Record without a transaction = %v, want ErrNoTransaction", err)
	}
}

func TestUnitRecordRefusesIncompleteEntries(t *testing.T) {
	for name, change := range map[string]func(*kernel.AuditEntry){
		"no account":             func(e *kernel.AuditEntry) { e.AccountID = kernel.ULID{} },
		"no actor":               func(e *kernel.AuditEntry) { e.Actor.Username = "" },
		"no action":              func(e *kernel.AuditEntry) { e.Action = "" },
		"no event type":          func(e *kernel.AuditEntry) { e.EventType = "" },
		"no affected type":       func(e *kernel.AuditEntry) { e.AffectedResource.Type = "" },
		"no affected ID":         func(e *kernel.AuditEntry) { e.AffectedResource.ID = "" },
		"related without an ID":  func(e *kernel.AuditEntry) { e.RelatedResource = &kernel.AuditResource{Type: "SPACE"} },
		"related without a type": func(e *kernel.AuditEntry) { e.RelatedResource = &kernel.AuditResource{ID: "root"} },
	} {
		e := minimal()
		change(&e)
		if _, err := audit.Record(t.Context(), e); !errors.Is(err, audit.ErrEntry) {
			t.Errorf("%s: %v, want ErrEntry", name, err)
		}
	}
}

func TestAccAuditRecordInvalidBody(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	e := minimal()
	e.Body = json.RawMessage(`{"unterminated`)
	if _, err := record(t, pool, e, nil); err == nil || errors.Is(err, kernel.ErrUnavailable) {
		t.Errorf("Record with a body that is not JSON = %v, want PostgreSQL's refusal", err)
	}
}
