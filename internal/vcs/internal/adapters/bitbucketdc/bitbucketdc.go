package bitbucketdc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Timeout bounds each call, every request it sends included.
const Timeout = 30 * time.Second

// Backoff is how long a rate limit lasts when Bitbucket's answer names no
// Retry-After.
const Backoff = time.Minute

// API is the REST API's path on an integration's API host.
const API = "/rest/api/1.0"

// limit is the page size every list asks for.
const limit = "100"

var (
	// ErrNoIntegration is a repository with no integration: Bitbucket
	// Data Center is reached only with an integration's access token.
	ErrNoIntegration = fmt.Errorf("bitbucketdc: the repository has no integration: %w",
		port.ErrNoIntegration)
	// ErrCredential is an access token that is missing, or that Bitbucket
	// refuses.
	ErrCredential = errors.New("bitbucketdc: access token refused")
	// ErrStatus is an answer that is none of the port's errors.
	ErrStatus = errors.New("bitbucketdc: unexpected answer")
	// ErrHost is an API host that is not an https URL.
	ErrHost = errors.New("bitbucketdc: API host is not an https URL")
)

// sha is a full commit SHA, which ReadFile reads at without resolving.
var sha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// abbreviated is what Bitbucket's commit route resolves besides a full
// SHA: a SHA's prefix.
var abbreviated = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are the certificate authorities trusted for an
	// integration's host; nil is the system's.
	RootCAs *x509.CertPool
}

// Provider is the Bitbucket Data Center provider: port.Source,
// port.Browse and port.Tester over Bitbucket's REST API 1.0 on each
// integration's own host, reached with an HTTP access token.
//
// ref: decision 0012
type Provider struct {
	client *http.Client
}

var (
	_ port.Source = (*Provider)(nil)
	_ port.Browse = (*Provider)(nil)
	_ port.Tester = (*Provider)(nil)
)

// New returns the provider, reaching each host over TLS 1.2 or later.
func New(o Options) *Provider {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	return newProvider(tr)
}

func newProvider(rt http.RoundTripper) *Provider {
	return &Provider{client: &http.Client{Transport: allowlist{next: rt}}}
}

// host is an integration's REST API base, with no trailing slash, and the
// user-facing host its commits and clones are on.
type host struct{ api, web string }

// hostOf is s's host: its API host's REST API, and its user-facing host,
// the API host when it names none. Each is an https URL, a context path
// such as /bitbucket allowed.
func hostOf(s port.Settings) (host, error) {
	api, err := base(s.APIHost)
	if err != nil {
		return host{}, err
	}
	web := api
	if s.UserFacingHost != "" {
		if web, err = base(s.UserFacingHost); err != nil {
			return host{}, err
		}
	}
	return host{api: api + API, web: web}, nil
}

// base is raw with no trailing slash, when it is an https URL with a host
// and no credential, query or fragment.
func base(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q", ErrHost, raw)
	}
	return strings.TrimSuffix(raw, "/"), nil
}

// baseKey carries the escaped path of a request's REST API base, which
// the allowlist matches the rest of its path against.
type baseKey struct{}

// allowlist sends a request only to one of Routes under the REST API base
// its context names.
type allowlist struct{ next http.RoundTripper }

func (a allowlist) RoundTrip(r *http.Request) (*http.Response, error) {
	b, _ := r.Context().Value(baseKey{}).(string) //nolint:errcheck // absent refuses
	rel, ok := strings.CutPrefix(r.URL.EscapedPath(), b)
	if b == "" || !ok || !Allowed(r.Method, rel) {
		return nil, fmt.Errorf("%w: %s %s", ErrRoute, r.Method, r.URL.EscapedPath())
	}
	return a.next.RoundTrip(r) //nolint:wrapcheck // the client wraps it
}

