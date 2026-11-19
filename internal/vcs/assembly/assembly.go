package assembly

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	vcsobserve "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/observe"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/rawgit"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/ignored"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/pullrequest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// Stream is the domain's migration stream, which internal/app registers
// after configuration's.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the vcs columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// SealedColumns are the vcs domain's sealed columns.
func SealedColumns() []crypt.Column { return pg.SealedColumns() }

// Entity is the kind of entity the access domain's middleware finds a
// space for, as the API's x-infraweave-subject names it: an integration,
// in its own space.
const Entity = "vcs_integration"

// Providers are the providers whose integrations infraweave stores. Raw
// Git has none: a stack on it names its repository by URL.
var Providers = []string{
	"GITHUB_ENTERPRISE", "GITLAB", "BITBUCKET_CLOUD", "BITBUCKET_DATACENTER", "AZURE_DEVOPS",
}

// DB is the pool the domain queries and begins its transactions on.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Settings are the vcs settings: CAFile is a PEM file of certificate
// authorities trusted beside the system's when reaching a repository's
// host, for a host under a private CA; BitbucketCloud is where its API is.
type Settings struct {
	CAFile               string           `mapstructure:"ca_file"`
	BitbucketCloud       BitbucketCloud   `mapstructure:"bitbucket_cloud"`
	Deliveries           DeliverySettings `mapstructure:"deliveries"`
	WebhookSecretOverlap time.Duration    `mapstructure:"webhook_secret_overlap"`
	EventRunsCeiling     int              `mapstructure:"event_runs_ceiling"`
}

// BitbucketCloud is where every BITBUCKET_CLOUD integration reaches
// Bitbucket Cloud: its API host, https://api.bitbucket.org/2.0 when
// empty. An integration names no host of its own, so a server reaching a
// stand-in for Bitbucket Cloud, such as a proxy, sets it here.
type BitbucketCloud struct {
	APIHost string `mapstructure:"api_host"`
}

// ErrConfig reports a vcs setting the server could not start with.
var ErrConfig = errors.New("invalid vcs configuration")

// Deps are what the vcs domain needs of the platform and the domains
// built before it: the database, the keys every integration's secrets are
// sealed under, the spaces an integration lives in, the authorizer, the
// stacks it reads and detaches, the policy for non-default creation, its settings, and the server's public
// origin, which webhook and manifest URLs start with.
type Deps struct {
	DB               DB
	Keys             pg.Sealer
	Spaces           accessapi.Spaces
	Authz            accessapi.Authorizer
	Stacks           stacksapi.Stacks
	Histories        runsapi.RunHistories
	PlanningLogs     runsapi.PlanningLogs
	CommitRuns       runsapi.CommitRuns
	CheckFacts       runsapi.CheckFacts
	Settings         Settings
	Public           string
	Log              *slog.Logger
	Objects          Objects
	Now              func() time.Time
	PushEventTimeout time.Duration
	Features         featuresapi.Options
	Creator          runsapi.Creator
	Supersede        runsapi.Supersede
	// Meter instruments this domain; nil uses its process OpenTelemetry meter.
	Meter metric.Meter
}

// Domain is the vcs domain, constructed once per process.
type Domain struct {
	sources      *source.Service
	integrations *integration.Service
	feedback     *feedback.Service
	api          http.Server
	intake       *intake.Service
	limits       *push.Limits
	push         *push.Service
	ignored      *ignored.Service
	requests     *pullrequest.Service
}

