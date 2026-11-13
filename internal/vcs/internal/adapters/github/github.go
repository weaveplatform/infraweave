package github

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Timeout bounds each call, every request it sends included.
const Timeout = 30 * time.Second

var (
	// ErrNoIntegration is a repository with no integration: GitHub is
	// reached only through an App.
	ErrNoIntegration = fmt.Errorf("github: the repository has no integration: %w",
		port.ErrNoIntegration)
	// ErrCredential is an App ID or private key that does not parse, or
	// that GitHub refuses.
	ErrCredential = fmt.Errorf("github: App credential refused: %w", port.ErrCredential)
	// ErrHost is a host that is not an http or https URL.
	ErrHost = errors.New("github: host is not an http or https URL")
)

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are the certificate authorities trusted for GitHub's host;
	// nil is the system's.
	RootCAs *x509.CertPool
}

// Provider is the GitHub provider: port.Source, port.Browse, port.Tester,
// port.Apps and port.Minter over GitHub's REST API, reached as a GitHub
// App.
//
// ref: decision 0012
type Provider struct {
	rt http.RoundTripper

	mu     sync.Mutex
	tokens map[tokenKey]*ghinstallation.Transport
}

var (
	_ port.Source = (*Provider)(nil)
	_ port.Browse = (*Provider)(nil)
	_ port.Tester = (*Provider)(nil)
	_ port.Apps   = (*Provider)(nil)
	_ port.Minter = (*Provider)(nil)
)

// New returns the provider, reaching GitHub over TLS 1.2 or later.
func New(o Options) *Provider {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	return newProvider(tr)
}

func newProvider(rt http.RoundTripper) *Provider {
	return &Provider{rt: rt, tokens: map[tokenKey]*ghinstallation.Transport{}}
}

// host is where an integration's App lives: the API's base URL, ending
// in a slash, and the user-facing host commits link to.
type host struct{ api, web string }

