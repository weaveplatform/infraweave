package integration

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// The GitHub providers: GitHub is the operator's App, which the server's
// settings configure, and GitHub Enterprise an account's own App on
// github.com or a GitHub Enterprise Server.
const (
	GitHub           = "GITHUB"
	GitHubEnterprise = "GITHUB_ENTERPRISE"
)

// ManifestTTL is how long a GitHub App manifest form stays open: the time
// a user has to create the App on its host and come back.
const ManifestTTL = time.Hour

// The hosts a manifest form creates an App on when it names none.
const (
	gitHubHost    = "https://github.com"
	gitHubAPIHost = "https://api.github.com"
)

// Manifest is a GitHub App manifest form an account has opened: its ID,
// which is its state and becomes the created integration's ID, the hosts
// the App is created on, and when it expires.
type Manifest struct {
	ID             kernel.ULID
	APIHost        string
	UserFacingHost string
	ExpiresAt      time.Time
}

// Details are what a caller who administers an integration's space sees
// beyond its fields: its App as its host reports it, for an App
// integration, its webhook secret and the URL its deliveries go to.
type Details struct {
	App           *port.App
	WebhookSecret string
	WebhookURL    string
}

// checkSettings refuses the provider settings in carries that no provider
// can use: a host that is not an https URL, an App ID that is not a
// number, and an App's private key that is not one.
func checkSettings(in Input) []kernel.FieldError {
	var errs []kernel.FieldError
	for field, host := range map[string]string{
		"apiHost": in.Settings.APIHost, "userFacingHost": in.Settings.UserFacingHost,
	} {
		if host != "" && !httpsURL(host) {
			errs = append(errs, kernel.FieldError{
				Pointer: "/" + field, Reason: kernel.ReasonFormat,
				Message: field + " is an https URL with a host and no credential.",
			})
		}
	}
	if id := in.Settings.AppID; id != "" && strings.Trim(id, "0123456789") != "" {
		errs = append(errs, kernel.FieldError{
			Pointer: "/appID", Reason: kernel.ReasonFormat, Message: "appID is a number.",
		})
	}
	if in.Settings.AppID != "" && in.Secrets != nil && in.Secrets.Credential != nil &&
		!privateKey(in.Secrets.Credential) {
		errs = append(errs, kernel.FieldError{
			Pointer: "/privateKey", Reason: kernel.ReasonFormat,
			Message: "privateKey is the App's PEM-encoded RSA private key.",
		})
	}
	return errs
}

// httpsURL reports whether s is an absolute https URL with a host and no
// user information.
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// privateKey reports whether b is a PEM-encoded RSA private key, in the
// PKCS #1 form GitHub issues or in PKCS #8.
func privateKey(b []byte) bool {
	block, _ := pem.Decode(b)
	if block == nil {
		return false
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return true
	}
	_, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	return err == nil
}

// sameSpace refuses slug when it names a space other than space; an empty
// slug keeps it.
func (s *Service) sameSpace(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
	slug string,
) error {
	if slug == "" {
		return nil
	}
	id, err := s.spaces.Find(ctx, acct, slug)
	if err != nil && !errors.Is(err, accessapi.ErrNoSpace) {
		return err //nolint:wrapcheck // the access domain names the read
	}
	if err == nil && id == space {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{
		refusal("space", kernel.ReasonRule, "An integration stays in the space it was made in."),
	}}
}

// WebhookURL is the URL the deliveries of acct's integration id of
// provider go to.
//
// ref: decision 0012 § "Webhook intake: stored before acknowledgement"
func (s *Service) WebhookURL(provider string, id kernel.ULID) string {
	return fmt.Sprintf("%s/webhooks/vcs/%s/%s", s.public, strings.ToLower(provider), id)
}

// Builtin reads the integration of provider whose credentials the
// operator sets, with p's account's check options: those it stored, or
// the defaults until it stores some. ErrNotConfigured when the operator
// has set none.
func (s *Service) Builtin(
	ctx context.Context,
	p kernel.Principal,
	provider string,
) (Integration, error) {
	use, ok := s.builtin[provider]
	if !ok {
		return Integration{}, fmt.Errorf("%w: %s", ErrNotConfigured, provider)
	}
	in, err := s.store.Default(ctx, p.AccountID, provider)
	if errors.Is(err, ErrNotFound) {
		in = Integration{
			Account: p.AccountID, Provider: provider, Space: p.AccountID.ULID,
			IsDefault: true, Labels: []string{}, Checks: Checks{Mode: checkModes[0]},
		}
	} else if err != nil {
		return Integration{}, err //nolint:wrapcheck // the store names the read
	}
	in.Settings = use.Settings
	return in, nil
}

// SetBuiltin stores p's account's check options of the built-in provider
// and whether its runs use a Git checkout, keeping the stored value of
// each that is nil. The route has decided SPACE_ADMIN on the top space.
func (s *Service) SetBuiltin(
	ctx context.Context,
	p kernel.Principal,
	provider string,
	checks Checks,
	useGitCheckout *bool,
) (Integration, error) {
	if _, ok := s.builtin[provider]; !ok {
		return Integration{}, fmt.Errorf("%w: %s", ErrNotConfigured, provider)
	}
	if !slices.Contains(checkModes, checks.Mode) {
		return Integration{}, &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
			Pointer: "/vcsChecks", Reason: kernel.ReasonEnum,
			Message: "vcsChecks is one of INDIVIDUAL, AGGREGATED and ALL.",
		}}}
	}
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		in, err := s.store.Default(ctx, p.AccountID, provider)
		stored := err == nil
		if errors.Is(err, ErrNotFound) {
			now := s.now()
			in = Integration{
				Account: p.AccountID, ID: kernel.NewULID(), Provider: provider,
				Slug: strings.ToLower(provider), Name: provider, Space: p.AccountID.ULID,
				IsDefault: true, Labels: []string{}, CreatedAt: now, UpdatedAt: now,
			}
		} else if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		in.Checks = checks
		if useGitCheckout != nil {
			in.UseGitCheckout = *useGitCheckout
		}
		if !stored {
			in.Version = 1
			return s.store.Create(
				ctx,
				in,
				Secrets{},
			) //nolint:wrapcheck // the store names the write
		}
		in.UpdatedAt = s.now()
		in.Version++
		return s.store.Write(ctx, in, nil) //nolint:wrapcheck // the store names the write
	})
	if err != nil {
		return Integration{}, err //nolint:wrapcheck // each step in the transaction names itself
	}
	return s.Builtin(ctx, p, provider)
}