// escape escapes each segment of p, keeping its slashes, as Bitbucket
// names a tag or a file path.
func escape(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// repoPath is repo's path under the REST API's base; ErrNoIntegration
// when it has no integration.
func repoPath(repo port.Repository) (string, error) {
	if repo.Integration == nil {
		return "", fmt.Errorf("%w: %s/%s", ErrNoIntegration, repo.Namespace, repo.Name)
	}
	return "/projects/" + url.PathEscape(
		repo.Namespace,
	) + "/repos/" + url.PathEscape(
		repo.Name,
	), nil
}

// get sends a GET of rel, an escaped path under in's REST API, with q as
// in's token, and answers a 2xx response, classifying any other.
func (p *Provider) get(
	ctx context.Context,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
) (*http.Response, error) {
	h, err := hostOf(in.Settings)
	if err != nil {
		return nil, err
	}
	tok := strings.TrimSpace(string(in.Credential))
	if tok == "" {
		return nil, fmt.Errorf("%w: no access token", ErrCredential)
	}
	u, _ := url.Parse(h.api) //nolint:errcheck // hostOf parsed it
	ctx = context.WithValue(ctx, baseKey{}, u.EscapedPath())
	raw := h.api + rel
	if len(q) > 0 {
		raw += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("bitbucketdc: %s: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	switch {
	case errors.Is(err, ErrRoute):
		return nil, fmt.Errorf("bitbucketdc: %s: %w", what, err)
	case err != nil:
		return nil, fmt.Errorf("%w: bitbucketdc: %s: %w", kernel.ErrUnavailable, what, err)
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, classify(resp, what)
}

// decode GETs rel with q into v.
func (p *Provider) decode(
	ctx context.Context,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
	v any,
) error {
	resp, err := p.get(ctx, in, rel, q, what)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: bitbucketdc: %s: %w", kernel.ErrUnavailable, what, err)
	}
	return nil
}

// page is one page of a Bitbucket list.
type page[T any] struct {
	Values        []T  `json:"values"`
	IsLastPage    bool `json:"isLastPage"`
	NextPageStart *int `json:"nextPageStart"`
}

// all collects every page of the list at rel with q, each page starting
// where the last one says the next does, which must be further on.
func all[T any](
	ctx context.Context,
	p *Provider,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
) ([]T, error) {
	q.Set("limit", limit)
	var out []T
	for start := 0; ; {
		q.Set("start", strconv.Itoa(start))
		var pg page[T]
		if err := p.decode(ctx, in, rel, q, what, &pg); err != nil {
			return nil, err
		}
		out = append(out, pg.Values...)
		if pg.IsLastPage {
			return out, nil
		}
		if pg.NextPageStart == nil || *pg.NextPageStart <= start {
			return nil, fmt.Errorf("%w: bitbucketdc: %s: a page with no next page",
				kernel.ErrUnavailable, what)
		}
		start = *pg.NextPageStart
	}
}

// commit is a commit as Bitbucket answers it, its author a user when
// Bitbucket links its e-mail to one.
type commit struct {
	ID              string `json:"id"`
	Message         string `json:"message"`
	AuthorTimestamp int64  `json:"authorTimestamp"`
	Author          struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Slug        string `json:"slug"`
	} `json:"author"`
}

// ref is a branch or a tag, with the commit it points at.
type ref struct {
	DisplayID    string `json:"displayId"`
	LatestCommit string `json:"latestCommit"`
}

// out is c as the port names it, in repo on h.
func out(h host, repo port.Repository, c commit) vcsapi.Commit {
	got := vcsapi.Commit{
		Hash: c.ID,
		URL: fmt.Sprintf("%s/projects/%s/repos/%s/commits/%s",
			h.web, repo.Namespace, repo.Name, c.ID),
		Message:    c.Message,
		Timestamp:  time.UnixMilli(c.AuthorTimestamp).UTC(),
		AuthorName: c.Author.Name,
	}
	if c.Author.Slug != "" {
		got.AuthorLogin = &c.Author.Slug
		if c.Author.DisplayName != "" {
			got.AuthorName = c.Author.DisplayName
		}
	}
	return got
}

// ResolveRef resolves ref of repo to its commit: a branch, then a tag,
// which it names, then a SHA or its prefix, as Bitbucket's commit route
// resolves one.
func (p *Provider) ResolveRef(
	ctx context.Context,
	repo port.Repository,
	name string,
) (vcsapi.Commit, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return p.resolve(ctx, repo, name)
}

func (p *Provider) resolve(
	ctx context.Context,
	repo port.Repository,
	name string,
) (vcsapi.Commit, error) {
	rp, err := repoPath(repo)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	in := *repo.Integration
	h, err := hostOf(in.Settings)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	at, tag := name, ""
	if !sha.MatchString(name) {
		bs, err := all[ref](ctx, p, in, rp+"/branches",
			url.Values{"filterText": {name}}, "ref "+name)
		if err != nil {
			return vcsapi.Commit{}, err
		}
		i := slices.IndexFunc(bs, func(b ref) bool { return b.DisplayID == name })
		if i >= 0 {
			at = bs[i].LatestCommit
		} else {
			var t ref
			switch err := p.decode(ctx, in, rp+"/tags/"+escape(name), nil, "ref "+name, &t); {
			case err == nil:
				at, tag = t.LatestCommit, name
			case !errors.Is(err, port.ErrNotFound):
				return vcsapi.Commit{}, err
			case !abbreviated.MatchString(name):
				return vcsapi.Commit{}, fmt.Errorf(
					"%w: bitbucketdc: ref %s",
					port.ErrNotFound,
					name,
				)
			}
		}
	}
	var c commit
	if err := p.decode(
		ctx,
		in,
		rp+"/commits/"+url.PathEscape(at),
		nil,
		"ref "+name,
		&c,
	); err != nil {
		return vcsapi.Commit{}, err
	}
	got := out(h, repo, c)
	got.Tag = tag
	return got, nil
}

// ReadFile reads path at ref of repo, resolving a ref that is not a full
// SHA first, so a ref names the same commit ResolveRef does.
func (p *Provider) ReadFile(
	ctx context.Context,
	repo port.Repository,
	name, file string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	at := name
	if !sha.MatchString(name) {
		c, err := p.resolve(ctx, repo, name)
		if err != nil {
			return nil, err
		}
		at = c.Hash
	}
	what := file + " at " + name
	resp, err := p.get(ctx, *repo.Integration, rp+"/raw/"+escape(file),
		url.Values{"at": {at}}, what)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, port.MaxFileSize+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: bitbucketdc: %s: %w", kernel.ErrUnavailable, what, err)
	case len(b) > port.MaxFileSize:
		return nil, fmt.Errorf("%w: %s", port.ErrTooLarge, what)
	}
	return b, nil
}

