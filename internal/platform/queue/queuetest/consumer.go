package queuetest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// ErrContract reports a consumer that fails the fenced completion contract.
var ErrContract = errors.New("invalid fenced consumer contract")

// ConsumerQueue is the queue boundary the fenced-consumer contract uses.
// The real PostgreSQL Queue supplies it in each domain's contract suite.
type ConsumerQueue interface {
	Enqueue(context.Context, queue.Kind, any, ...queue.Option) (int64, error)
	Claim(context.Context, queue.Name, []string, int, time.Duration, string) ([]queue.Job, error)
	Complete(context.Context, queue.Job) error
}

// ConsumeTwice drives two claimed jobs for the same logical delivery
// through consume. Each must complete atomically with its writes: a later
// unfenced completion must find the claim already lost. The caller then
// checks its own rows and target jobs for idempotency.
func ConsumeTwice(ctx context.Context, q ConsumerQueue, kind queue.Kind, account kernel.ULID, consume queue.Handler) error {
	for range 2 {
		if _, err := q.Enqueue(ctx, kind, struct{}{}, queue.Account(account)); err != nil {
			return fmt.Errorf("contract enqueue consumer: %w", err)
		}
		jobs, err := q.Claim(ctx, kind.Queue, []string{kind.Name}, 1, time.Minute, "consumer-contract")
		if err != nil {
			return fmt.Errorf("contract claim consumer: %w", err)
		}
		if len(jobs) != 1 {
			return fmt.Errorf("%w: expected one consumer, claimed %d", ErrContract, len(jobs))
		}
		if err := consume(ctx, jobs[0]); err != nil {
			return fmt.Errorf("contract consume: %w", err)
		}
		if err := q.Complete(ctx, jobs[0]); !errors.Is(err, queue.ErrLeaseLost) {
			if err == nil {
				return ErrContract
			}
			return fmt.Errorf("contract verify completion fence: %w", err)
		}
	}
	return nil
}
