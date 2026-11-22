package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	configurationassembly "github.com/weaveplatform/infraweave/internal/configuration/assembly"
	eventsassembly "github.com/weaveplatform/infraweave/internal/events/assembly"
	expressionsassembly "github.com/weaveplatform/infraweave/internal/expressions/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policiesassembly "github.com/weaveplatform/infraweave/internal/policies/assembly"
	runsassembly "github.com/weaveplatform/infraweave/internal/runs/assembly"
	stacksassembly "github.com/weaveplatform/infraweave/internal/stacks/assembly"
	stateassembly "github.com/weaveplatform/infraweave/internal/state/assembly"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
	vendorsassembly "github.com/weaveplatform/infraweave/internal/vendors/assembly"
	workersassembly "github.com/weaveplatform/infraweave/internal/workers/assembly"
)

// Domains are the business domains, constructed in the declared graph's
// order (repo-layout.md, "Declared graph"), so each is built after every
// domain whose api/ it imports. A back edge, a call to a domain built
// later, is bound here after construction.
type Domains struct {
	Features      *featuresassembly.Domain
	Access        *accessassembly.Domain
	Workers       *workersassembly.Domain
	Vendors       *vendorsassembly.Domain
	Stacks        *stacksassembly.Domain
	Configuration *configurationassembly.Domain
	Policies      *policiesassembly.Domain
	Runs          *runsassembly.Domain
	State         *stateassembly.Domain
	VCS           *vcsassembly.Domain
	Events        *eventsassembly.Domain
	Expressions   *expressionsassembly.Domain
	// Auth authenticates the API's requests; nil refuses every credential.
	Auth accessapi.Authenticator
	// OperatorAuth authenticates the operator API's requests, admitting
	// operator principals alone; nil refuses every credential.
	OperatorAuth accessapi.Authenticator
	// WorkerAuth authenticates the worker API's requests, admitting worker
	// session tokens alone; nil refuses every credential.
	WorkerAuth accessapi.Authenticator
	// StateAuth authenticates the state backend's requests, admitting run
	// tokens as HTTP Basic passwords alone; nil refuses every credential.
	StateAuth accessapi.Authenticator
}

// Objects are the objects the domains record in blob_objects: those runs'
// workers upload, state's versions and retained VCS bodies; blobs.Objects is one.
type Objects interface {
	runsassembly.Objects
	stateassembly.Objects
	vcsassembly.Objects
	policiesassembly.Objects
}

