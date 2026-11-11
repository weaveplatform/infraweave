package assembly

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/rawgit"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
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
// host, for a host under a private CA; GitHubApp the operator's GitHub
// App, which the GITHUB provider is; and BitbucketCloud where Bitbucket
// Cloud's API is.
type Settings struct {
	CAFile         string         `mapstructure:"ca_file"`
	GitHubApp      GitHubApp      `mapstructure:"github_app"`
	BitbucketCloud BitbucketCloud `mapstructure:"bitbucket_cloud"`
}

// BitbucketCloud is where every BITBUCKET_CLOUD integration reaches
// Bitbucket Cloud: its API host, https://api.bitbucket.org/2.0 when
// empty. An integration names no host of its own, so a server reaching a
// stand-in for Bitbucket Cloud, such as a proxy, sets it here.
type BitbucketCloud struct {
	APIHost string `mapstructure:"api_host"`
}

// GitHubApp is the GitHub App the operator registers for the GITHUB
// provider: its ID, the files holding its private key and webhook secret,
// and its API host, https://api.github.com when empty. With no ID the
// GITHUB provider answers 503.
//
// ref: decision 0012 § "Integrations, the default and spaces"
type GitHubApp struct {
	AppID             string `mapstructure:"app_id"`
	PrivateKeyFile    string `mapstructure:"private_key_file"`
	WebhookSecretFile string `mapstructure:"webhook_secret_file"`
	APIHost           string `mapstructure:"api_host"`
}

// ErrConfig reports a vcs setting the server could not start with.
var ErrConfig = errors.New("invalid vcs configuration")

// Deps are what the vcs domain needs of the platform and the domains
// built before it: the database, the keys every integration's secrets are
// sealed under, the spaces an integration lives in, the authorizer, the
// stacks it reads and detaches, its settings, and the server's public
// origin, which webhook and manifest URLs start with.
type Deps struct {
	DB       DB
	Keys     pg.Sealer
	Spaces   accessapi.Spaces
	Authz    accessapi.Authorizer
	Stacks   stacksapi.Stacks
	Settings Settings
	Public   string
}

// Domain is the vcs domain, constructed once per process.
type Domain struct {
	sources      *source.Service
	integrations *integration.Service
	api          http.Server
}

// New constructs the domain over d, reaching raw Git repositories, served
// as the GIT provider, GitHub's, GitLab's, Bitbucket Cloud's, Bitbucket
// Data Center's and Azure DevOps's, over https with the system's roots and those of vcs.ca_file. It fails when vcs.ca_file cannot be read or
// holds no certificate, and when vcs.github_app names an App whose files
// cannot be read.
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
	builtin, err := operatorApp(d.Settings.GitHubApp)
	if err != nil {
		return nil, err
	}
	gh := github.New(github.Options{RootCAs: roots})
	gl := gitlab.New(gitlab.Options{RootCAs: roots})
	bbc := bitbucketcloud.New(bitbucketcloud.Options{
		RootCAs: roots, APIHost: d.Settings.BitbucketCloud.APIHost,
	})
	bbdc := bitbucketdc.New(bitbucketdc.Options{RootCAs: roots})
	ado := azuredevops.New(azuredevops.Options{RootCAs: roots})
	git := rawgit.New(rawgit.Options{RootCAs: roots})
	integrations := integration.New(integration.Deps{
		Store: pg.New(d.DB, d.Keys), DB: d.DB, Spaces: d.Spaces, Authz: d.Authz,
		Stacks: d.Stacks, Providers: Providers,
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
		Builtin: builtin, Apps: gh, Public: d.Public,
	})
	return &Domain{
		sources: source.New(d.Stacks, map[string]port.Source{
			integration.Git:              git,
			integration.GitHub:           gh,
			integration.GitHubEnterprise: gh,
			integration.GitLab:           gl,
			integration.BitbucketCloud:   bbc,
			integration.BitbucketDC:      bbdc,
			integration.AzureDevOps:      ado,
		}, integrations),
		integrations: integrations,
		api:          http.New(integrations, d.Spaces, nil),
	}, nil
}

// operatorApp is the GITHUB provider's integration from the operator's
// App settings, none when they name no App.
func operatorApp(a GitHubApp) (map[string]port.Integration, error) {
	if a.AppID == "" {
		return map[string]port.Integration{}, nil
	}
	key, err := os.ReadFile(a.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("%w: vcs.github_app.private_key_file: %w", ErrConfig, err)
	}
	var secret []byte
	if f := a.WebhookSecretFile; f != "" {
		if secret, err = os.ReadFile(f); err != nil {
			return nil, fmt.Errorf("%w: vcs.github_app.webhook_secret_file: %w", ErrConfig, err)
		}
	}
	host := a.APIHost
	if host == "" {
		host = "https://api.github.com"
	}
	return map[string]port.Integration{integration.GitHub: {
		Provider: integration.GitHub, Credential: key, WebhookSecret: bytes.TrimSpace(secret),
		Settings: port.Settings{APIHost: host, UserFacingHost: host, AppID: a.AppID},
	}}, nil
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
		done, err := d.integrations.Purge(ctx, acct, PurgeBatch)
		if err != nil || done {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}

// Sources resolves the commits of a stack's repository for other domains.
func (d *Domain) Sources() vcsapi.Sources { return d.sources }

// StackSources resolves a stack's refs for the stacks domain's commit
// operations, which internal/app binds there.
func (d *Domain) StackSources() stacksapi.StackSources { return d.sources.Resolver() }

// Track is the consumer that sets a stack's branch head and tracked
// commit when a stacksapi.Changed gives the stack its source or changes
// it; every replica subscribes it, and the outbox runs each event once.
const Track = "vcs.track"

// Subscribe subscribes the domain's consumers on events.
func (d *Domain) Subscribe(events *outbox.Outbox) error {
	//nolint:wrapcheck // names the consumer
	return events.Subscribe(stacksapi.ChangedKind, Track, stackChanged(d.sources.Track))
}

// stackChanged is the outbox handler that decodes a stacksapi.Changed and
// runs track on its stack when the change touched its source; a document
// that does not decode never will, so its job dies at once.
func stackChanged(
	track func(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error,
) outbox.Handler {
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
