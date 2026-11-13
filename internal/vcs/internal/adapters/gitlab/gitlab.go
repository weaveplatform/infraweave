package gitlab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"golang.org/x/time/rate"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Timeout bounds each call, every request it sends included.
const Timeout = 30 * time.Second

var (
	// ErrNoIntegration is a repository with no integration: GitLab is
	// reached only with an integration's access token.
	ErrNoIntegration = fmt.Errorf("gitlab: the repository has no integration: %w",
		port.ErrNoIntegration)
	// ErrCredential is an access token that is missing, or that GitLab
	// refuses.
	ErrCredential = fmt.Errorf("gitlab: access token refused: %w", port.ErrCredential)
	// ErrHost is a host that is not an http or https URL.
	ErrHost = errors.New("gitlab: host is not an http or https URL")
	// ErrCannotMint is a token GitLab refuses a project access token to:
	// one below Maintainer on the project, one without the api scope, or
	// a group or project access token, whose bot user may mint none.
	ErrCannotMint = fmt.Errorf("gitlab: the token may not mint a project access token: %w",
		port.ErrCannotMint)
)

// botRefusal is the words of GitLab's 400 to a bot user's mint, which
// gitlabfake.BotRefusal repeats. Only these words make a 400 a refusal:
// another 400 is a request this provider got wrong, and lending the
// integration's wider token for it would hide the fault.
const botRefusal = "permission to create project access token"

// Username is the user git authenticates a token as over HTTPS; GitLab
// reads the token alone.
const Username = "oauth2"

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are the certificate authorities trusted for GitLab's host;
	// nil is the system's.
	RootCAs *x509.CertPool
}

// Provider is the GitLab provider: port.Source, port.Browse, port.Tester,
// port.Minter and port.Lender over GitLab's REST API, reached with an
// access token.
//
// ref: decision 0012
type Provider struct {
	rt http.RoundTripper
}

var (
	_ port.Source = (*Provider)(nil)
	_ port.Browse = (*Provider)(nil)
	_ port.Tester = (*Provider)(nil)
	_ port.Minter = (*Provider)(nil)
	_ port.Lender = (*Provider)(nil)
)

// New returns the provider, reaching GitLab over TLS 1.2 or later.
func New(o Options) *Provider {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	return newProvider(tr)
}

func newProvider(rt http.RoundTripper) *Provider { return &Provider{rt: rt} }

// host is where an integration's GitLab lives: the API's base URL, ending
// in a slash, and the user-facing host commits link to.
type host struct{ api, web string }

// hostOf is s's host. No API host is gitlab.com's; a self-managed host,
// under a relative path or not, has its API under /api/v4, which the API
// host may name or leave out.
func hostOf(s port.Settings) (host, error) {
	if s.APIHost == "" {
		return host{
			api: "https://gitlab.com/api/v4/",
			web: strings.TrimSuffix(or(s.UserFacingHost, "https://gitlab.com"), "/"),
		}, nil
	}
	u, err := url.Parse(s.APIHost)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return host{}, fmt.Errorf("%w: %q", ErrHost, s.APIHost)
	}
	web := u.Scheme + "://" + u.Host +
		strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api/v4")
	return host{
		api: web + "/api/v4/",
		web: strings.TrimSuffix(or(s.UserFacingHost, web), "/"),
	}, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// client is a client of in's host acting with its access token. It never
// retries, so a rate limit reaches the caller, which reschedules for the
// reset, and never paces itself, the host's answer being the limit.
func (p *Provider) client(in port.Integration) (*gitlab.Client, host, error) {
	h, err := hostOf(in.Settings)
	if err != nil {
		return nil, host{}, err
	}
	tok := strings.TrimSpace(string(in.Credential))
	if tok == "" {
		return nil, host{}, fmt.Errorf("%w: no access token", ErrCredential)
	}
	c, err := gitlab.NewClient(tok,
		gitlab.WithBaseURL(h.api),
		gitlab.WithHTTPClient(&http.Client{Transport: p.rt}),
		gitlab.WithoutRetries(),
		gitlab.WithCustomLimiter(rate.NewLimiter(rate.Inf, 0)),
	)
	if err != nil {
		return nil, host{}, fmt.Errorf("%w: %s: %w", ErrHost, h.api, err)
	}
	return c, h, nil
}

// repo is a client of repo's integration and repo's project ID, its full
// path.
func (p *Provider) repo(repo port.Repository) (*gitlab.Client, host, string, error) {
	if repo.Integration == nil {
		return nil, host{}, "", fmt.Errorf("%w: %s/%s", ErrNoIntegration, repo.Namespace, repo.Name)
	}
	c, h, err := p.client(*repo.Integration)
	return c, h, repo.Namespace + "/" + repo.Name, err
}

