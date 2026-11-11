package app

import (
	"context"
	"encoding/json"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// Each domain that keeps rows of an account consumes accessapi.Deleted
// and deletes them, under its consumer's name. access deletes its own rows
// in the deletion's transaction, so nothing waits for the purges: the
// outbox retries a failed or stopped purge alone until it finishes, and
// the nightly orphan count reports rows a purge never reached. The
// consumers are bound here because features sits below access and cannot
// read its event.
//
// ref: decision 0004
const (
	featuresPurge      = "features.purge"
	stacksPurge        = "stacks.purge"
	configurationPurge = "configuration.purge"
	vcsPurge           = "vcs.purge"
	runsPurge          = "runs.purge"
)

// subscribePurges subscribes each domain's purge to accessapi.Deleted on
// events.
func subscribePurges(events *outbox.Outbox, d Domains) error {
	for consumer, run := range map[string]func(context.Context, accessapi.Deleted) error{
		featuresPurge: func(ctx context.Context, del accessapi.Deleted) error {
			return d.Features.Purge(ctx, kernel.OrganizationID{ULID: del.Organization},
				kernel.AccountID{ULID: del.Account}, del.OrganizationDeleted)
		},
		stacksPurge: func(ctx context.Context, del accessapi.Deleted) error {
			return d.Stacks.Purge(ctx, kernel.AccountID{ULID: del.Account})
		},
		configurationPurge: func(ctx context.Context, del accessapi.Deleted) error {
			return d.Configuration.Purge(ctx, kernel.AccountID{ULID: del.Account})
		},
		vcsPurge: func(ctx context.Context, del accessapi.Deleted) error {
			return d.VCS.Purge(ctx, kernel.AccountID{ULID: del.Account})
		},
		runsPurge: func(ctx context.Context, del accessapi.Deleted) error {
			return d.Runs.Purge(ctx, kernel.AccountID{ULID: del.Account})
		},
	} {
		if err := events.Subscribe(accessapi.DeletedKind, consumer, purge(run)); err != nil {
			return err //nolint:wrapcheck // names the consumer
		}
	}
	return nil
}

// purge is the outbox handler that decodes an accessapi.Deleted and runs
// run on it; a document that does not decode never will, so its job dies
// at once.
func purge(run func(context.Context, accessapi.Deleted) error) outbox.Handler {
	return func(ctx context.Context, ev outbox.Event, _ queue.Job) error {
		var del accessapi.Deleted
		if err := json.Unmarshal(ev.Document, &del); err != nil {
			return queue.Permanent(
				fmt.Errorf("%s %s: %w", ev.Kind, ev.ID, err),
			) //nolint:wrapcheck // marks it
		}
		return run(ctx, del)
	}
}
