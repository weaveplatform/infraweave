// Package push turns verified provider events into bounded per-stack work.
package push

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var (
	// ErrCapped refuses a new run once the delivery has used its budget.
	ErrCapped = errors.New("vcs: event run limit reached")
	// ErrNoTransaction refuses admission outside a run-creating transaction.
	ErrNoTransaction = errors.New("vcs: event run admission requires a transaction")
)

// LimitStore keeps the account cap and reserves slots on the delivery in the
// caller's transaction. A null account cap still uses the operator ceiling.
type LimitStore interface {
	RunLimit(context.Context, kernel.AccountID) (*int, error)
	SetRunLimit(context.Context, kernel.AccountID, *int) error
	ReserveRun(context.Context, kernel.AccountID, kernel.ULID, int) (bool, error)
	PurgeRunLimit(context.Context, kernel.AccountID) error
	Audit(context.Context, kernel.AuditEntry) error
}

// Limits supplies the API setting and the admission check used immediately
// before a new event run is inserted. It keeps no per-replica setting cache.
type Limits struct {
	DB      tx.Beginner
	Store   LimitStore
	Spaces  accessapi.Spaces
	Ceiling int
	Now     func() time.Time
}

// Get is the caller account's stored cap, nil when none is configured.
func (l *Limits) Get(ctx context.Context, acct kernel.AccountID) (*int, error) {
	return l.Store.RunLimit(ctx, acct) //nolint:wrapcheck // the store names the read
}

// Set writes the account setting and its audit entry in one transaction. The
// top-space hold prevents a concurrent account deletion from leaving a new row.
func (l *Limits) Set(ctx context.Context, p kernel.Principal, limit *int, remoteIP string) (*int, error) {
	if limit != nil && (*limit < 1 || *limit > l.Ceiling) {
		reason := kernel.ReasonMinimum
		if *limit > l.Ceiling {
			reason = kernel.ReasonMaximum
		}
		return nil, &kernel.Invalid{
			Detail: "The VCS event run limit is outside its allowed range.",
			Errors: []kernel.FieldError{{
				Pointer: "/limit", Reason: reason,
				Message: fmt.Sprintf("The limit must be null or between 1 and the operator ceiling of %d.", l.Ceiling),
			}},
		}
	}
	err := tx.Do(ctx, l.DB, tx.Options{}, func(ctx context.Context) error {
		if err := l.Spaces.Hold(ctx, p.AccountID, p.AccountID.ULID); err != nil {
			return err //nolint:wrapcheck // access names the account hold
		}
		if err := l.Store.SetRunLimit(ctx, p.AccountID, limit); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return l.Store.Audit(ctx, kernel.AuditEntry{ //nolint:wrapcheck // the audit boundary names the write
			AccountID: p.AccountID.ULID, Actor: kernel.AuditActor{Username: p.Login},
			Action: "account.update_vcs_event_triggered_runs_limit", EventType: "UPDATE",
			AffectedResource: kernel.AuditResource{Type: "ACCOUNT", ID: p.AccountID.String()},
			RemoteIP:         remoteIP, CreatedAt: l.Now(),
		})
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // each transaction step names its failure
	}
	return limit, nil
}

// Effective always applies the operator ceiling, including when it is lowered
// below an account setting that was valid when saved.
func (l *Limits) Effective(ctx context.Context, acct kernel.AccountID) (int, error) {
	limit, err := l.Store.RunLimit(ctx, acct)
	if err != nil {
		return 0, err //nolint:wrapcheck // the store names the read
	}
	if limit != nil {
		return min(*limit, l.Ceiling), nil
	}
	return l.Ceiling, nil
}