// New constructs the domain over d, reaching raw Git repositories, served
// as the GIT provider, GitHub's, GitLab's, Bitbucket Cloud's, Bitbucket
// Data Center's and Azure DevOps's, over https with the system's roots and those of vcs.ca_file. It fails when vcs.ca_file cannot be read or
// holds no certificate.
func New(d Deps) (*Domain, error) {
	var roots *x509.CertPool
	if f := d.Settings.CAFile; f != "" {
		pem, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("%w: vcs.ca_file: %w", ErrConfig, err)
		}
		if roots, err = x509.SystemCertPool(); err != nil {
			return nil, fmt.Errorf("%w: system roots: %w", ErrConfig, err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%w: vcs.ca_file %s holds no PEM certificate", ErrConfig, f)
		}
	}
	gh := github.New(github.Options{RootCAs: roots})
	gl := gitlab.New(gitlab.Options{RootCAs: roots})
	bbc := bitbucketcloud.New(bitbucketcloud.Options{
		RootCAs: roots, APIHost: d.Settings.BitbucketCloud.APIHost,
	})
	bbdc := bitbucketdc.New(bitbucketdc.Options{RootCAs: roots})
	ado := azuredevops.New(azuredevops.Options{RootCAs: roots})
	git := rawgit.New(rawgit.Options{RootCAs: roots})
	clock := d.Now
	if clock == nil {
		clock = time.Now
	}
	limits := &push.Limits{DB: d.DB, Store: pg.New(d.DB, d.Keys), Spaces: d.Spaces, Ceiling: d.Settings.EventRunsCeiling, Now: clock}
	identities := &lifecycle.Service{DB: d.DB, Store: pg.New(d.DB, d.Keys), Stacks: d.Stacks, Now: clock, Log: d.Log}
	integrations := integration.New(integration.Deps{
		Installations: identities,
		Store:         pg.New(d.DB, d.Keys), DB: d.DB, Spaces: d.Spaces, Authz: d.Authz,
		Stacks: d.Stacks, Providers: Providers, Features: d.Features,
		Testers: map[string]port.Tester{
			integration.GitHub: gh, integration.GitHubEnterprise: gh, integration.GitLab: gl,
			integration.BitbucketCloud: bbc, integration.BitbucketDC: bbdc,
			integration.AzureDevOps: ado,
		},
		Browse: map[string]port.Brancher{
			integration.Git: git, integration.GitHub: gh, integration.GitHubEnterprise: gh,
			integration.GitLab: gl, integration.BitbucketCloud: bbc,
			integration.BitbucketDC: bbdc, integration.AzureDevOps: ado,
		},
		Apps: gh, Public: d.Public, Now: d.Now, SecretOverlap: d.Settings.WebhookSecretOverlap,
	})
	sources := source.New(source.Deps{
		DB:         d.DB,
		Identities: identities,
		Stacks:     d.Stacks,
		Providers: map[string]port.Source{
			integration.Git:              git,
			integration.GitHub:           gh,
			integration.GitHubEnterprise: gh,
			integration.GitLab:           gl,
			integration.BitbucketCloud:   bbc,
			integration.BitbucketDC:      bbdc,
			integration.AzureDevOps:      ado,
		},
		Integrations: integrations,
		Tokens:       pg.NewTokens(d.DB),
		Jobs:         queue.New(d.DB),
		Now:          clock,
		Log:          d.Log,
	})
	ignoredTriggers := ignored.New(ignored.Deps{Store: pg.New(d.DB, d.Keys), Stacks: d.Stacks, Authz: d.Authz, Now: clock})
	checks := feedback.NewDeployments(queue.New(d.DB), func(ctx context.Context, acct kernel.AccountID) error {
		if d.Spaces == nil {
			return accessapi.ErrNoAccount
		}
		return d.Spaces.Hold(ctx, acct, acct.ULID)
	}, feedback.DeploymentDeps{
		DB: d.DB, Store: pg.New(d.DB, d.Keys), Histories: d.Histories,
		Stacks: d.Stacks, Integrations: integrations,
		Providers: map[string]port.Deployments{
			integration.GitHub: gh, integration.GitHubEnterprise: gh, integration.GitLab: gl,
		},
	}).WithChecks(feedback.CheckDeps{
		DB:           d.DB,
		Store:        pg.New(d.DB, d.Keys),
		Facts:        d.CheckFacts,
		Histories:    d.Histories,
		Stacks:       d.Stacks,
		Integrations: integrations,
		Public:       d.Public,
		Now:          clock,
		Log:          d.Log,
		Providers: map[string]feedback.CheckProviders{
			integration.GitHub:           gh,
			integration.GitHubEnterprise: gh,
			integration.GitLab:           gl,
			integration.BitbucketCloud:   bbc,
			integration.BitbucketDC:      bbdc,
			integration.AzureDevOps:      ado,
		},
	})
	checks.WithComments(feedback.CommentDeps{
		DB: d.DB, Store: pg.New(d.DB, d.Keys), Histories: d.Histories, Logs: d.PlanningLogs,
		Stacks: d.Stacks, Integrations: integrations, Public: d.Public,
		Providers: map[string]port.Comments{
			integration.GitHub: gh, integration.GitHubEnterprise: gh, integration.GitLab: gl,
			integration.BitbucketCloud: bbc, integration.BitbucketDC: bbdc, integration.AzureDevOps: ado,
		},
	})
	actionRefused, err := vcsobserve.CheckActionRefused(domainMeter(d))
	if err != nil {
		return nil, fmt.Errorf("vcs: action counter: %w", err)
	}
	pushes := push.New(push.Deps{
		Lifecycle: identities,
		DB:        d.DB, Store: pg.New(d.DB, d.Keys), Stacks: d.Stacks, Spaces: d.Spaces,
		Integrations: integrations, Sources: sources, Actions: sources, Effects: pg.New(d.DB, d.Keys), ActionRefused: actionRefused, Creator: d.Creator, Supersede: d.Supersede,
		Jobs: queue.New(d.DB), Limits: limits, Checks: checks, Now: clock, Log: d.Log, Ignored: ignoredTriggers,
	})
	requests := pullrequest.New(pullrequest.Deps{
		Store: pg.New(d.DB, d.Keys), Integrations: integrations,
		Authors: map[string]pullrequest.Authors{integration.GitLab: gl}, Runs: d.CommitRuns, Pushes: pushes,
		Heads: map[string]pullrequest.Heads{integration.BitbucketCloud: bbc},
		Now:   clock, Retention: d.Settings.Deliveries.Retention,
	})
	inbox, err := newIntake(d, requests, map[string]any{
		integration.GitHubEnterprise: gh, integration.GitLab: gl,
		integration.BitbucketCloud: bbc, integration.BitbucketDC: bbdc, integration.AzureDevOps: ado,
	})
	if err != nil {
		return nil, err
	}
	return &Domain{
		sources:      sources,
		push:         pushes,
		requests:     requests,
		integrations: integrations,
		intake:       inbox,
		limits:       limits,
		ignored:      ignoredTriggers,
		feedback:     checks,
		api:          http.New(integrations, d.Spaces, nil, limits).WithDeliveries(inbox).WithIgnored(ignoredTriggers),
	}, nil
}

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.integrations.SpaceGuard() }