// ResolveRef resolves ref of repo to its commit, naming the tag when ref
// is one. GitLab names no commit author's login.
func (p *Provider) ResolveRef(
	ctx context.Context,
	repo port.Repository,
	ref string,
) (vcsapi.Commit, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, h, pid, err := p.repo(repo)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	gc, resp, err := c.Commits.GetCommit(pid, ref, nil, gitlab.WithContext(ctx))
	if err != nil {
		return vcsapi.Commit{}, classify(resp, err, "ref "+ref)
	}
	out := vcsapi.Commit{
		Hash:       gc.ID,
		URL:        fmt.Sprintf("%s/%s/-/commit/%s", h.web, pid, gc.ID),
		Message:    gc.Message,
		AuthorName: gc.AuthorName,
	}
	if gc.AuthoredDate != nil {
		out.Timestamp = *gc.AuthoredDate
	}
	if ref == out.Hash {
		return out, nil
	}
	switch _, resp, err := c.Tags.GetTag(pid, ref, gitlab.WithContext(ctx)); {
	case err == nil:
		out.Tag = ref
	case !errors.Is(classify(resp, err, ""), port.ErrNotFound):
		return vcsapi.Commit{}, classify(resp, err, "tag "+ref)
	}
	return out, nil
}

// capped keeps what a response body writes up to port.MaxFileSize and
// ends the request on the byte past it, so a larger file is never read
// to its end.
type capped struct {
	b    []byte
	stop context.CancelFunc
}

func (w *capped) Write(p []byte) (int, error) {
	if len(w.b)+len(p) > port.MaxFileSize {
		w.stop()
		return 0, port.ErrTooLarge
	}
	w.b = append(w.b, p...)
	return len(p), nil
}

// ReadFile reads path at ref of repo through the raw file route.
func (p *Provider) ReadFile(
	ctx context.Context,
	repo port.Repository,
	ref, name string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("projects/%s/repository/files/%s/raw",
		gitlab.PathEscape(pid), gitlab.PathEscape(name))
	req, err := c.NewRequest(http.MethodGet, u, &gitlab.GetRawFileOptions{Ref: &ref},
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx)})
	if err != nil {
		return nil, fmt.Errorf("gitlab: %s: %w", u, err)
	}
	w := &capped{b: []byte{}, stop: cancel}
	switch resp, err := c.Do(req, w); {
	case errors.Is(err, port.ErrTooLarge):
		return nil, fmt.Errorf("%w: %s at %s", port.ErrTooLarge, name, ref)
	case err != nil:
		return nil, classify(resp, err, name+" at "+ref)
	}
	return w.b, nil
}

// AffectedFiles lists, sorted, the paths that differ between head and its
// merge base with base in repo, a renamed file under both its names.
// GitLab's comparison lists the files within its diff limits.
func (p *Provider) AffectedFiles(
	ctx context.Context,
	repo port.Repository,
	base, head string,
) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return nil, err
	}
	cmp, resp, err := c.Repositories.Compare(pid, &gitlab.CompareOptions{From: &base, To: &head},
		gitlab.WithContext(ctx))
	if err != nil {
		return nil, classify(resp, err, base+"..."+head)
	}
	if cmp.CompareTimeout {
		return nil, fmt.Errorf("%w: gitlab: the comparison %s...%s timed out",
			kernel.ErrUnavailable, base, head)
	}
	out := make([]string, 0, len(cmp.Diffs))
	for _, d := range cmp.Diffs {
		out = append(out, d.NewPath, d.OldPath)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// SourceURL is repo's clone URL on its integration's user-facing host,
// from which a worker fetches the commit over Git's HTTPS with the run's
// token, as every provider's source is fetched; empty on a host that does
// not parse.
func (p *Provider) SourceURL(repo port.Repository, _ string) string {
	var s port.Settings
	if repo.Integration != nil {
		s = repo.Integration.Settings
	}
	h, err := hostOf(s)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s.git", h.web, repo.Namespace, repo.Name)
}

// Repositories lists, sorted, the projects in's token's user is a member
// of, each by its namespace's full path, a subgroup's included, and its
// path.
func (p *Provider) Repositories(ctx context.Context, in port.Integration) ([]port.Listed, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.client(in)
	if err != nil {
		return nil, err
	}
	ps, err := projects(ctx, c, 0)
	if err != nil {
		return nil, err
	}
	out := make([]port.Listed, 0, len(ps))
	for _, full := range ps {
		out = append(out, port.Listed{Namespace: path.Dir(full), Name: path.Base(full)})
	}
	return out, nil
}

// projects lists, sorted, the full paths of the projects c's user is a
// member of at least at level, at any level when it is 0.
func projects(
	ctx context.Context,
	c *gitlab.Client,
	level gitlab.AccessLevelValue,
) ([]string, error) {
	opt := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100},
		Membership:  gitlab.Ptr(true),
	}
	if level > 0 {
		opt.MinAccessLevel = &level
	}
	ps, err := all(ctx, "projects",
		func(o ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error) {
			return c.Projects.ListProjects(opt, o...)
		})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.PathWithNamespace)
	}
	slices.Sort(out)
	return out, nil
}

// Branches lists repo's branches, sorted.
func (p *Provider) Branches(ctx context.Context, repo port.Repository) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return nil, err
	}
	opt := &gitlab.ListBranchesOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}
	bs, err := all(ctx, "branches",
		func(o ...gitlab.RequestOptionFunc) ([]*gitlab.Branch, *gitlab.Response, error) {
			return c.Branches.ListBranches(pid, opt, o...)
		})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	slices.Sort(out)
	return out, nil
}

