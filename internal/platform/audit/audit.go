package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var (
	// ErrNoTransaction reports a Record made outside a transaction, whose
	// entry could outlive or miss the change it describes.
	ErrNoTransaction = errors.New("audit entry recorded outside a transaction")
	// ErrEntry reports an entry without an account, actor, action, event
	// type or affected resource, or with a related resource without a type
	// or ID.
	ErrEntry = errors.New("invalid audit entry")
)

const insert = `
INSERT INTO audit_trail_entries (account_id, id, actor_username, actor_run, action, event_type,
    affected_type, affected_id, affected_metadata, related_type, related_id, related_metadata,
    body, remote_ip, identity_ulid, owner_login, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, nullif($14, ''), $15,
    nullif($16, ''), $17)`

// Record inserts e into its account's trail inside the transaction on ctx,
// so the entry exists exactly when the change does; without a transaction
// it returns ErrNoTransaction. It assigns the entry's ID, sets CreatedAt
// when it is zero, and returns the entry as stored. Body is stored as
// given.
func Record(ctx context.Context, e kernel.AuditEntry) (kernel.AuditEntry, error) {
	if err := validate(e); err != nil {
		return kernel.AuditEntry{}, err
	}
	if !tx.In(ctx) {
		return kernel.AuditEntry{}, ErrNoTransaction
	}
	e.ID = kernel.NewULID()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	var related kernel.AuditResource
	if e.RelatedResource != nil {
		related = *e.RelatedResource
	}
	_, err := tx.Q(ctx, nil).Exec(ctx, insert, e.AccountID, e.ID, e.Actor.Username, e.Actor.Run,
		e.Action, e.EventType, e.AffectedResource.Type, e.AffectedResource.ID,
		jsonb(e.AffectedResource.Metadata), text(related.Type), text(related.ID),
		jsonb(related.Metadata), jsonb(e.Body), e.RemoteIP, e.IdentityULID, e.OwnerLogin,
		e.CreatedAt)
	if err != nil {
		return kernel.AuditEntry{}, fmt.Errorf(
			"record audit entry %s: %w",
			e.Action,
			sql.Unavailable(err),
		)
	}
	return e, nil
}

func validate(e kernel.AuditEntry) error {
	switch {
	case e.AccountID.IsZero(), e.Actor.Username == "", e.Action == "", e.EventType == "",
		e.AffectedResource.Type == "", e.AffectedResource.ID == "",
		e.RelatedResource != nil && (e.RelatedResource.Type == "" || e.RelatedResource.ID == ""):
		return fmt.Errorf("%w: account %s, actor %q, action %q, event type %q, affected %s %q",
			ErrEntry, e.AccountID, e.Actor.Username, e.Action, e.EventType,
			e.AffectedResource.Type, e.AffectedResource.ID)
	}
	return nil
}

// jsonb passes absent JSON as NULL rather than as an empty document.
func jsonb(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
