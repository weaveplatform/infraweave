package port

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Delivery is the idempotency record of one sink target for an event.
type Delivery struct {
	Account   kernel.ULID
	EventID   kernel.ULID
	Sink      string
	Target    string
	RuleIndex int
	CreatedAt time.Time
}

// Store records fan-out in the caller's transaction and bounds cleanup.
type Store interface {
	Insert(ctx context.Context, delivery Delivery) (bool, error)
	Prune(ctx context.Context, before time.Time, limit int) (int64, error)
	Purge(ctx context.Context, account kernel.ULID, limit int) (int64, error)
}

// Metrics records committed delivery outcomes with bounded attributes.
type Metrics interface {
	Add(ctx context.Context, outcome string, count int64)
}