// Mint mints a project access token named name on repo's project, with
// read_repository alone at Reporter, so it clones that project and reads
// nothing else. GitLab expires a token at the start of its expiry date in
// UTC, so it expires at the start of expires' date, before expires.
func (p *Provider) Mint(
	ctx context.Context,
	repo port.Repository,
	name string,
	expires time.Time,
) (port.Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return port.Credential{}, err
	}
	day := time.Date(expires.UTC().Year(), expires.UTC().Month(), expires.UTC().Day(),
		0, 0, 0, 0, time.UTC)
	iso := gitlab.ISOTime(day)
	tok, resp, err := c.ProjectAccessTokens.CreateProjectAccessToken(pid,
		&gitlab.CreateProjectAccessTokenOptions{
			Name:        &name,
			Scopes:      &[]string{"read_repository"},
			AccessLevel: gitlab.Ptr(gitlab.ReporterPermissions),
			ExpiresAt:   &iso,
		}, gitlab.WithContext(ctx))
	if err != nil {
		if refusedMint(resp, err) {
			return port.Credential{}, fmt.Errorf("%w: %s: %w", ErrCannotMint, pid, err)
		}
		return port.Credential{}, classify(resp, err, "access token of "+pid)
	}
	return port.Credential{
		Username: Username, Token: []byte(tok.Token),
		ID: strconv.FormatInt(tok.ID, 10), ExpiresAt: day,
	}, nil
}

// refusedMint reports whether GitLab refused the token a project access
// token: a 403 that is no rate limit, or a 400 refusing a bot user.
func refusedMint(resp *gitlab.Response, err error) bool {
	if resp == nil || resp.Response == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusForbidden:
		return rateLimit(resp.Response) == nil
	case http.StatusBadRequest:
		var e *gitlab.ErrorResponse
		return errors.As(err, &e) && strings.Contains(e.Message, botRefusal)
	}
	return false
}

// Revoke revokes repo's project access token id. A token GitLab no longer
// finds, revoked already or on a project gone since, is nil: there is
// nothing left to revoke.
func (p *Provider) Revoke(ctx context.Context, repo port.Repository, id string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("gitlab: access token ID %q: %w", id, err)
	}
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return err
	}
	resp, err := c.ProjectAccessTokens.RevokeProjectAccessToken(pid, n, gitlab.WithContext(ctx))
	if err == nil {
		return nil
	}
	if err := classify(
		resp,
		err,
		"access token "+id+" of "+pid,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		return err
	}
	return nil
}

// Lend is in's own access token, which reads every project its user is a
// member of.
func (p *Provider) Lend(in port.Integration) port.Credential {
	return port.Credential{
		Username: Username, Token: []byte(strings.TrimSpace(string(in.Credential))),
	}
}

// all collects every page list answers, classifying the failure of the
// page that fails.
func all[T any](
	ctx context.Context,
	what string,
	list func(...gitlab.RequestOptionFunc) ([]T, *gitlab.Response, error),
) ([]T, error) {
	var last *gitlab.Response
	out, err := gitlab.ScanAndCollect(
		func(page gitlab.PaginationOptionFunc) ([]T, *gitlab.Response, error) {
			ts, resp, err := list(gitlab.WithContext(ctx), page)
			last = resp
			return ts, resp, err
		})
	if err != nil {
		return nil, classify(last, err, what)
	}
	return out, nil
}

// classify maps a GitLab failure to the port's errors: a missing
// resource is port.ErrNotFound; a rate limit is a *port.RateLimited; a
// refused token is ErrCredential; a host that fails or does not answer
// is kernel.ErrUnavailable.
func classify(resp *gitlab.Response, err error, what string) error {
	if resp == nil || resp.Response == nil || resp.StatusCode < http.StatusBadRequest {
		return fmt.Errorf("%w: gitlab: %s: %w", kernel.ErrUnavailable, what, err)
	}
	if rl := rateLimit(resp.Response); rl != nil {
		return rl
	}
	switch s := resp.StatusCode; {
	case s == http.StatusNotFound:
		return fmt.Errorf("%w: gitlab: %s: %w", port.ErrNotFound, what, err)
	case s == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s: %w", ErrCredential, what, err)
	case s >= http.StatusInternalServerError:
		return fmt.Errorf("%w: gitlab: %s: %w", kernel.ErrUnavailable, what, err)
	}
	return fmt.Errorf("gitlab: %s: %w", what, err)
}

// rateLimit is resp's rate limit, from RateLimit-Reset, or from
// Retry-After when it carries no reset; nil when it carries neither.
func rateLimit(resp *http.Response) *port.RateLimited {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	if s, err := strconv.ParseInt(resp.Header.Get("RateLimit-Reset"), 10, 64); err == nil {
		return &port.RateLimited{Reset: time.Unix(s, 0)}
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		return &port.RateLimited{
			Reset: time.Now().Add(time.Duration(s) * time.Second).Truncate(time.Second),
		}
	}
	return nil
}