// SpaceOf finds the space of an integration by its ID, which internal/app
// registers for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.integrations.SpaceOf }

// StackIntegrations checks the integration a stack's write names, the
// back edge internal/app binds in the stacks domain.
func (d *Domain) StackIntegrations() stacksapi.StackIntegrations { return d.integrations }

// PurgeBatch is how many integrations one statement of Purge deletes.
const PurgeBatch = 500

// Purge deletes the deleted account acct's integrations, PurgeBatch at a
// time. internal/app runs it for each accessapi.Deleted.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		done, err := d.feedback.PurgeComments(ctx, acct, PurgeBatch)
		if err != nil {
			return err //nolint:wrapcheck // the store names the purge
		}
		if done {
			break
		}
	}
	for {
		done, err := d.feedback.PurgeDeployments(ctx, acct, PurgeBatch)
		if err != nil {
			return err //nolint:wrapcheck // the store names the purge
		}
		checksDone, err := d.feedback.PurgeChecks(ctx, acct, PurgeBatch)
		if err != nil {
			return fmt.Errorf("VCS check purge: %w", err)
		}
		if done && checksDone {
			break
		}
	}
	for {
		done, err := d.integrations.Purge(ctx, acct, PurgeBatch)
		if err != nil {
			return err //nolint:wrapcheck // the store names the purge
		}
		if done {
			if err := d.purgeDeliveries(ctx, acct); err != nil {
				return err
			}
			if d.requests != nil {
				if err := d.requests.Purge(ctx, acct); err != nil {
					return err //nolint:wrapcheck // the request service names the failed purge
				}
			}
			if d.ignored != nil {
				if err := d.ignored.Purge(ctx, acct); err != nil {
					return fmt.Errorf("vcs: purge ignored history: %w", err)
				}
			}
			return d.limits.Store.PurgeRunLimit(ctx, acct) //nolint:wrapcheck // the VCS setting store names the purge
		}
	}
}

