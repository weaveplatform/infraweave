package state

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// retentionSteps move the servers' clock on for a stack's state versions
// and check which of them state.retention deleted. A real server's clock
// cannot be moved, so the versions age instead: each one's creation moves
// back by the time that passes, which is also when the version before it
// stopped being current, and the platform's "falls due" step runs the
// pass that reads them.
//
// ref: decision 0010 § "Rollback, searches and retention"
// ref: decision 0016
type retentionSteps struct{ w *harness.World }

// span is a span PostgreSQL reads as an interval, such as "2 hours".
const span = `(\d+ (?:minutes?|hours?|days?))`

func (r *retentionSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^`+span+` pass(?:es)? for the state versions of the stack "(\w+)"$`, r.age)
	sc.Step(`^the state versions? "([^"]+)" (?:is|are) deleted, with (?:its|their) objects?$`,
		r.deleted)
	sc.Step(`^the state versions? "([^"]+)" (?:is|are) kept, with (?:its|their) objects?$`,
		r.kept)
}

// age moves the creation of every version of the home account's stack
// slug back by passing.
func (r *retentionSteps) age(ctx context.Context, passing, slug string) error {
	var n int
	if err := r.w.Row(ctx, `WITH v AS (
			UPDATE managed_state_versions v SET created_at = v.created_at - $3::interval
			FROM stacks s
			WHERE s.account_id = $1 AND s.slug = $2
				AND v.account_id = s.account_id AND v.stack_id = s.id
			RETURNING 1)
		SELECT count(*) FROM v`,
		[]any{harness.UUIDOf(harness.Home.Account), slug, passing}, &n); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if n == 0 {
		return fmt.Errorf("%w: the stack %s has no state versions to age", harness.ErrStep, slug)
	}
	return nil
}

// versionIDs are the IDs kept as names, comma-separated.
func (r *retentionSteps) versionIDs(names string) ([]string, error) {
	var out []string
	for name := range strings.SplitSeq(names, ",") {
		id, ok := r.w.IDs[strings.TrimSpace(name)]
		if !ok {
			return nil, fmt.Errorf("%w: no state version %q", harness.ErrStep, name)
		}
		out = append(out, id)
	}
	return out, nil
}

// objects are the keys of the scenario's bucket. Every object holds the
// empty needle, so Containing lists them all.
func (r *retentionSteps) objects(ctx context.Context) ([]string, error) {
	store, err := r.w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // names @objectstore
	}
	keys, _, err := store.Containing(ctx, r.w.Bucket, nil)
	return keys, err //nolint:wrapcheck // names the bucket
}

// stored reports whether the version id has a row, a blob_objects row in
// the states store and an object in the bucket, each found by the key
// that ends in the version's ID, as every version's key does; live
// reports whether its blob_objects row is live.
func (r *retentionSteps) stored(ctx context.Context, id string) (row, record, live, object bool, err error) {
	if err := r.w.Row(ctx, `SELECT
			EXISTS (SELECT 1 FROM managed_state_versions WHERE id = $1),
			EXISTS (SELECT 1 FROM blob_objects WHERE store = 'states' AND key LIKE '%/' || $2),
			EXISTS (SELECT 1 FROM blob_objects WHERE store = 'states' AND key LIKE '%/' || $2
				AND state = 'live')`,
		[]any{harness.UUIDOf(id), id}, &row, &record, &live); err != nil {
		return false, false, false, false, err //nolint:wrapcheck // names the query
	}
	keys, err := r.objects(ctx)
	if err != nil {
		return false, false, false, false, err
	}
	object = slices.ContainsFunc(keys, func(k string) bool {
		return strings.HasPrefix(k, "states/") && strings.HasSuffix(k, "/"+id)
	})
	return row, record, live, object, nil
}

// deleted waits until no version kept as names has a row, a blob_objects
// row or an object left: retention deletes the row, and blob.delete the
// object and then its record once that commits.
func (r *retentionSteps) deleted(ctx context.Context, names string) error {
	ids, err := r.versionIDs(names)
	if err != nil {
		return err
	}
	for _, id := range ids {
		//nolint:wrapcheck // names what it waited for
		if err := deps.Eventually(ctx, "the state version "+id+" and its object to go",
			func(ctx context.Context) (bool, error) {
				row, record, _, object, err := r.stored(ctx, id)
				return !row && !record && !object, err
			}); err != nil {
			return err
		}
	}
	return nil
}

// kept checks that each version kept as names has its row, a live
// blob_objects row and its object in the bucket.
func (r *retentionSteps) kept(ctx context.Context, names string) error {
	ids, err := r.versionIDs(names)
	if err != nil {
		return err
	}
	for _, id := range ids {
		row, _, live, object, err := r.stored(ctx, id)
		if err != nil {
			return err
		}
		if !row || !live || !object {
			return fmt.Errorf("%w: the state version %s has a row %t, a live record %t and "+
				"an object %t", harness.ErrStep, id, row, live, object)
		}
	}
	return nil
}
