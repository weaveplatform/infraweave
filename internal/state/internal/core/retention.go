package core

import (
	"context"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// RetentionKind is the periodic task that deletes the versions whose
// retention has passed. It is housekeeping that no caller waits on, so it
// runs on the maintenance queue, and Ignore keeps a slow pass from
// overlapping the next.
//
// ref: decision 0010 § "Rollback, searches and retention"
// ref: decision 0011 § "Scheduling and cron"
var RetentionKind = queue.Kind{Name: "state.retention", Queue: queue.Maintenance, Policy: queue.Ignore}

// RetentionEvery is how often RetentionKind runs: daily, so a version
// outlives its retention by a day at most, for one pass over the versions
// a day.
const RetentionEvery = 24 * time.Hour

// DefaultRetention is state.noncurrent_retention's default, 90 days.
const DefaultRetention = 90 * 24 * time.Hour

// Expired is a version retention may delete: its account and ID, which
// order a pass, and the run that wrote it, nil when no run did.
type Expired struct {
	Account kernel.AccountID
	ID      kernel.ULID
	Run     *kernel.ULID
}

// Retain deletes every version a newer version of its workspace replaced
// more than the service's retention ago, PurgeBatch at a time in batches,
// as Purge does, and answers how many it deleted; a retention of 0 keeps
// every version. It never deletes a workspace's current version, or a
// version whose run has not ended, which a pass after the run ends
// deletes. A pass walks the versions once, in the order of their accounts
// and IDs, so the versions it keeps never hold up the rest. A version
// that may be deleted stays so, since no write makes an older version
// current again and an ended run never runs again, so a write while the
// pass runs only adds a version too new to delete.
//
// ref: decision 0010 § "Rollback, searches and retention"
func (s *Service) Retain(ctx context.Context) (int, error) {
	if s.retention <= 0 {
		return 0, nil
	}
	var (
		after   Expired
		deleted int
	)
	err := s.batches(ctx, func(ctx context.Context) ([]string, bool, error) {
		found, err := s.store.Expired(ctx, after, s.retention, PurgeBatch)
		if err != nil {
			return nil, false, err //nolint:wrapcheck // the store names the read
		}
		var keys []string
		for _, group := range byAccount(found) {
			ids, err := s.deletable(ctx, group)
			if err != nil {
				return nil, false, err
			}
			if len(ids) == 0 {
				continue
			}
			k, err := s.store.DeleteVersions(ctx, group[0].Account, ids)
			if err != nil {
				return nil, false, err //nolint:wrapcheck // the store names the delete
			}
			keys = append(keys, k...)
		}
		// A transaction retried after a conflict reads from the same
		// version again, so the pass moves on only once this one commits.
		tx.AfterCommit(ctx, func(context.Context) {
			if len(found) > 0 {
				after = found[len(found)-1]
			}
			deleted += len(keys)
		})
		return keys, len(found) < PurgeBatch, nil
	})
	return deleted, err
}

// deletable answers the IDs of those of one account's versions whose run,
// if any, has ended, asking the runs domain about each run once, and not
// at all when no run wrote any of them.
func (s *Service) deletable(ctx context.Context, group []Expired) ([]kernel.ULID, error) {
	var (
		runs, under []kernel.ULID
		err         error
	)
	for _, v := range group {
		if v.Run != nil && !slices.Contains(runs, *v.Run) {
			runs = append(runs, *v.Run)
		}
	}
	if len(runs) > 0 {
		if under, err = s.ends.NotEnded(ctx, group[0].Account, runs); err != nil {
			return nil, err //nolint:wrapcheck // the runs domain names the read
		}
	}
	ids := make([]kernel.ULID, 0, len(group))
	for _, v := range group {
		if v.Run == nil || !slices.Contains(under, *v.Run) {
			ids = append(ids, v.ID)
		}
	}
	return ids, nil
}

// byAccount splits versions, in the order of their accounts, into one
// group per account, each in its order.
func byAccount(versions []Expired) [][]Expired {
	var out [][]Expired
	for i, v := range versions {
		if i == 0 || v.Account != versions[i-1].Account {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], v)
	}
	return out
}