// OpenManifest opens a manifest form for p's account that creates a
// GitHub App on host, whose API is apiHost, each github.com's when empty,
// owned by the organization owner or by the user when owner is empty. The
// route has decided SPACE_ADMIN on the top space.
//
// ref: decision 0012 § "Integrations, the default and spaces"
func (s *Service) OpenManifest(
	ctx context.Context,
	p kernel.Principal,
	host, apiHost, owner string,
) (port.Manifest, error) {
	if s.apps == nil {
		return port.Manifest{}, fmt.Errorf("%w: GitHub Apps", ErrNotConfigured)
	}
	if host == "" {
		host = gitHubHost
	}
	if apiHost == "" {
		apiHost = gitHubAPIHost
	}
	host, apiHost = strings.TrimSuffix(host, "/"), strings.TrimSuffix(apiHost, "/")
	var errs []kernel.FieldError
	for field, h := range map[string]string{"host": host, "apiHost": apiHost} {
		if !httpsURL(h) {
			errs = append(errs, kernel.FieldError{
				Pointer: "/" + field, Reason: kernel.ReasonFormat,
				Message: field + " is an https URL with a host and no credential.",
			})
		}
	}
	if len(errs) > 0 {
		return port.Manifest{}, &kernel.Invalid{Detail: invalidDetail, Errors: errs}
	}
	m := Manifest{
		ID: kernel.NewULID(), APIHost: apiHost, UserFacingHost: host,
		ExpiresAt: s.now().Add(ManifestTTL),
	}
	if err := s.store.OpenManifest(ctx, p.AccountID, m); err != nil {
		return port.Manifest{}, err //nolint:wrapcheck // the store names the write
	}
	//nolint:wrapcheck // the provider names its refusal
	return s.apps.Manifest(host, owner, s.WebhookURL(GitHubEnterprise, m.ID),
		s.public+"/vcs/github-enterprise/manifest", m.ID.String())
}

// FromManifest completes p's account's manifest form state with code,
// which the App's host redirected back with: it converts the code into
// the App and stores it as a GitHub Enterprise integration in the top
// space, with the form's ID, named after the App, and the default when
// the account has none. A form that is unknown, expired or completed is
// refused at state; the route has decided SPACE_ADMIN on the top space.
func (s *Service) FromManifest(
	ctx context.Context,
	p kernel.Principal,
	code, state string,
) (Integration, error) {
	if s.apps == nil {
		return Integration{}, fmt.Errorf("%w: GitHub Apps", ErrNotConfigured)
	}
	gone := &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
		Pointer: "/state", Reason: kernel.ReasonNotFound,
		Message: "The manifest form has expired, was completed, or was never opened.",
	}}}
	id, err := kernel.ParseULID(state)
	if err != nil {
		return Integration{}, gone
	}
	var m Manifest
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		m, err = s.store.TakeManifest(ctx, p.AccountID, id, s.now())
		return err //nolint:wrapcheck // the store names the write
	})
	if errors.Is(err, ErrNotFound) {
		return Integration{}, gone
	}
	if err != nil {
		return Integration{}, err //nolint:wrapcheck // the store names the write
	}
	app, err := s.apps.Convert(ctx, m.APIHost, code)
	if errors.Is(err, port.ErrNotFound) {
		return Integration{}, &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
			Pointer: "/code", Reason: kernel.ReasonNotFound,
			Message: "The App's host does not know the code.",
		}}}
	}
	if err != nil {
		return Integration{}, err //nolint:wrapcheck // the provider names its failure
	}
	_, err = s.store.Default(ctx, p.AccountID, GitHubEnterprise)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Integration{}, err //nolint:wrapcheck // the store names the read
	}
	return s.create(ctx, p, m.ID, Input{
		Provider: GitHubEnterprise, Name: app.Name, IsDefault: err != nil, Labels: []string{},
		Settings: port.Settings{
			APIHost: m.APIHost, UserFacingHost: m.UserFacingHost, AppID: app.AppID,
		},
		Secrets: &Secrets{Credential: app.PrivateKey, WebhookSecret: app.WebhookSecret},
	})
}

// Details are in's webhook secret and URL, and its App, for an
// integration whose settings name one, as its host reports it now. The
// caller answers the secret and URL only to one who administers in's
// space.
func (s *Service) Details(ctx context.Context, in Integration) (Details, error) {
	use, err := s.use(ctx, in)
	if err != nil {
		return Details{}, err
	}
	d := Details{
		WebhookSecret: string(use.WebhookSecret), WebhookURL: s.WebhookURL(in.Provider, in.ID),
	}
	if in.Settings.AppID != "" && s.apps != nil {
		app := s.apps.App(ctx, use)
		d.App = &app
	}
	return d, nil
}