// hostOf is s's host. No API host is github.com's; a bare host is GitHub
// Enterprise Server's, whose API is under /api/v3; one with a path is
// taken as the API's base.
func hostOf(s port.Settings) (host, error) {
	if s.APIHost == "" {
		return host{
			api: "https://api.github.com/",
			web: or(s.UserFacingHost, "https://github.com"),
		}, nil
	}
	u, err := url.Parse(s.APIHost)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return host{}, fmt.Errorf("%w: %q", ErrHost, s.APIHost)
	}
	web := u.Scheme + "://" + u.Host
	switch {
	case u.Host == "api.github.com":
		web = "https://github.com"
	case strings.Trim(u.Path, "/") == "":
		u.Path = "/api/v3"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	return host{api: u.String(), web: strings.TrimSuffix(or(s.UserFacingHost, web), "/")}, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// app is an integration's App: its host, ID, key and the transport that
// signs its JWTs.
type app struct {
	host host
	id   int64
	key  [32]byte
	tr   *ghinstallation.AppsTransport
}

// app is in's App; ErrCredential when its ID or key does not parse.
func (p *Provider) app(in port.Integration) (app, error) {
	h, err := hostOf(in.Settings)
	if err != nil {
		return app{}, err
	}
	id, err := strconv.ParseInt(in.Settings.AppID, 10, 64)
	if err != nil {
		return app{}, fmt.Errorf("%w: App ID %q", ErrCredential, in.Settings.AppID)
	}
	tr, err := ghinstallation.NewAppsTransport(p.rt, id, in.Credential)
	if err != nil {
		return app{}, fmt.Errorf("%w: private key: %w", ErrCredential, err)
	}
	tr.BaseURL = strings.TrimSuffix(h.api, "/")
	return app{host: h, id: id, key: sha256.Sum256(in.Credential), tr: tr}, nil
}

// client is a client of h sending through rt.
func client(h host, rt http.RoundTripper) *github.Client {
	c, err := github.NewClient(
		github.WithHTTPClient(&http.Client{Transport: rt}),
		github.WithURLs(&h.api, &h.api),
	)
	if err != nil {
		panic(fmt.Sprintf("github: client of %s: %v", h.api, err)) // hostOf has parsed h.api
	}
	return c
}

// tokenKey names an installation's token transport. The key's hash is in
// it so a changed key never reuses a token the old one minted.
type tokenKey struct {
	api          string
	app          int64
	key          [32]byte
	installation int64
}

// installation is a client acting as installation id of a, reusing the
// transport, and so the token, of an earlier call.
func (p *Provider) installation(a app, id int64) *github.Client {
	k := tokenKey{api: a.host.api, app: a.id, key: a.key, installation: id}
	p.mu.Lock()
	tr, ok := p.tokens[k]
	if !ok {
		tr = ghinstallation.NewFromAppsTransport(a.tr, id)
		tr.BaseURL = a.tr.BaseURL
		p.tokens[k] = tr
	}
	p.mu.Unlock()
	return client(a.host, tr)
}

// repo is a client acting as the installation of repo's App that reaches
// repo; ErrNotFound when none does.
func (p *Provider) repo(ctx context.Context, repo port.Repository) (*github.Client, host, error) {
	a, id, err := p.installationOf(ctx, repo)
	if err != nil {
		return nil, host{}, err
	}
	return p.installation(a, id), a.host, nil
}

// installationOf is repo's App and the ID of its installation that
// reaches repo; ErrNotFound when none does.
func (p *Provider) installationOf(ctx context.Context, repo port.Repository) (app, int64, error) {
	if repo.Integration == nil {
		return app{}, 0, fmt.Errorf("%w: %s/%s", ErrNoIntegration, repo.Namespace, repo.Name)
	}
	a, err := p.app(*repo.Integration)
	if err != nil {
		return app{}, 0, err
	}
	in, _, err := client(
		a.host,
		a.tr,
	).Apps.GetRepositoryInstallation(
		ctx,
		repo.Namespace,
		repo.Name,
	)
	if err != nil {
		return app{}, 0, classify(err, "installation of "+repo.Namespace+"/"+repo.Name)
	}
	return a, in.GetID(), nil
}

// Mint mints an installation token of repo's App that reads repo's
// contents alone. GitHub expires it an hour after minting and takes no
// expiry, so expires bounds nothing here and the token carries no ID to
// revoke. The provider never caches it: the installation's own token,
// which its other calls reuse, stays unnarrowed.
//
// ref: decision 0012 § "Source access for runs"
func (p *Provider) Mint(
	ctx context.Context,
	repo port.Repository,
	_ string,
	_ time.Time,
) (port.Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, id, err := p.installationOf(ctx, repo)
	if err != nil {
		return port.Credential{}, err
	}
	tok, _, err := client(a.host, a.tr).Apps.CreateInstallationToken(ctx, id,
		&github.InstallationTokenOptions{
			Repositories: []string{repo.Name},
			Permissions:  &github.InstallationPermissions{Contents: new("read")},
		})
	if err != nil {
		return port.Credential{}, classify(err, "token for "+repo.Namespace+"/"+repo.Name)
	}
	return port.Credential{
		Username: "x-access-token", Token: []byte(tok.GetToken()),
		ExpiresAt: tok.GetExpiresAt().Time,
	}, nil
}

// Revoke is nil: a token Mint mints carries no ID and only expires, an
// hour after minting.
func (p *Provider) Revoke(context.Context, port.Repository, string) error { return nil }

// ResolveRef resolves ref of repo to its commit, naming the tag when ref
// is one.
func (p *Provider) ResolveRef(
	ctx context.Context,
	repo port.Repository,
	ref string,
) (vcsapi.Commit, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, h, err := p.repo(ctx, repo)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	rc, _, err := c.Repositories.GetCommit(ctx, repo.Namespace, repo.Name, ref, nil)
	if err != nil {
		return vcsapi.Commit{}, classify(err, "ref "+ref)
	}
	out := vcsapi.Commit{
		Hash: rc.GetSHA(),
		URL: fmt.Sprintf(
			"%s/%s/%s/commit/%s",
			h.web,
			repo.Namespace,
			repo.Name,
			rc.GetSHA(),
		),
		Message:    rc.GetCommit().GetMessage(),
		AuthorName: rc.GetCommit().GetAuthor().GetName(),
		Timestamp:  rc.GetCommit().GetAuthor().GetDate().Time,
	}
	// GitHub's author is null when no account owns the commit's e-mail.
	if a := rc.GetAuthor(); a != nil {
		out.AuthorLogin = a.Login
	}
	if ref == out.Hash {
		return out, nil
	}
	switch _, _, err := c.Git.GetRef(ctx, repo.Namespace, repo.Name, "tags/"+ref); {
	case err == nil:
		out.Tag = ref
	case !errors.Is(classify(err, ""), port.ErrNotFound):
		return vcsapi.Commit{}, classify(err, "tag "+ref)
	}
	return out, nil
}

// ReadFile reads path at ref of repo through the raw media type, reading
// one byte past port.MaxFileSize to tell a file at the limit from one
// over it.
func (p *Provider) ReadFile(
	ctx context.Context,
	repo port.Repository,
	ref, path string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return nil, err
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	u := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", url.PathEscape(repo.Namespace),
		url.PathEscape(repo.Name), strings.Join(segs, "/"), url.QueryEscape(ref))
	req, err := c.NewRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("github: %s: %w", u, err)
	}
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	resp, err := c.BareDo(req)
	if err != nil {
		return nil, classify(err, path+" at "+ref)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, port.MaxFileSize+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: github: %s at %s: %w", kernel.ErrUnavailable, path, ref, err)
	case len(b) > port.MaxFileSize:
		return nil, fmt.Errorf("%w: %s at %s", port.ErrTooLarge, path, ref)
	}
	return b, nil
}

