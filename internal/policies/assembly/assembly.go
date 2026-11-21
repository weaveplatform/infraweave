package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/http"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ContractPattern is the policy schema route on the API listener.
const ContractPattern = http.ContractPattern

// Domain wires policy persistence, management and contract publication on the
// shared API listener.
type Domain struct {
	server http.Server
	svc    *core.Service
	store  *pg.Store
}

// Deps supplies persistence, space ownership, authorization and the bounded runtime.
type Deps struct {
	DB      pg.DB
	Spaces  accessapi.Spaces
	Authz   accessapi.Authorizer
	Runtime *rego.Engine
	Events  *outbox.Outbox
	Stacks  stacksapi.Stacks
	Targets stacksapi.AttachmentTargets
	Now     func() time.Time
}

// Entity identifies policy IDs to the shared subject resolver.
const Entity = "policy"

// Stream is the policies domain's migration stream, before runs.
func Stream() migrate.Stream { return pg.Stream() }

// Held declares cross-domain ownership for the shared integrity audit.
func Held() []integrity.Held { return pg.Held() }

// SpaceGuard prevents removing spaces that still own policies.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.svc }

// Surfaces supplies the domain-owned compiler to the shared editor registry.
func (d *Domain) Surfaces() []expr.Surface { return []expr.Surface{d.svc.CheckSurface()} }

// New constructs policy management over its owned store and domain ports.
func New(d Deps) *Domain {
	store := pg.New(d.DB)
	svc := core.New(
		core.Deps{
			Store:   store,
			DB:      d.DB,
			Spaces:  d.Spaces,
			Authz:   d.Authz,
			Runtime: d.Runtime,
			Events:  d.Events,
			Stacks:  d.Stacks,
			Targets: d.Targets,
			Queue:   queue.New(d.DB),
			Now:     d.Now,
		},
	)
	return &Domain{server: http.New(svc, d.Spaces), svc: svc, store: store}
}

// PurgeBatch bounds each account-deletion transaction.
const PurgeBatch = 500

// Purge resumes an account cleanup safely after any completed batch.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		done, err := d.store.Purge(ctx, acct, PurgeBatch)
		if err != nil || done {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}

// RegisterEvents installs the policy change contract before outbox consumers start.
func RegisterEvents(kinds *outbox.Kinds) error {
	for _, kind := range []struct {
		name    string
		version int
		schema  []byte
	}{
		{policyapi.ChangedKind, policyapi.ChangedVersion, policyapi.ChangedSchema},
		{policyapi.EvaluatedKind, policyapi.EvaluationEventVersion, policyapi.EvaluatedSchema},
		{policyapi.InternalErrorKind, policyapi.EvaluationEventVersion, policyapi.InternalErrorSchema},
	} {
		if err := kinds.Register(kind.name, kind.version, kind.schema); err != nil {
			return fmt.Errorf("policies: register %s: %w", kind.name, err)
		}
	}
	return nil
}

// Evaluator supplies transactional decision evidence without owning run state.
func (d *Domain) Evaluator() policyapi.Evaluator { return d.svc }

// RunInputBuilder supplies the typed projection for run workflow callers.
func (d *Domain) RunInputBuilder() policyapi.RunInputBuilder { return d.svc }

// TaskInputBuilder supplies the typed pre-creation command/session projection.
func (d *Domain) TaskInputBuilder() policyapi.TaskInputBuilder { return d.svc }

// PushInputBuilder supplies the typed projection for stored provider events.
func (d *Domain) PushInputBuilder() policyapi.PushInputBuilder { return d.svc }

// Autoattach names the durable stack-change consumer on every replica.
const Autoattach = "policies.autoattach"

// Subscribe installs policy reconciliation before the shared outbox starts.
func (d *Domain) Subscribe(events *outbox.Outbox) error {
	return events.Subscribe(stacksapi.ChangedKind, Autoattach, stackChanged(d.svc.StackChanged)) //nolint:wrapcheck // the outbox names the consumer
}

// Jobs binds accepted space versions to reconciliation of their current tree.
func (d *Domain) Jobs() []queue.Registration {
	return []queue.Registration{{Kind: core.Reconcile, Handler: func(ctx context.Context, j queue.Job) error {
		return d.svc.ReconcileAccount(ctx, kernel.AccountID{ULID: j.Account})
	}, Options: queue.HandlerOptions{Feature: "policies"}}}
}

// StackGuard fences explicit attachment reach and removes deleted stack rows.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.svc.StackGuard() }

func stackChanged(run func(context.Context, kernel.AccountID, kernel.ULID) error) outbox.Handler {
	return func(ctx context.Context, ev outbox.Event, _ queue.Job) error {
		var changed stacksapi.Changed
		if err := json.Unmarshal(ev.Document, &changed); err != nil {
			return queue.Permanent(fmt.Errorf("policies: decode %s %s: %w", ev.Kind, ev.ID, err)) //nolint:wrapcheck // marks an invalid event
		}
		return run(ctx, kernel.AccountID{ULID: ev.Account}, changed.Stack)
	}
}

// SpaceOf resolves a policy subject using both its account and immutable ID.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.svc.SpaceOf }

// AttachmentEntity resolves subjects by attachment ID rather than policy ID.
const AttachmentEntity = "policy_attachment"

// AttachmentSpaceOf resolves the owning stack's space for attachment mutations.
func (d *Domain) AttachmentSpaceOf() accessapi.SpaceOf { return d.svc.AttachmentSpaceOf }

// StackPolicies is the readable projection port bound by app after construction.
func (d *Domain) StackPolicies() stacksapi.StackPolicies { return d.svc }

// Identities supplies account-qualified policy existence for historical views.
func (d *Domain) Identities() policyapi.PolicyIdentities { return d.svc }

// Routes registers the domain's routes. A nil domain serves none, for tests
// which construct only the domains their request reaches.
func (d *Domain) Routes(r chi.Router) {
	if d != nil {
		d.server.Routes(r)
	}
}

// PoliciesAPI is the domain's partial of the shared strict server.
type PoliciesAPI = http.Server

// API is the domain's shared API partial. The zero partial still reads linked
// build metadata for tests constructing only the API surface under inspection.
func (d *Domain) API() PoliciesAPI {
	if d == nil {
		return PoliciesAPI{}
	}
	return d.server
}
