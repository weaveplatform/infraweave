package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func orgEntry() kernel.OrganizationAuditEntry {
	return kernel.OrganizationAuditEntry{
		OrganizationID:   kernel.NewOrganizationID(),
		ActorKind:        kernel.PrincipalOperator,
		ActorID:          kernel.NewULID().String(),
		Action:           "organization.create",
		EventType:        "CREATE",
		AffectedResource: kernel.AuditResource{Type: "ORGANIZATION", ID: "acme"},
	}
}

// TestAccAuditRecordOrganization checks that an organization entry is
// stored with every field, its remote address and body optional, and
// that a rolled-back change leaves none.
func TestAccAuditRecordOrganization(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	full := orgEntry()
	full.Body = json.RawMessage(`{"slug":"acme"}`)
	full.RemoteIP = "192.0.2.4"
	full.CreatedAt = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	for name, e := range map[string]kernel.OrganizationAuditEntry{"every field": full, "required fields": orgEntry()} {
		t.Run(name, func(t *testing.T) {
			var stored kernel.OrganizationAuditEntry
			err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
				var err error
				stored, err = audit.RecordOrganization(ctx, e)
				return err
			})
			if err != nil || stored.ID.IsZero() || stored.CreatedAt.IsZero() {
				t.Fatalf("RecordOrganization = %+v, %v", stored, err)
			}
			var (
				kind, actor, action, remote string
				body                        []byte
				at                          time.Time
			)
			if err := pool.QueryRow(
				t.Context(),
				`
SELECT actor_kind, actor_id, action, coalesce(remote_ip, ''), body, created_at
FROM organization_audit_trail_entries WHERE organization_id = $1 AND id = $2`,
				e.OrganizationID,
				stored.ID,
			).Scan(&kind, &actor, &action, &remote, &body, &at); err != nil {
				t.Fatal(err)
			}
			if kind != "operator" || actor != e.ActorID || action != e.Action ||
				remote != e.RemoteIP || (e.Body == nil) != (body == nil) ||
				!at.Equal(stored.CreatedAt.Truncate(time.Microsecond)) {
				t.Errorf("stored %s %s %s %q %s %s", kind, actor, action, remote, body, at)
			}
		})
	}
	rollback := errors.New("rollback")
	e := orgEntry()
	_ = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		if _, err := audit.RecordOrganization(ctx, e); err != nil {
			t.Fatal(err)
		}
		return rollback
	})
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM organization_audit_trail_entries WHERE organization_id = $1`,
		e.OrganizationID).Scan(&n); err != nil || n != 0 {
		t.Errorf("a rolled-back entry: %d, %v", n, err)
	}
	pool.Close()
	err := tx.Do(context.Background(), pool, tx.Options{}, func(ctx context.Context) error {
		_, err := audit.RecordOrganization(ctx, orgEntry())
		return err
	})
	if err == nil {
		t.Error("a closed pool recorded an entry")
	}
}

func TestUnitRecordOrganizationRefuses(t *testing.T) {
	if _, err := audit.RecordOrganization(
		t.Context(),
		orgEntry(),
	); !errors.Is(
		err,
		audit.ErrNoTransaction,
	) {
		t.Errorf("no transaction: %v", err)
	}
	for name, f := range map[string]func(*kernel.OrganizationAuditEntry){
		"organization": func(e *kernel.OrganizationAuditEntry) { e.OrganizationID = kernel.OrganizationID{} },
		"actor kind":   func(e *kernel.OrganizationAuditEntry) { e.ActorKind = "" },
		"actor":        func(e *kernel.OrganizationAuditEntry) { e.ActorID = "" },
		"action":       func(e *kernel.OrganizationAuditEntry) { e.Action = "" },
		"event type":   func(e *kernel.OrganizationAuditEntry) { e.EventType = "" },
		"affected":     func(e *kernel.OrganizationAuditEntry) { e.AffectedResource.ID = "" },
	} {
		e := orgEntry()
		f(&e)
		if _, err := audit.RecordOrganization(t.Context(), e); !errors.Is(err, audit.ErrEntry) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