// AffectedFiles lists, sorted, the paths that differ between base and
// head of repo, a renamed file under both its names. GitHub's comparison
// lists at most 300 files.
func (p *Provider) AffectedFiles(
	ctx context.Context,
	repo port.Repository,
	base, head string,
) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return nil, err
	}
	var out []string
	opts := &github.ListOptions{PerPage: 100}
	for {
		cmp, resp, err := c.Repositories.CompareCommits(
			ctx,
			repo.Namespace,
			repo.Name,
			base,
			head,
			opts,
		)
		if err != nil {
			return nil, classify(err, base+"..."+head)
		}
		for _, f := range cmp.Files {
			out = append(out, f.GetFilename())
			if prev := f.GetPreviousFilename(); prev != "" {
				out = append(out, prev)
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// SourceURL is repo's clone URL on its integration's user-facing host,
// from which a worker fetches the commit over Git's HTTPS with the run's
// installation token, as every provider's source is fetched; empty on a
// host that does not parse.
func (p *Provider) SourceURL(repo port.Repository, _ string) string {
	var s port.Settings
	if repo.Integration != nil {
		s = repo.Integration.Settings
	}
	h, err := hostOf(s)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s.git", strings.TrimSuffix(h.web, "/"), repo.Namespace, repo.Name)
}

// Repositories lists, sorted, the repositories every installation of in's
// App reaches.
func (p *Provider) Repositories(ctx context.Context, in port.Integration) ([]port.Listed, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, err := p.app(in)
	if err != nil {
		return nil, err
	}
	installs, err := installations(ctx, client(a.host, a.tr))
	if err != nil {
		return nil, err
	}
	var out []port.Listed
	for _, inst := range installs {
		c := p.installation(a, inst.GetID())
		opts := &github.ListOptions{PerPage: 100}
		for {
			list, resp, err := c.Apps.ListRepos(ctx, opts)
			if err != nil {
				return nil, classify(err, "repositories")
			}
			for _, r := range list.Repositories {
				out = append(
					out,
					port.Listed{Namespace: r.GetOwner().GetLogin(), Name: r.GetName()},
				)
			}
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
	slices.SortFunc(out, func(a, b port.Listed) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	return out, nil
}

// Branches lists repo's branches, sorted.
func (p *Provider) Branches(ctx context.Context, repo port.Repository) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return nil, err
	}
	var out []string
	opts := &github.BranchListOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		bs, resp, err := c.Repositories.ListBranches(ctx, repo.Namespace, repo.Name, opts)
		if err != nil {
			return nil, classify(err, "branches")
		}
		for _, b := range bs {
			out = append(out, b.GetName())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	slices.Sort(out)
	return out, nil
}

// installations lists every installation of the App c acts as.
func installations(ctx context.Context, c *github.Client) ([]*github.Installation, error) {
	var out []*github.Installation
	opts := &github.ListOptions{PerPage: 100}
	for {
		list, resp, err := c.Apps.ListInstallations(ctx, opts)
		if err != nil {
			return nil, classify(err, "installations")
		}
		out = append(out, list...)
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// classify maps a GitHub failure to the port's errors: a missing
// resource, or a ref GitHub answers 422 for, is port.ErrNotFound; a rate
// limit is a *port.RateLimited; a refused credential is ErrCredential; a
// host that fails or does not answer is kernel.ErrUnavailable.
func classify(err error, what string) error {
	if rl, ok := errors.AsType[*github.RateLimitError](err); ok {
		return &port.RateLimited{Reset: rl.Rate.Reset.Time}
	}
	if ab, ok := errors.AsType[*github.AbuseRateLimitError](err); ok {
		return &port.RateLimited{Reset: time.Now().Add(ab.GetRetryAfter()).Truncate(time.Second)}
	}
	var resp *http.Response
	if er, ok := errors.AsType[*github.ErrorResponse](err); ok {
		resp = er.Response
	} else if he, ok := errors.AsType[*ghinstallation.HTTPError](err); ok {
		resp = he.Response
	}
	if resp == nil {
		return fmt.Errorf("%w: github: %s: %w", kernel.ErrUnavailable, what, err)
	}
	if rl := rateLimit(resp); rl != nil {
		return rl
	}
	switch s := resp.StatusCode; {
	case s == http.StatusNotFound || s == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: github: %s: %w", port.ErrNotFound, what, err)
	case s == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s: %w", ErrCredential, what, err)
	case s >= http.StatusInternalServerError:
		return fmt.Errorf("%w: github: %s: %w", kernel.ErrUnavailable, what, err)
	}
	return fmt.Errorf("github: %s: %w", what, err)
}

// rateLimit is resp's rate limit, from Retry-After or from
// X-RateLimit-Reset when no request remains; nil when it carries none.
func rateLimit(resp *http.Response) *port.RateLimited {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		return &port.RateLimited{
			Reset: time.Now().Add(time.Duration(s) * time.Second).Truncate(time.Second),
		}
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if s, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return &port.RateLimited{Reset: time.Unix(s, 0)}
		}
	}
	return nil
}
