package bitbucketcloud

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

// Backoff is how long a rate limit lasts when Bitbucket's answer names
// no reset, as most of its 429s do.
const Backoff = time.Minute

// DefaultAPIHost is Bitbucket Cloud's API.
const DefaultAPIHost = "https://api.bitbucket.org/2.0"

// pagelen is the page size every list asks for, Bitbucket's largest for
// repositories and branches.
const pagelen = "100"

var (
	// ErrNoIntegration is a repository with no integration: Bitbucket
	// Cloud is reached only with an integration's API token.
	ErrNoIntegration = fmt.Errorf("bitbucketcloud: the repository has no integration: %w",
		port.ErrNoIntegration)
	// ErrCredential is an e-mail or API token that is missing, or that
	// Bitbucket refuses.
	ErrCredential = errors.New("bitbucketcloud: API token refused")
	// ErrStatus is an answer that is none of the port's errors.
	ErrStatus = errors.New("bitbucketcloud: unexpected answer")
	// ErrHost is an API host that is not an http or https URL.
	ErrHost = errors.New("bitbucketcloud: API host is not an http or https URL")
)

// sha is a full commit SHA, which ReadFile reads at without resolving.
var sha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// abbreviated is what Bitbucket's commit route resolves besides a full
// SHA: a SHA's prefix.
var abbreviated = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are the certificate authorities trusted for the API host;
	// nil is the system's.
	RootCAs *x509.CertPool
	// APIHost is the API's base URL; empty is DefaultAPIHost.
	APIHost string
}

// Provider is the Bitbucket Cloud provider: port.Source, port.Browse and
// port.Tester over Bitbucket's REST API 2.0, reached with an Atlassian
// account's e-mail and API token.
//
// ref: decision 0012
type Provider struct {
	client *http.Client
	// api is the API's base URL with no trailing slash, and web the
	// user-facing host commits and clones are on.
	api, web string
	// host is ErrHost when Options named an API host that is not a URL,
	// which every call then answers.
	host error
}

var (
	_ port.Source = (*Provider)(nil)
	_ port.Browse = (*Provider)(nil)
	_ port.Tester = (*Provider)(nil)
)

// New returns the provider, reaching the API over TLS 1.2 or later. When
// o.APIHost is not an http or https URL, every call answers ErrHost.
func New(o Options) *Provider {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	return newProvider(tr, o.APIHost)
}

func newProvider(rt http.RoundTripper, apiHost string) *Provider {
	api, web, err := hostOf(apiHost)
	if err != nil {
		return &Provider{host: err}
	}
	u, _ := url.Parse(api) //nolint:errcheck // hostOf parsed it
	return &Provider{
		client: &http.Client{Transport: allowlist{next: rt, base: u.EscapedPath()}},
		api:    api, web: web,
	}
}

// hostOf is the API's base and the user-facing host of apiHost: Bitbucket
// Cloud's for its own API, and the API host's origin for any other, such
// as a test's.
func hostOf(apiHost string) (api, web string, err error) {
	api = strings.TrimSuffix(apiHost, "/")
	if api == "" || api == DefaultAPIHost {
		return DefaultAPIHost, "https://bitbucket.org", nil
	}
	u, err := url.Parse(api)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("%w: %q", ErrHost, apiHost)
	}
	return api, u.Scheme + "://" + u.Host, nil
}

// allowlist sends a request only to one of Routes under base.
type allowlist struct {
	next http.RoundTripper
	base string
}

func (a allowlist) RoundTrip(r *http.Request) (*http.Response, error) {
	rel, ok := strings.CutPrefix(r.URL.EscapedPath(), a.base)
	if !ok || !Allowed(r.Method, rel) {
		return nil, fmt.Errorf("%w: %s %s", ErrRoute, r.Method, r.URL.EscapedPath())
	}
	return a.next.RoundTrip(r) //nolint:wrapcheck // the client wraps it
}

