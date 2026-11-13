package app

import (
	"context"
	"log/slog"
	"slices"
	"time"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	configurationassembly "github.com/weaveplatform/infraweave/internal/configuration/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsassembly "github.com/weaveplatform/infraweave/internal/runs/assembly"
	stacksassembly "github.com/weaveplatform/infraweave/internal/stacks/assembly"
	stateassembly "github.com/weaveplatform/infraweave/internal/state/assembly"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
	workersassembly "github.com/weaveplatform/infraweave/internal/workers/assembly"
)

// CountOrphans is integrity.orphans, the nightly count of IDs a domain
// holds whose row another domain has deleted. It runs on the maintenance
// queue with policy Ignore, so a slow count never overlaps the next.
//
// ref: decision 0004
var CountOrphans = queue.Kind{
	Name: "integrity.orphans", Queue: queue.Maintenance, Policy: queue.Ignore,
}

// orphansEvery is how often integrity.orphans runs.
const orphansEvery = 24 * time.Hour

// held are the columns of every domain that hold another domain's rows.
// Only internal/app sees every domain, so only it can pair them with
// their owners' keys.
func held() []integrity.Held {
	return slices.Concat(
		featuresassembly.Held(),
		stacksassembly.Held(),
		configurationassembly.Held(),
		runsassembly.Held(),
		stateassembly.Held(),
		vcsassembly.Held(),
		workersassembly.Held(),
	)
}

// keys are the key columns of every domain whose rows another domain's
// columns hold, which held's columns name.
func keys() []integrity.Key {
	return slices.Concat(
		accessassembly.Keys(),
		stacksassembly.Keys(),
		runsassembly.Keys(),
		workersassembly.Keys(),
	)
}

// orphansTask counts, through q, each held column's IDs whose row is gone,
// and logs each column that has any at warn. A purge removes a deleted
// account's rows soon after the deletion, so a count that stays above zero
// from one night to the next names a purge that failed for good, or rows
// deleted outside one.
func orphansTask(q tx.Querier, log *slog.Logger) cron.Task {
	return cron.Task{
		Kind:  CountOrphans,
		Every: orphansEvery,
		Handler: func(ctx context.Context, _ queue.Job) error {
			counts, err := integrity.Count(ctx, q, keys(), held())
			if err != nil {
				return err //nolint:wrapcheck // names the column
			}
			for _, c := range counts {
				if c.Count > 0 {
					log.Warn("held IDs whose row is gone",
						"held", c.Held.String(), "entity", c.Held.Entity, "ids", c.Count)
				}
			}
			return nil
		},
	}
}
