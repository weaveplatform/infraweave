package assembly

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/rawgit"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// Settings are the vcs settings: CAFile is a PEM file of certificate
// authorities trusted beside the system's when reaching a repository's
// host, for a host under a private CA.
type Settings struct {
	CAFile string `mapstructure:"ca_file"`
}

// ErrConfig reports a vcs setting the server could not start with.
var ErrConfig = errors.New("invalid vcs configuration")

// Deps are what the vcs domain needs of the domains built before it.
type Deps struct {
	Stacks   stacksapi.Stacks
	Settings Settings
}

// Domain is the vcs domain, constructed once per process.
type Domain struct {
	sources *source.Service
}

// New constructs the domain over d, reading raw Git repositories, served as
// the GIT provider, over https with the system's roots and those of
// vcs.ca_file. It fails only when vcs.ca_file cannot be read or holds no
// certificate.
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
	return &Domain{sources: source.New(d.Stacks, map[string]port.Source{
		"GIT": rawgit.New(rawgit.Options{RootCAs: roots}),
	})}, nil
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
func (d *Domain) API() VCSAPI { return VCSAPI{} }
