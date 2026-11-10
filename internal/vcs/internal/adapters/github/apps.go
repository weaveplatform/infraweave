package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// permission is one permission the App needs, at least at level, which
// read or write is, write implying read.
type permission struct {
	name, level string
	get         func(*github.InstallationPermissions) *string
}

// permissions are what infraweave needs of an App: reading a repository's
// source, and writing its checks, pull request comments and commit
// statuses. The manifest asks for them and Test checks them, so both
// read this one list.
var permissions = []permission{
	{"checks", "write", func(p *github.InstallationPermissions) *string { return p.Checks }},
	{"contents", "read", func(p *github.InstallationPermissions) *string { return p.Contents }},
	{"metadata", "read", func(p *github.InstallationPermissions) *string { return p.Metadata }},
	{
		"pull_requests",
		"write",
		func(p *github.InstallationPermissions) *string { return p.PullRequests },
	},
	{"statuses", "write", func(p *github.InstallationPermissions) *string { return p.Statuses }},
}

// events are the webhook events infraweave needs an App to send.
var events = []string{"pull_request", "push"}

// Test checks that in's App can be read, holds permissions, and sends
// events. An App that cannot be read fails every check, with the reason.
func (p *Provider) Test(ctx context.Context, in port.Integration) ([]port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	failed := func(reason string) []port.Check {
		return []port.Check{
			{Type: port.CheckConnection, Reason: reason},
			{Type: port.CheckPermissions, Reason: "The App could not be read."},
			{Type: port.CheckGithubAppEvents, Reason: "The App could not be read."},
		}
	}
	a, err := p.app(in)
	if err != nil {
		return failed(err.Error()), nil
	}
	ga, _, err := client(a.host, a.tr).Apps.Get(ctx, "")
	if err != nil {
		return failed(classify(err, "App").Error()), nil
	}
	var missing []string
	for _, want := range permissions {
		got := want.get(ga.GetPermissions())
		if got == nil || (want.level == "write" && *got != "write") {
			missing = append(missing, want.name+":"+want.level)
		}
	}
	var unsent []string
	for _, e := range events {
		if !slices.Contains(ga.Events, e) {
			unsent = append(unsent, e)
		}
	}
	return []port.Check{
		{Type: port.CheckConnection, Passed: true},
		check(port.CheckPermissions, "The App lacks the permissions ", missing),
		check(port.CheckGithubAppEvents, "The App does not send the events ", unsent),
	}, nil
}

func check(kind, reason string, lacking []string) port.Check {
	if len(lacking) == 0 {
		return port.Check{Type: kind, Passed: true}
	}
	return port.Check{Type: kind, Reason: reason + strings.Join(lacking, ", ") + "."}
}

// App reports in's App: COULD_NOT_DECODE when its ID or key does not
// parse, APP_NOT_FOUND when its host refuses or does not know it, and
// ERROR when its host fails.
func (p *Provider) App(ctx context.Context, in port.Integration) port.App {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, err := p.app(in)
	if err != nil {
		return port.App{Status: port.AppCouldNotDecode}
	}
	c := client(a.host, a.tr)
	ga, _, err := c.Apps.Get(ctx, "")
	if err != nil {
		if errors.Is(classify(err, ""), port.ErrNotFound) ||
			errors.Is(classify(err, ""), ErrCredential) {
			return port.App{Status: port.AppNotFound}
		}
		return port.App{Status: port.AppError}
	}
	list, err := installations(ctx, c)
	if err != nil {
		return port.App{Status: port.AppError, URL: ga.GetHTMLURL()}
	}
	out := port.App{Status: port.AppOK, URL: ga.GetHTMLURL()}
	for _, i := range list {
		out.Installations = append(out.Installations, port.Installation{
			ID: i.GetID(), Account: i.GetAccount().GetLogin(),
			AccountType: i.GetAccount().GetType(), URL: i.GetHTMLURL(),
		})
	}
	return out
}

// manifest is GitHub's App manifest.
//
//nolint:tagliatelle // GitHub's format
type manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     hookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

type hookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// Manifest writes the form that creates a private App on host,
// github.com when it is empty, asking for permissions and events. The
// App's homepage is the host redirectURL names.
func (p *Provider) Manifest(
	host, owner, webhookURL, redirectURL, state string,
) (port.Manifest, error) {
	base := strings.TrimSuffix(or(host, "https://github.com"), "/")
	if u, err := url.Parse(
		base,
	); err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Host == "" {
		return port.Manifest{}, fmt.Errorf("%w: %q", ErrHost, host)
	}
	home, err := url.Parse(redirectURL)
	if err != nil || home.Host == "" {
		return port.Manifest{}, fmt.Errorf("%w: redirect %q", ErrHost, redirectURL)
	}
	perms := make(map[string]string, len(permissions))
	for _, want := range permissions {
		perms[want.name] = want.level
	}
	b, err := json.Marshal(manifest{
		Name: "infraweave", URL: home.Scheme + "://" + home.Host,
		HookAttributes: hookAttributes{URL: webhookURL, Active: true},
		RedirectURL:    redirectURL, DefaultPermissions: perms, DefaultEvents: events,
	})
	if err != nil {
		return port.Manifest{}, fmt.Errorf("github: manifest: %w", err)
	}
	endpoint := base + "/settings/apps/new"
	if owner != "" {
		endpoint = base + "/organizations/" + url.PathEscape(owner) + "/settings/apps/new"
	}
	return port.Manifest{
		Endpoint: endpoint + "?state=" + url.QueryEscape(state),
		Manifest: string(b),
	}, nil
}

// Convert turns code into the App apiHost created from a manifest. The
// conversion is unauthenticated: the code is the credential, and GitHub
// accepts it once, within an hour.
func (p *Provider) Convert(ctx context.Context, apiHost, code string) (port.Created, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	h, err := hostOf(port.Settings{APIHost: apiHost})
	if err != nil {
		return port.Created{}, err
	}
	cfg, _, err := client(h, p.rt).Apps.CompleteAppManifest(ctx, code)
	if err != nil {
		return port.Created{}, classify(err, "manifest code")
	}
	return port.Created{
		Name: cfg.GetName(), AppID: strconv.FormatInt(cfg.GetID(), 10),
		PrivateKey: []byte(cfg.GetPEM()), WebhookSecret: []byte(cfg.GetWebhookSecret()),
	}, nil
}