// AffectedFiles lists, sorted, the paths that differ between head and its
// merge base with base in repo, a moved file under both its names, from
// Bitbucket's compare, which compares from head to base.
func (p *Provider) AffectedFiles(
	ctx context.Context,
	repo port.Repository,
	base, head string,
) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	type path struct {
		String string `json:"toString"`
	}
	changes, err := all[struct {
		Path    *path `json:"path"`
		SrcPath *path `json:"srcPath"`
	}](ctx, p, *repo.Integration, rp+"/compare/changes",
		url.Values{"from": {head}, "to": {base}}, base+"..."+head)
	if err != nil {
		return nil, err
	}
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		for _, p := range []*path{c.Path, c.SrcPath} {
			if p != nil {
				got = append(got, p.String)
			}
		}
	}
	slices.Sort(got)
	return slices.Compact(got), nil
}

// SourceURL is repo's clone URL on its integration's user-facing host,
// under the project's key in lower case, as Bitbucket names it; empty for
// a repository with no integration or a host that is not an https URL.
func (p *Provider) SourceURL(repo port.Repository, _ string) string {
	if repo.Integration == nil {
		return ""
	}
	h, err := hostOf(repo.Integration.Settings)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/scm/%s/%s.git", h.web, strings.ToLower(repo.Namespace), repo.Name)
}

// repositories lists, sorted, the repositories in's user holds permission
// on, REPO_READ or REPO_ADMIN, each by its project's key and its slug.
func (p *Provider) repositories(
	ctx context.Context,
	in port.Integration,
	permission string,
) ([]port.Listed, error) {
	rs, err := all[struct {
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	}](ctx, p, in, "/repos", url.Values{"permission": {permission}}, "repositories")
	if err != nil {
		return nil, err
	}
	got := make([]port.Listed, 0, len(rs))
	for _, r := range rs {
		got = append(got, port.Listed{Namespace: r.Project.Key, Name: r.Slug})
	}
	slices.SortFunc(got, func(a, b port.Listed) int {
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return got, nil
}

// Repositories lists, sorted, the repositories in's user reads, each by
// its project's key and its slug.
func (p *Provider) Repositories(ctx context.Context, in port.Integration) ([]port.Listed, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return p.repositories(ctx, in, "REPO_READ")
}

// Branches lists repo's branches, sorted.
func (p *Provider) Branches(ctx context.Context, repo port.Repository) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	bs, err := all[ref](ctx, p, *repo.Integration, rp+"/branches", url.Values{}, "branches")
	if err != nil {
		return nil, err
	}
	got := make([]string, 0, len(bs))
	for _, b := range bs {
		got = append(got, b.DisplayID)
	}
	slices.Sort(got)
	return got, nil
}

// classify maps a Bitbucket answer that is not 2xx to the port's errors:
// a rate limit is a *port.RateLimited; a missing resource is
// port.ErrNotFound; a refused token, or one lacking a permission, is
// ErrCredential; a host that fails is kernel.ErrUnavailable.
func classify(resp *http.Response, what string) error {
	var body struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
	msg := resp.Status
	if len(body.Errors) > 0 && body.Errors[0].Message != "" {
		msg = body.Errors[0].Message
	}
	switch s := resp.StatusCode; {
	case s == http.StatusTooManyRequests:
		return rateLimit(resp)
	case s == http.StatusNotFound:
		return fmt.Errorf("%w: bitbucketdc: %s: %s", port.ErrNotFound, what, msg)
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return fmt.Errorf("%w: %s: %s", ErrCredential, what, msg)
	case s >= http.StatusInternalServerError:
		return fmt.Errorf("%w: bitbucketdc: %s: %s", kernel.ErrUnavailable, what, msg)
	}
	return fmt.Errorf("%w: %s: %s", ErrStatus, what, msg)
}

// rateLimit is the reset of resp, a 429: Retry-After's seconds from now,
// or Backoff from now when it names none.
func rateLimit(resp *http.Response) *port.RateLimited {
	wait := Backoff
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		wait = time.Duration(s) * time.Second
	}
	return &port.RateLimited{Reset: time.Now().Add(wait).Truncate(time.Second)}
}