// escape escapes each segment of p, keeping its slashes, as Bitbucket
// names a branch, a tag or a file path.
func escape(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// repoPath is repo's path under the API's base; ErrNoIntegration when it
// has no integration.
func repoPath(repo port.Repository) (string, error) {
	if repo.Integration == nil {
		return "", fmt.Errorf("%w: %s/%s", ErrNoIntegration, repo.Namespace, repo.Name)
	}
	return "/repositories/" + url.PathEscape(repo.Namespace) + "/" + url.PathEscape(repo.Name), nil
}

// get sends a GET of rawURL as in's account and answers a 2xx response,
// classifying any other.
func (p *Provider) get(
	ctx context.Context,
	in port.Integration,
	rawURL, what string,
) (*http.Response, error) {
	if p.host != nil {
		return nil, p.host
	}
	tok := strings.TrimSpace(string(in.Credential))
	if in.Settings.Email == "" || tok == "" {
		return nil, fmt.Errorf("%w: no e-mail or API token", ErrCredential)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bitbucketcloud: %s: %w", what, err)
	}
	req.SetBasicAuth(in.Settings.Email, tok)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	switch {
	case errors.Is(err, ErrRoute):
		return nil, fmt.Errorf("bitbucketcloud: %s: %w", what, err)
	case err != nil:
		return nil, fmt.Errorf("%w: bitbucketcloud: %s: %w", kernel.ErrUnavailable, what, err)
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, classify(resp, what)
}

// decode GETs rawURL into v.
func (p *Provider) decode(
	ctx context.Context,
	in port.Integration,
	rawURL, what string,
	v any,
) error {
	resp, err := p.get(ctx, in, rawURL, what)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: bitbucketcloud: %s: %w", kernel.ErrUnavailable, what, err)
	}
	return nil
}

// page is one page of a Bitbucket list.
type page[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

// all collects every page of the list at rel, an escaped path under the
// API's base with its query, following each page's next link, which must
// stay under the API's base.
func all[T any](
	ctx context.Context,
	p *Provider,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
) ([]T, error) {
	q.Set("pagelen", pagelen)
	next := p.api + rel + "?" + q.Encode()
	var out []T
	for next != "" {
		if !strings.HasPrefix(next, p.api+"/") {
			return nil, fmt.Errorf("%w: bitbucketcloud: %s: a next page off the API: %s",
				kernel.ErrUnavailable, what, next)
		}
		var pg page[T]
		if err := p.decode(ctx, in, next, what, &pg); err != nil {
			return nil, err
		}
		out = append(out, pg.Values...)
		next = pg.Next
	}
	return out, nil
}

// commit is a commit as Bitbucket answers it.
type commit struct {
	Hash    string    `json:"hash"`
	Date    time.Time `json:"date"`
	Message string    `json:"message"`
	Author  struct {
		Raw  string `json:"raw"`
		User *struct {
			DisplayName string `json:"display_name"` //nolint:tagliatelle // Bitbucket's spelling
			Nickname    string `json:"nickname"`
		} `json:"user"`
	} `json:"author"`
}

// ref is a branch or a tag, with the commit it points at.
type ref struct {
	Name   string `json:"name"`
	Target commit `json:"target"`
}

// out is c as the port names it, in repo.
func (p *Provider) out(repo port.Repository, c commit) vcsapi.Commit {
	got := vcsapi.Commit{
		Hash:      c.Hash,
		URL:       fmt.Sprintf("%s/%s/%s/commits/%s", p.web, repo.Namespace, repo.Name, c.Hash),
		Message:   c.Message,
		Timestamp: c.Date,
	}
	got.AuthorName, _, _ = strings.Cut(c.Author.Raw, " <")
	if u := c.Author.User; u != nil {
		got.AuthorName = u.DisplayName
		if u.Nickname != "" {
			got.AuthorLogin = &u.Nickname
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
	if !sha.MatchString(name) {
		for _, kind := range []string{"branches", "tags"} {
			var r ref
			err := p.decode(ctx, in, p.api+rp+"/refs/"+kind+"/"+escape(name), "ref "+name, &r)
			switch {
			case err == nil:
				got := p.out(repo, r.Target)
				if kind == "tags" {
					got.Tag = name
				}
				return got, nil
			case !errors.Is(err, port.ErrNotFound):
				return vcsapi.Commit{}, err
			}
		}
		if !abbreviated.MatchString(name) {
			return vcsapi.Commit{}, fmt.Errorf("%w: bitbucketcloud: ref %s", port.ErrNotFound, name)
		}
	}
	var c commit
	if err := p.decode(
		ctx,
		in,
		p.api+rp+"/commit/"+url.PathEscape(name),
		"ref "+name,
		&c,
	); err != nil {
		return vcsapi.Commit{}, err
	}
	return p.out(repo, c), nil
}

// ReadFile reads path at ref of repo, resolving a ref that is not a full
// SHA first, so a branch with a slash in its name never reads as a path.
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
	resp, err := p.get(ctx, *repo.Integration, p.api+rp+"/src/"+at+"/"+escape(file), what)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, port.MaxFileSize+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: bitbucketcloud: %s: %w", kernel.ErrUnavailable, what, err)
	case len(b) > port.MaxFileSize:
		return nil, fmt.Errorf("%w: %s", port.ErrTooLarge, what)
	}
	return b, nil
}

// AffectedFiles lists, sorted, the paths that differ between head and its
// merge base with base in repo, a renamed file under both its names, from
// Bitbucket's diffstat, which names head first.
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
	type side struct {
		Path string `json:"path"`
	}
	stats, err := all[struct {
		Old *side `json:"old"`
		New *side `json:"new"`
	}](ctx, p, *repo.Integration,
		rp+"/diffstat/"+url.PathEscape(head)+".."+url.PathEscape(base), url.Values{},
		base+"..."+head)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(stats))
	for _, s := range stats {
		for _, sd := range []*side{s.Old, s.New} {
			if sd != nil {
				out = append(out, sd.Path)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// SourceURL is repo's clone URL on the user-facing host: Bitbucket serves
// no archive to an API token, so a worker checks commit out with Git.
func (p *Provider) SourceURL(repo port.Repository, _ string) string {
	if p.host != nil {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s.git", p.web, repo.Namespace, repo.Name)
}

// workspaces lists the slugs of the workspaces in's account reaches.
func (p *Provider) workspaces(ctx context.Context, in port.Integration) ([]string, error) {
	ws, err := all[struct {
		Workspace struct {
			Slug string `json:"slug"`
		} `json:"workspace"`
	}](ctx, p, in, "/user/workspaces", url.Values{}, "workspaces")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Workspace.Slug)
	}
	slices.Sort(out)
	return out, nil
}

// repositories lists, sorted, the repositories in's account holds role
// on, member or admin, in every workspace it reaches.
func (p *Provider) repositories(
	ctx context.Context,
	in port.Integration,
	role string,
) ([]port.Listed, error) {
	ws, err := p.workspaces(ctx, in)
	if err != nil {
		return nil, err
	}
	var out []port.Listed
	for _, w := range ws {
		rs, err := all[struct {
			Slug string `json:"slug"`
		}](ctx, p, in, "/repositories/"+url.PathEscape(w), url.Values{"role": {role}},
			"repositories of "+w)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, port.Listed{Namespace: w, Name: r.Slug})
		}
	}
	slices.SortFunc(out, func(a, b port.Listed) int {
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Repositories lists, sorted, the repositories in's account is a member
// of, each by its workspace and its slug.
func (p *Provider) Repositories(ctx context.Context, in port.Integration) ([]port.Listed, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return p.repositories(ctx, in, "member")
}

// Branches lists repo's branches, sorted.
func (p *Provider) Branches(ctx context.Context, repo port.Repository) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	bs, err := all[ref](ctx, p, *repo.Integration, rp+"/refs/branches", url.Values{}, "branches")
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

// classify maps a Bitbucket answer that is not 2xx to the port's errors:
// a rate limit is a *port.RateLimited; a missing resource is
// port.ErrNotFound; a refused e-mail or token, or one lacking a scope,
// is ErrCredential; a host that fails is kernel.ErrUnavailable.
func classify(resp *http.Response, what string) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
	msg := body.Error.Message
	if msg == "" {
		msg = resp.Status
	}
	switch s := resp.StatusCode; {
	case s == http.StatusTooManyRequests:
		return rateLimit(resp)
	case s == http.StatusNotFound:
		return fmt.Errorf("%w: bitbucketcloud: %s: %s", port.ErrNotFound, what, msg)
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return fmt.Errorf("%w: %s: %s", ErrCredential, what, msg)
	case s >= http.StatusInternalServerError:
		return fmt.Errorf("%w: bitbucketcloud: %s: %s", kernel.ErrUnavailable, what, msg)
	}
	return fmt.Errorf("%w: %s: %s", ErrStatus, what, msg)
}

// rateLimit is the reset of resp, a 429: X-RateLimit-Reset's time, or
// Retry-After's seconds from now, or Backoff from now when it names
// neither.
func rateLimit(resp *http.Response) *port.RateLimited {
	if s, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		return &port.RateLimited{Reset: time.Unix(s, 0)}
	}
	wait := Backoff
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		wait = time.Duration(s) * time.Second
	}
	return &port.RateLimited{Reset: time.Now().Add(wait).Truncate(time.Second)}
}
