package assembly

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sts"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/aws"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/http"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/pg"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Stream is the cloud domain's migration stream.
func Stream() migrate.Stream { return pg.Stream() }

// Held are the cloud columns that hold other domains' rows, for the
// nightly orphan count.
func Held() []integrity.Held { return pg.Held() }

// The kinds of entity the access domain's middleware finds a space for,
// as the API's x-infraweave-subject names them: an AWS integration, in its
// own space, and an attachment, in its stack's.
const (
	Entity           = "aws_integration"
	AttachmentEntity = "aws_integration_attachment"
)

// Feature is the capability that gates AWS integrations.
const Feature = "cloud-integrations"

// Settings are the cloud domain's: cloud.aws, where the server reaches
// AWS STS.
type Settings struct {
	AWS aws.Settings `mapstructure:"aws"`
}

// ErrConfig reports a cloud setting the server could not start with.
var ErrConfig = errors.New("invalid cloud configuration")

// Validate refuses a cloud.aws.sts_endpoint that is not an absolute http
// or https URL, which every STS call would fail on.
func (s Settings) Validate() error {
	e := s.AWS.STSEndpoint
	if e == "" {
		return nil
	}
	u, err := url.Parse(e)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: cloud.aws.sts_endpoint %q is not an http or https URL", ErrConfig, e)
	}
	return nil
}

// Deps are what the cloud domain takes from the platform and the domains
// below it: the database, the spaces an integration lives in and holds,
// the accounts whose slugs external IDs start with, the stacks it
// attaches to and holds, the authorizer, the feature switches with the
// organization each account belongs to, its settings and, for a test,
// the STS client's options.
type Deps struct {
	DB           pg.DB
	Spaces       accessapi.Spaces
	Accounts     accessapi.Accounts
	Stacks       stacksapi.Stacks
	Authz        accessapi.Authorizer
	Features     featuresapi.Options
	Organization func(ctx context.Context, acct kernel.AccountID) (kernel.OrganizationID, error)
	Settings     Settings
	STS          []func(*sts.Options)
	Now          func() time.Time
}

// Domain is the cloud domain, constructed once per process.
type Domain struct {
	svc   *core.Service
	store *pg.Store
	api   http.Server
}

// New constructs the domain on d.
func New(d Deps) *Domain {
	store := pg.New(d.DB)
	svc := core.New(core.Deps{
		Store: store, DB: d.DB, Spaces: d.Spaces, Accounts: d.Accounts, Stacks: d.Stacks,
		Authz: d.Authz, Roles: roles{aws.New(d.Settings.AWS, d.STS...)},
		Enabled: enabled(d.Features, d.Organization), Now: d.Now,
	})
	return &Domain{svc: svc, store: store, api: http.New(svc, d.Spaces, d.Stacks)}
}

// enabled reports whether an account may use AWS integrations: whether
// its organization's entitlement, the installation's switches and the
// account's preview opt-in admit Feature.
func enabled(features featuresapi.Options, org func(context.Context, kernel.AccountID) (kernel.OrganizationID, error)) func(context.Context, kernel.AccountID) (bool, error) {
	return func(ctx context.Context, acct kernel.AccountID) (bool, error) {
		o, err := org(ctx, acct)
		if err != nil {
			return false, fmt.Errorf("cloud: the organization of account %s: %w", acct, err)
		}
		err = features.AllowAccount(ctx, o, acct, Feature)
		if _, refused := errors.AsType[*featuresapi.Refusal](err); refused {
			return false, nil
		}
		return err == nil, err //nolint:wrapcheck // the features domain names the read
	}
}

// roles is the STS client as the service's core.Roles.
type roles struct{ c *aws.Client }

// CallerAccount answers core.ErrUnsupported without AWS credentials and a
// *core.Upstream for any other STS failure.
func (r roles) CallerAccount(ctx context.Context) (string, error) {
	account, err := r.c.CallerAccount(ctx)
	return account, mapped(err)
}

// Assume assumes a's role and discards the credentials.
func (r roles) Assume(ctx context.Context, a core.Assumption) error {
	_, err := r.c.AssumeRole(ctx, aws.AssumeInput{
		RoleARN: a.RoleARN, ExternalID: a.ExternalID, SessionName: a.SessionName,
		Duration: a.Duration, Region: a.Region,
	})
	return mapped(err)
}

// mapped is the client's err as the service's refusals.
func mapped(err error) error {
	var refused *aws.Refused
	switch {
	case err == nil:
		return nil
	case errors.Is(err, aws.ErrNoCredentials):
		return fmt.Errorf("%w: %w", core.ErrUnsupported, err)
	case errors.As(err, &refused):
		return &core.Refused{Message: refused.Message}
	}
	return &core.Upstream{Code: aws.Code(err), Err: err}
}

// PurgeBatch is how many rows one statement of Purge deletes, so no
// transaction holds an account's every integration at once.
const PurgeBatch = 500

// Purge deletes the deleted account acct's integrations and attachments,
// PurgeBatch rows at a time. internal/app runs it for each
// accessapi.Deleted; a purge stopped part way keeps what it deleted, and
// running it again finishes.
func (d *Domain) Purge(ctx context.Context, acct kernel.AccountID) error {
	for {
		done, err := d.store.Purge(ctx, acct, PurgeBatch)
		if err != nil || done {
			return err //nolint:wrapcheck // the store names the purge
		}
	}
}

// SpaceGuard is the domain's say in the access domain's space deletes and
// moves, which internal/app registers there.
func (d *Domain) SpaceGuard() accessapi.SpaceGuard { return d.svc.SpaceGuard() }

// StackGuard is the domain's say in the stacks domain's stack deletes and
// moves, which internal/app registers there.
func (d *Domain) StackGuard() stacksapi.StackGuard { return d.svc.StackGuard() }

// SpaceOf finds an integration's space by its ID, which internal/app
// registers with the access domain for Entity.
func (d *Domain) SpaceOf() accessapi.SpaceOf { return d.svc.SpaceOf }

// AttachmentSpaceOf finds the space of an attachment's stack by the
// attachment's ID, which internal/app registers for AttachmentEntity.
func (d *Domain) AttachmentSpaceOf() accessapi.SpaceOf { return d.svc.AttachmentSpaceOf }

// CloudAPI is the domain's partial of the API's strict server, which
// internal/app embeds under this alias.
type CloudAPI = http.Server

// API is the domain's partial of the API's strict server; a nil domain's
// is the zero partial, for tests that construct none.
func (d *Domain) API() CloudAPI {
	if d == nil {
		return CloudAPI{}
	}
	return d.api
}
