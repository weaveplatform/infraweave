package sample

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// DefaultRetention is worker.samples.retention's default: a month of
// daily usage assessments and a margin.
//
// ref: decision 0008 § "Pool samples"
const DefaultRetention = 35 * 24 * time.Hour

// PruneBatch bounds one statement of the prune and PruneRounds the
// statements of one pass. A pass deletes at most 100,000 samples, more
// than a minute's samples of that many pools, so the prune keeps up, and
// a shortened retention's backlog goes over several passes rather than
// holding up the next minute's sample.
const (
	PruneBatch  = 5000
	PruneRounds = 20
)

// Store is what the service needs of the samples' storage.
type Store interface {
	// Sample writes at's sample of each private pool of every account:
	// its busy and its registered workers, and queued[pool] of its runs. A
	// pool with a sample at at keeps it. It answers how many it wrote.
	Sample(ctx context.Context, at time.Time, queued map[kernel.ULID]int) (int, error)
	// Prune deletes up to batch of the samples taken before before, and
	// answers how many.
	Prune(ctx context.Context, before time.Time, batch int) (int, error)
	// Between reads acct's samples of pool taken from from up to, not
	// including, to, oldest first.
	Between(
		ctx context.Context,
		acct kernel.AccountID,
		pool kernel.ULID,
		from, to time.Time,
	) ([]workersapi.Sample, error)
}

// Deps are the service's: its Store, Retention, worker.samples.retention,
// and Now, its clock. A zero Retention takes DefaultRetention.
type Deps struct {
	Store     Store
	Retention time.Duration
	Now       func() time.Time
}

// Service is the pool samples' logic.
type Service struct {
	store     Store
	retention time.Duration
	now       func() time.Time
	scheduler workersapi.Scheduler
}

// New returns the service on d.
func New(d Deps) *Service {
	s := &Service{store: d.Store, retention: d.Retention, now: d.Now}
	if s.retention == 0 {
		s.retention = DefaultRetention
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Bind binds the runs domain's workersapi.Scheduler, which counts the
// runs waiting for each pool.
func (s *Service) Bind(sch workersapi.Scheduler) { s.scheduler = sch }

// Sampled is what one pass did: the samples it wrote and the expired
// ones it deleted.
type Sampled struct {
	Written, Pruned int
}

// Sample is worker.sample's pass. It writes each private pool's sample
// for the current minute, so a second pass in the same minute writes
// nothing, then deletes the samples older than worker.samples.retention.
// Until the runs domain's scheduler is bound, it counts no queued run.
//
// ref: decision 0008 § "Pool samples"
func (s *Service) Sample(ctx context.Context) (Sampled, error) {
	var out Sampled
	now := s.now()
	var queued map[kernel.ULID]int
	if s.scheduler != nil {
		var err error
		if queued, err = s.scheduler.Queued(ctx); err != nil {
			return out, err //nolint:wrapcheck // the runs domain names the read
		}
	}
	n, err := s.store.Sample(ctx, now.UTC().Truncate(time.Minute), queued)
	if err != nil {
		return out, err //nolint:wrapcheck // the store names the write
	}
	out.Written = n
	before := now.Add(-s.retention)
	for range PruneRounds {
		n, err := s.store.Prune(ctx, before, PruneBatch)
		if err != nil {
			return out, err //nolint:wrapcheck // the store names the delete
		}
		out.Pruned += n
		if n < PruneBatch {
			break
		}
	}
	return out, nil
}

// SamplesBetween reads acct's samples of pool taken from from up to, not
// including, to, oldest first; an empty window has none.
func (s *Service) SamplesBetween(
	ctx context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	from, to time.Time,
) ([]workersapi.Sample, error) {
	if !from.Before(to) {
		return nil, nil
	}
	return s.store.Between(ctx, acct, pool, from, to) //nolint:wrapcheck // the store names the read
}

var _ workersapi.Samples = (*Service)(nil)