// StackGuard removes stack-owned ignored records in the deletion transaction.
func (d *Domain) StackGuard() stacksapi.StackGuard { return stackGuard{d.ignored, d.feedback} }

type stackGuard []stacksapi.StackGuard

func (g stackGuard) LostOnMove(ctx context.Context, m stacksapi.StackMove) ([]string, error) {
	var out []string
	for _, guard := range g {
		rows, err := guard.LostOnMove(ctx, m)
		if err != nil {
			return nil, fmt.Errorf("VCS stack move: %w", err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (g stackGuard) Deleting(ctx context.Context, st stacksapi.Stack) error {
	for _, guard := range g {
		if err := guard.Deleting(ctx, st); err != nil {
			return fmt.Errorf("VCS stack deletion: %w", err)
		}
	}
	return nil
}

// Sources resolves the commits of a stack's repository for other domains.
func (d *Domain) Sources() vcsapi.Sources { return d.sources }

// PullRequests supplies the stacks-owned request history back edge.
func (d *Domain) PullRequests() stacksapi.PullRequestReader { return d.requests }

// RunSources answers a run's source at its handover and releases its
// credentials at its end, the runsapi.RunSources internal/app binds in
// the runs domain.
func (d *Domain) RunSources() runsapi.RunSources { return source.Runs{Service: d.sources} }

// StackSources resolves a stack's refs for the stacks domain's commit
// operations, which internal/app binds there.
func (d *Domain) StackSources() stacksapi.StackSources { return d.sources.Resolver() }

// Track is the consumer that sets a stack's branch head and tracked
// commit when a stacksapi.Changed gives the stack its source or changes
// it; every replica subscribes it, and the outbox runs each event once.
const Track = "vcs.track"

// Revoke is the job that revokes a run's minted tokens once
// vcsapi.Sources.Release enqueues it.
var Revoke = source.Revoke

// Jobs are the domain's job handlers, which the scheduler registers:
// Revoke's.
func (d *Domain) Jobs() []queue.Registration {
	return []queue.Registration{
		{Kind: Revoke, Handler: d.sources.Revoked},
		d.deliveryJob(),
		{Kind: feedback.ReportDeployment, Handler: d.feedback.Reported},
		{Kind: feedback.ReportCheck, Handler: d.feedback.Checked},
		{Kind: feedback.ReportComment, Handler: d.feedback.Commented},
		{
			Kind: push.Evaluate, Handler: d.push.Evaluated,
			Options: queue.HandlerOptions{Feature: "vcs-integrations"},
		},
	}
}

// Subscribe subscribes the domain's consumers on events.
func (d *Domain) Subscribe(events *outbox.Outbox) error {
	//nolint:wrapcheck // names the consumer
	return events.Subscribe(stacksapi.ChangedKind, Track, stackChanged(d.sources.Track))
}

// stackChanged is the outbox handler that decodes a stacksapi.Changed and
// runs track on its stack when the change touched its source; a document
// that does not decode never will, so its job dies at once.
func stackChanged(track func(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error) outbox.Handler {
	return func(ctx context.Context, ev outbox.Event, _ queue.Job) error {
		var ch stacksapi.Changed
		if err := json.Unmarshal(ev.Document, &ch); err != nil {
			return queue.Permanent( //nolint:wrapcheck // marks it
				fmt.Errorf("%s %s: %w", ev.Kind, ev.ID, err),
			)
		}
		if !ch.Source {
			return nil
		}
		return track(ctx, kernel.AccountID{ULID: ev.Account}, ch.Stack)
	}
}

// VCSAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type VCSAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() VCSAPI {
	if d == nil {
		return VCSAPI{}
	}
	return d.api
}

func domainMeter(d Deps) metric.Meter {
	if d.Meter != nil {
		return d.Meter
	}
	return otel.Meter("infraweave/vcs")
}