// newDomains constructs every domain on db, sealing under keys and signing
// tokens through kms under the kms backend, with c's settings; runs'
// workers upload and state writes its versions through objects, nil for a
// test that stores nothing.
// The domains append their events to events, whose kinds newKinds
// registered, and subscribe their purges there, logging to log. It fails
// on a setting a domain reads a file for, and on an outbox whose purges
// are already subscribed.
func newDomains(c config.Config, db *sql.DB, keys *crypt.Keyring, objects Objects, kms []signer.KMS, events *outbox.Outbox, log *slog.Logger) (Domains, error) {
	features := featuresassembly.New(featuresassembly.Deps{
		DB:       db.Primary(),
		Settings: c.Features,
	})
	access, err := accessassembly.New(accessassembly.Deps{
		DB:               db.Primary(),
		Features:         features.Entitlements(),
		Events:           events,
		Keys:             keys,
		KMS:              kms,
		Settings:         c.Auth,
		PublicURL:        c.Server.PublicURL,
		Audience:         strings.TrimSuffix(c.Server.PublicURL, "/") + BaseURL,
		OperatorAudience: strings.TrimSuffix(c.Server.PublicURL, "/") + OperatorBaseURL,
	})
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the setting
	}
	workers := workersassembly.New(workersassembly.Deps{
		PublicPool:       c.Worker.PublicPool,
		DB:               db.Primary(),
		Keys:             keys,
		Spaces:           access.Spaces(),
		Authz:            access.Authorizer(),
		Public:           c.Server.PublicURL,
		Tokens:           access.Tokens(),
		Features:         features.Entitlements(),
		Version:          buildinfo.Version(),
		PollMax:          c.Worker.PollTimeoutMax,
		OfflineAfter:     c.Worker.OfflineAfter,
		LostAfter:        c.Run.LostAfter,
		PreparingTimeout: c.Run.PreparingTimeoutDefault,
		SampleRetention:  c.Worker.Samples.Retention,
		Log:              log,
	})
	access.GuardSpaces(workers.SpaceGuard())
	access.ResolveSubjects(workersassembly.Entity, workers.SpaceOf())
	vendors := vendorsassembly.New(vendorsassembly.Deps{DB: db.Primary(), Settings: c.Vendors})
	stacks := stacksassembly.New(stacksassembly.Deps{
		DB:       db.Primary(),
		Spaces:   access.Spaces(),
		Authz:    access.Authorizer(),
		Bindings: access.Bindings(),
		Grants:   access.Grants(),
		Pools:    workers.Pools(),
		Events:   events,
		Catalog:  vendors.Catalog(),
	})
	workers.GuardPools(stacks.PoolGuard())
	workers.BindStacks(stacks.SpaceOf())
	access.GuardSpaces(stacks.SpaceGuard())
	access.ResolveSubjects(stacksassembly.Entity, stacks.SpaceOf())
	access.ResolveSubjects(stacksassembly.BindingEntity, stacks.BindingSpaceOf())
	configuration := configurationassembly.New(configurationassembly.Deps{
		DB:     db.Primary(),
		Keys:   keys,
		Spaces: access.Spaces(),
		Stacks: stacks.Stacks(),
		Authz:  access.Authorizer(),
		Events: events,
	})
	access.GuardSpaces(configuration.SpaceGuard())
	access.ResolveSubjects(configurationassembly.Entity, configuration.SpaceOf())
	access.ResolveSubjects(
		configurationassembly.AttachmentEntity,
		configuration.AttachmentSpaceOf(),
	)
	stacks.GuardStacks(configuration.StackGuard())
	stacks.BindConfiguration(configuration.Configuration())
	policyRuntime, err := rego.New(c.Policy)
	if err != nil {
		return Domains{}, fmt.Errorf("construct policy runtime: %w", err)
	}
	policies := policiesassembly.New(
		policiesassembly.Deps{
			LoginChanges: access.LoginPolicyChanges(),
			DB:           db.Primary(),
			Spaces:       access.Spaces(),
			Authz:        access.Authorizer(),
			Runtime:      policyRuntime,
			Events:       events,
			Stacks:       stacks.Stacks(),
			Targets:      stacks.AttachmentTargets(),
			Keys:         keys,
			Objects:      objects,
		},
	)
	access.BindLoginEvaluator(policies.LoginEvaluator())
	access.ResolveSubjects(policiesassembly.Entity, policies.SpaceOf())
	access.ResolveSubjects(policiesassembly.AttachmentEntity, policies.AttachmentSpaceOf())
	access.ResolveSubjects(policiesassembly.CaptureEntity, policies.CaptureSpaceOf())
	stacks.BindPolicies(policies.StackPolicies())
	stacks.GuardStacks(policies.StackGuard())
	access.GuardSpaces(policies.SpaceGuard())
	runs := runsassembly.New(runsassembly.Deps{
		PublicLimits:        features.Entitlements(),
		AccountOrganization: access.AccountOrganization,
		PublicPoolEnabled:   c.Worker.PublicPool.Enabled,
		PolicyTaskInputs:    policies.TaskInputBuilder(),
		ClaimSpaces:         access.ClaimSpaces(),
		ClaimStacks:         stacks.ClaimTargets(),
		PolicyIdentities:    policies.Identities(),
		PolicySamples:       policies.SampleRetentions(),
		PolicySampleReader:  policies.SampleReader(),
		PolicyEvaluator:     policies.Evaluator(),
		PolicySelection:     policies.Selection(),
		PolicyInputs:        policies.RunInputBuilder(),
		ReviewMetadata:      access.ReviewMetadata(),
		PolicyStacks:        stacks.PolicySnapshots(),
		PolicyPools:         workers.PoolMetadata(),
		PolicyRoles:         access.StackRoles(),
		PolicySpaces:        access.PolicySpaces(),
		PolicyContexts:      configuration.PolicyContexts(),
		DB:                  db.Primary(),
		Keys:                keys,
		Objects:             objects,
		Spaces:              access.Spaces(),
		Accounts:            access.Accounts(),
		Authz:               access.Authorizer(),
		Tokens:              access.Tokens(),
		Stacks:              stacks.Stacks(),
		Environment:         configuration.Environment(),
		Catalog:             vendors.Catalog(),
		Events:              events,
		Jobs:                queue.New(db.Primary()),
		Settings:            c.Run,
		RuntimeSettings:     c.Runs,
		LogRetention:        c.ObjectStore.Retention.RunLogs,
	})
	stacks.GuardStacks(runs.StackGuard())
	access.GuardSpaces(runs.SpaceGuard())
	access.ResolveSubjects(runsassembly.Entity, runs.SpaceOf())
	access.BindRuns(runs.RunAccess())
	stacks.SetStackStatus(runs.StackStatus())
	stacks.BindDestroyer(runs.StackDestroyer())
	workers.BindScheduler(runs.Scheduler())
	workers.BindPublicAssignments(runs.PublicAssignments())
	workers.BindReports(runs.Reports())
	workers.BindLiveness(runs.Liveness())
	workers.BindSignals(runs.Signals())
	registry, err := expr.NewRegistry(
		c.Expressions,
		slices.Concat(configuration.Surfaces(), runs.Surfaces(), vendors.Surfaces(), policies.Surfaces())...,
	)
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the surface or the setting
	}
	configuration.BindExpressions(registry)
	runs.BindExpressions(registry)
	state := stateassembly.New(stateassembly.Deps{
		DB:        db.Primary(),
		Stacks:    stacks.Stacks(),
		Access:    runs.StateAccess(),
		Runs:      runs.StackRuns(),
		Ends:      runs.RunEnds(),
		Authz:     access.Authorizer(),
		Reader:    access.StateReader(),
		Objects:   objects,
		Jobs:      queue.New(db.Primary()),
		Keys:      keys,
		PublicURL: c.Server.PublicURL,
		URLTTL:    c.ObjectStore.SignedURLTTL,
		Settings:  c.State,
	})
	stacks.GuardStacks(state.StackGuard())
	stacks.BindManagedState(state.ManagedState())
	stacks.BindStateRollbacks(state.StateRollbacks())
	access.ResolveSubjects(stateassembly.Entity, state.SpaceOf())
	vcs, err := vcsassembly.New(vcsassembly.Deps{
		DB:               db.Primary(),
		Keys:             keys,
		Spaces:           access.Spaces(),
		Authz:            access.Authorizer(),
		Stacks:           stacks.Stacks(),
		Histories:        runs,
		PlanningLogs:     runs.PlanningLogs(),
		CommitRuns:       runs.CommitRuns(),
		CheckFacts:       runs.CheckFacts(),
		Settings:         c.VCS,
		Public:           c.Server.PublicURL,
		Log:              log,
		Objects:          objects,
		PushEventTimeout: c.Policy.Timeouts.PushEvent,
		Features:         features.Entitlements(),
		Creator:          runs.Creator(),
		Supersede:        runs.Supersede(),
		Policies:         policies.Evaluator(), PolicyInputs: policies.PushInputBuilder(), PolicyStacks: stacks.PolicySnapshots(),
		PolicyLocks:     stacks.PolicyLocks(),
		PolicySelection: policies.Selection(),
		PolicySpaces:    access.PolicySpaces(), PolicyPools: workers.PoolMetadata(), PolicyRoles: access.StackRoles(),
		PolicyContexts: configuration.PolicyContexts(), PolicyRuns: runs.PushRunReader(), PolicyCancellations: runs.PushCancellations(),
		PolicyAllowed: func(ctx context.Context, account kernel.AccountID) (bool, error) {
			org, err := access.AccountOrganization(ctx, account)
			if err != nil {
				return false, fmt.Errorf("VCS policy account organization: %w", err)
			}
			return features.Allowed(ctx, org, "policies")
		},
	})
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the setting
	}
	access.GuardSpaces(vcs.SpaceGuard())
	stacks.GuardStacks(vcs.StackGuard())
	access.ResolveSubjects(vcsassembly.Entity, vcs.SpaceOf())
	stacks.BindSources(vcs.StackSources())
	runs.BindSources(vcs.RunSources(), vcs.StackSources())
	stacks.BindIntegrations(vcs.StackIntegrations())
	stacks.BindPullRequests(vcs.PullRequests())
	eventSinks, err := eventsassembly.New(eventsassembly.Deps{
		DB: db.Primary(), Feedback: vcs.Feedback(), Settings: c.Events.Deliveries,
	})
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the event setting or instrument
	}
	d := Domains{
		Features:      features,
		Access:        access,
		Workers:       workers,
		Vendors:       vendors,
		Stacks:        stacks,
		Configuration: configuration,
		Policies:      policies,
		Runs:          runs,
		State:         state,
		VCS:           vcs,
		Events:        eventSinks,
		Expressions:   expressionsassembly.New(registry),
		Auth:          access.Authenticator(),
		OperatorAuth:  access.OperatorAuthenticator(),
		WorkerAuth:    access.WorkerAuthenticator(),
		StateAuth:     access.StateAuthenticator(),
	}
	if err := subscribePurges(events, d); err != nil {
		return Domains{}, err
	}
	if err := configuration.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	if err := policies.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	if err := vcs.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	if err := runs.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	if err := state.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	if err := eventSinks.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	return d, nil
}

