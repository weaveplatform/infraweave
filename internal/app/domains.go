package app

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	configurationassembly "github.com/weaveplatform/infraweave/internal/configuration/assembly"
	expressionsassembly "github.com/weaveplatform/infraweave/internal/expressions/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsassembly "github.com/weaveplatform/infraweave/internal/runs/assembly"
	stacksassembly "github.com/weaveplatform/infraweave/internal/stacks/assembly"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
	vendorsassembly "github.com/weaveplatform/infraweave/internal/vendors/assembly"
)

// Domains are the business domains, constructed in the declared graph's
// order (repo-layout.md, "Declared graph"), so each is built after every
// domain whose api/ it imports. A back edge, a call to a domain built
// later, is bound here after construction.
type Domains struct {
	Features      *featuresassembly.Domain
	Access        *accessassembly.Domain
	Vendors       *vendorsassembly.Domain
	Stacks        *stacksassembly.Domain
	Configuration *configurationassembly.Domain
	Runs          *runsassembly.Domain
	VCS           *vcsassembly.Domain
	Expressions   *expressionsassembly.Domain
	// Auth authenticates the API's requests; nil refuses every credential.
	Auth accessapi.Authenticator
	// OperatorAuth authenticates the operator API's requests, admitting
	// operator principals alone; nil refuses every credential.
	OperatorAuth accessapi.Authenticator
}

// newDomains constructs every domain on db, sealing under keys and signing
// tokens through kms under the kms backend, with c's settings; the domains
// append their events to events, whose kinds newKinds registered, and
// subscribe their purges there. It fails on a setting a domain reads a file
// for, and on an outbox whose purges are already subscribed.
func newDomains(
	c config.Config,
	db *sql.DB,
	keys *crypt.Keyring,
	kms []signer.KMS,
	events *outbox.Outbox,
) (Domains, error) {
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
	vendors := vendorsassembly.New(vendorsassembly.Deps{DB: db.Primary(), Settings: c.Vendors})
	stacks := stacksassembly.New(stacksassembly.Deps{
		DB:       db.Primary(),
		Spaces:   access.Spaces(),
		Authz:    access.Authorizer(),
		Bindings: access.Bindings(),
		Grants:   access.Grants(),
		Events:   events,
	})
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
	runs := runsassembly.New(runsassembly.Deps{DB: db.Primary()})
	stacks.GuardStacks(runs.StackGuard())
	stacks.SetStackStatus(runs.StackStatus())
	registry, err := expr.NewRegistry(c.Expressions, configuration.Surfaces()...)
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the surface or the setting
	}
	configuration.BindExpressions(registry)
	vcs, err := vcsassembly.New(vcsassembly.Deps{Stacks: stacks.Stacks(), Settings: c.VCS})
	if err != nil {
		return Domains{}, err //nolint:wrapcheck // names the setting
	}
	stacks.BindSources(vcs.StackSources())
	d := Domains{
		Features:      features,
		Access:        access,
		Vendors:       vendors,
		Stacks:        stacks,
		Configuration: configuration,
		Runs:          runs,
		VCS:           vcs,
		Expressions:   expressionsassembly.New(registry),
		Auth:          access.Authenticator(),
		OperatorAuth:  access.OperatorAuthenticator(),
	}
	if err := subscribePurges(events, d); err != nil {
		return Domains{}, err
	}
	if err := configuration.Subscribe(events); err != nil {
		return Domains{}, err //nolint:wrapcheck // names the consumer
	}
	if err := vcs.Subscribe(events); err != nil {
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
	return kinds
}

// cron is every domain's periodic tasks, with the nightly orphan count
// over q, which the scheduler role runs, logging to log.
func (d Domains) cron(q tx.Querier, log *slog.Logger) []cron.Task {
	return slices.Concat(d.Access.Cron(), d.Vendors.Cron(log), []cron.Task{orphansTask(q, log)})
}
