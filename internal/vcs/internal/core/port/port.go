package port

import (
	"context"
	"errors"
	"fmt"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

var (
	// ErrNotFound reports a repository, ref or path a Source cannot find.
	ErrNotFound = errors.New("vcs: not found")
	// ErrRepositoryURL reports a repository URL a Source refuses to reach,
	// such as one that is not https or that carries a credential.
	ErrRepositoryURL = errors.New("vcs: repository URL refused")
	// ErrTooLarge reports a file larger than ReadFile returns.
	ErrTooLarge = errors.New("vcs: file too large")
	// ErrNoIntegration reports a repository a Source reaches only through
	// an integration, when the stack names none and its provider has no
	// default.
	ErrNoIntegration = errors.New("vcs: no integration")
)

// RateLimited reports a provider call its host refused for its rate limit,
// which lifts at Reset. A caller reschedules for Reset rather than
// retrying.
//
// ref: decision 0012 § "Provider abstraction"
type RateLimited struct{ Reset time.Time }

func (e *RateLimited) Error() string {
	return fmt.Sprintf("vcs: rate limited until %s", e.Reset.UTC().Format(time.RFC3339))
}

// MaxFileSize is the largest file ReadFile returns.
const MaxFileSize = 1 << 20

// Repository is a stack's repository as a Source reaches it: its
// namespace and name, URL, the repository URL the stack names, which a
// provider without an API, such as raw Git, needs to reach it, and the
// integration the stack uses, nil when it uses none.
type Repository struct {
	Namespace   string
	Name        string
	URL         string
	Integration *Integration
}

// Source reads a provider's repositories. Every provider implements it;
// the server checks capabilities, not provider names. A provider it cannot
// reach is kernel.ErrUnavailable, and a repository URL it refuses is
// ErrRepositoryURL.
//
// ref: decision 0012
type Source interface {
	// ResolveRef resolves ref, a branch, a tag or a full commit SHA, of
	// repo to its commit; ErrNotFound when repo or ref is missing.
	ResolveRef(ctx context.Context, repo Repository, ref string) (vcsapi.Commit, error)
	// ReadFile reads path at ref of repo; ErrNotFound when any of them is
	// missing, and ErrTooLarge when the file exceeds MaxFileSize.
	ReadFile(ctx context.Context, repo Repository, ref, path string) ([]byte, error)
	// AffectedFiles lists the paths that differ between the commits base
	// and head of repo; ErrNotFound when either is missing.
	AffectedFiles(ctx context.Context, repo Repository, base, head string) ([]string, error)
	// SourceURL is the URL a worker fetches commit of repo from.
	SourceURL(repo Repository, commit string) string
}

// Settings are an integration's provider settings, each empty when its
// provider has none: the API and user-facing hosts, the user and e-mail
// a token belongs to, an Azure DevOps organization's URL and the projects
// of it the integration reaches, every one when empty, and a GitHub App's
// ID.
type Settings struct {
	APIHost            string   `json:"apiHost,omitempty"`
	UserFacingHost     string   `json:"userFacingHost,omitempty"`
	Username           string   `json:"username,omitempty"`
	Email              string   `json:"email,omitempty"`
	OrganizationURL    string   `json:"organizationUrl,omitempty"`
	AccessibleProjects []string `json:"accessibleProjects,omitempty"`
	AppID              string   `json:"appId,omitempty"`
}

// Integration is a VCS integration as a provider's client uses it, with
// its credential and webhook secret opened. Nothing but the provider's
// client sees them.
type Integration struct {
	Provider      string
	Settings      Settings
	Credential    []byte
	WebhookSecret []byte
}

// The kinds of check a Tester runs.
const (
	CheckConnection      = "CONNECTION"
	CheckPermissions     = "PERMISSIONS"
	CheckWebhook         = "WEBHOOK"
	CheckGithubAppEvents = "GITHUB_APP_EVENTS"
)

// Check is one check of a test: its kind, whether it passed, and why not.
type Check struct {
	Type   string
	Passed bool
	Reason string
}

// Tester checks that an integration reaches its provider with the
// permissions infraweave needs. A provider it cannot reach is a failed
// CheckConnection, not an error.
//
// ref: decision 0012
type Tester interface {
	Test(ctx context.Context, in Integration) ([]Check, error)
}

// Listed is a repository an integration reaches, by namespace and name.
type Listed struct {
	Namespace string
	Name      string
}

// Browse lists what an integration reaches, for a user choosing a stack's
// repository and branch.
//
// ref: decision 0012
type Browse interface {
	// Repositories lists the repositories in reaches, sorted by namespace
	// and name.
	Repositories(ctx context.Context, in Integration) ([]Listed, error)
	// Branches lists repo's branches, sorted; ErrNotFound when repo is
	// missing.
	Branches(ctx context.Context, repo Repository) ([]string, error)
}

// The states of a GitHub App as its host reports it.
const (
	AppOK             = "OK"
	AppNotFound       = "APP_NOT_FOUND"
	AppCouldNotDecode = "COULD_NOT_DECODE"
	AppError          = "ERROR"
)

// App is a GitHub App as its host reports it: its state, the URL of its
// page, and where it is installed. A host that cannot be reached, or does
// not know the App, is a state, not an error.
type App struct {
	Status        string
	URL           string
	Installations []Installation
}

// Installation is one installation of a GitHub App on an account.
type Installation struct {
	ID          int64
	Account     string
	AccountType string
	URL         string
}

// Manifest is a GitHub App manifest form: the URL a browser posts it to
// and the manifest itself.
type Manifest struct {
	Endpoint string
	Manifest string
}

// Created is a GitHub App its host created from a manifest, with its
// name, private key and webhook secret.
type Created struct {
	Name          string
	AppID         string
	PrivateKey    []byte
	WebhookSecret []byte
}

// Apps is what the GitHub provider does with GitHub Apps beyond reading
// repositories: report an App, write the manifest form that creates one,
// and convert the code its host redirects back with into the App.
//
// ref: decision 0012 § "Integrations, the default and spaces"
type Apps interface {
	// App reports in's App.
	App(ctx context.Context, in Integration) App
	// Manifest writes the form that creates an App on host, owned by the
	// organization owner or by the user when owner is empty, sending its
	// deliveries to webhookURL and its browser back to redirectURL with
	// state.
	Manifest(host, owner, webhookURL, redirectURL, state string) (Manifest, error)
	// Convert turns code, which apiHost redirected back with, into the
	// App it created; ErrNotFound when apiHost does not know the code.
	Convert(ctx context.Context, apiHost, code string) (Created, error)
}