// Allowed is queue.Entitlements over the domains: a job's account names
// its organization through access, whose entitlement features decides. A
// job of an account that no longer exists is refused only a switched-off
// feature, and its handler finds the account gone.
func (d Domains) Allowed(ctx context.Context, account kernel.ULID, feature string) (bool, error) {
	org, err := d.Access.AccountOrganization(ctx, kernel.AccountID{ULID: account})
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the read
	}
	return d.Features.Allowed(ctx, org, feature) //nolint:wrapcheck // as above
}

var _ queue.Entitlements = Domains{}

// newKinds is every domain's event kinds, which the outbox checks each
// event against.
func newKinds() *outbox.Kinds {
	kinds := outbox.NewKinds()
	// Each kind's schema is a constant, registered once, so never an error.
	_ = accessassembly.RegisterEvents(kinds)
	_ = stacksassembly.RegisterEvents(kinds)
	_ = configurationassembly.RegisterEvents(kinds)
	_ = policiesassembly.RegisterEvents(kinds)
	_ = runsassembly.RegisterEvents(kinds)
	return kinds
}

// cron is every domain's periodic tasks, with the nightly orphan count
// over q, which the scheduler role runs, logging to log.
func (d Domains) cron(q tx.Querier, log *slog.Logger) []cron.Task {
	return slices.Concat(d.Access.Cron(), d.Vendors.Cron(log), d.Workers.Cron(),
		d.Policies.Cron(), d.Runs.Cron(log), d.State.Cron(log), d.VCS.Cron(), d.Events.Cron(), []cron.Task{orphansTask(q, log)})
}
