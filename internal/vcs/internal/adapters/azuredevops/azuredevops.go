package azuredevops

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

// Backoff is how long a rate limit lasts when Azure DevOps's answer names
// no reset.
const Backoff = time.Minute

// APIVersion is the REST API version every request names.
const APIVersion = "7.1"

// top is the page size every paged list asks for.
const top = 100

var (
	// ErrNoIntegration is a repository with no integration: Azure DevOps
	// is reached only with an integration's personal access token.
	ErrNoIntegration = fmt.Errorf("azuredevops: the repository has no integration: %w",
		port.ErrNoIntegration)
	// ErrCredential is a personal access token that is missing, or that
	// Azure DevOps refuses.
	ErrCredential = fmt.Errorf("azuredevops: personal access token refused: %w", port.ErrCredential)
	// ErrStatus is an answer that is none of the port's errors.
	ErrStatus = errors.New("azuredevops: unexpected answer")
	// ErrHost is an organization URL or user-facing host that is not an
	// https URL.
	ErrHost = errors.New("azuredevops: organization URL is not an https URL")
)

// sha is a full commit SHA, the only form Azure DevOps's commit route
// resolves.
var sha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Options are what a deployment or a test changes.
type Options struct {
	// RootCAs are the certificate authorities trusted for an
	// organization's host; nil is the system's.
	RootCAs *x509.CertPool
}

// Provider is the Azure DevOps provider: port.Source, port.Browse and
// port.Tester over Azure DevOps's REST API, reached with a personal access
// token on the organization each integration names.
//
// ref: decision 0012
type Provider struct {
	client *http.Client
}

var (
	_ port.Source = (*Provider)(nil)
	_ port.Browse = (*Provider)(nil)
	_ port.Tester = (*Provider)(nil)
	_ port.Lender = (*Provider)(nil)
)

// PATUser is the user git authenticates a personal access token as over
// HTTPS; Azure DevOps reads the token alone, but git needs a user.
const PATUser = "pat"

// New returns the provider, reaching each organization over TLS 1.2 or
// later.
func New(o Options) *Provider {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	tr.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	return newProvider(tr)
}

func newProvider(rt http.RoundTripper) *Provider {
	return &Provider{client: &http.Client{Transport: allowlist{next: rt}}}
}

// org is an organization as the provider reaches it: its URL with no
// trailing slash, that URL's escaped path, and the user-facing host its
// commits and clones are on.
type org struct {
	api, base, web string
}

// orgOf is s's organization: its organization URL, and its user-facing
// host or, when s names none, the organization URL; ErrHost when either
// is not an https URL with a host and nothing past its path.
func orgOf(s port.Settings) (org, error) {
	api, err := httpsURL(s.OrganizationURL)
	if err != nil {
		return org{}, err
	}
	web := api
	if s.UserFacingHost != "" {
		if web, err = httpsURL(s.UserFacingHost); err != nil {
			return org{}, err
		}
	}
	u, _ := url.Parse(api) //nolint:errcheck // httpsURL parsed it
	return org{api: api, base: u.EscapedPath(), web: web}, nil
}

// httpsURL is raw with no trailing slash; ErrHost when it is not an https
// URL with a host and no credential, query or fragment.
func httpsURL(raw string) (string, error) {
	trimmed := strings.TrimSuffix(raw, "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q", ErrHost, raw)
	}
	return trimmed, nil
}

// baseKey carries a request's organization path to the allowlist.
type baseKey struct{}

// allowlist sends a request only to one of Routes under the organization
// path its context carries.
type allowlist struct {
	next http.RoundTripper
}

func (a allowlist) RoundTrip(r *http.Request) (*http.Response, error) {
	base, _ := r.Context().Value(baseKey{}).(string) //nolint:errcheck // absent is no base
	rel, ok := strings.CutPrefix(r.URL.EscapedPath(), base)
	if !ok || !Allowed(r.Method, rel) {
		return nil, fmt.Errorf("%w: %s %s", ErrRoute, r.Method, r.URL.EscapedPath())
	}
	return a.next.RoundTrip(r) //nolint:wrapcheck // the client wraps it
}

// repoPath is repo's path under its organization's URL; ErrNoIntegration
// when it has no integration.
func repoPath(repo port.Repository) (string, error) {
	if repo.Integration == nil {
		return "", fmt.Errorf("%w: %s/%s", ErrNoIntegration, repo.Namespace, repo.Name)
	}
	return "/" + url.PathEscape(repo.Namespace) + "/_apis/git/repositories/" +
		url.PathEscape(repo.Name), nil
}

// get sends a GET of rel, an escaped path under in's organization, with q
// and the API version, as in's token, and answers a 2xx response that is
// not Azure DevOps's sign-in page, classifying any other.
func (p *Provider) get(
	ctx context.Context,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
) (*http.Response, error) {
	o, err := orgOf(in.Settings)
	if err != nil {
		return nil, err
	}
	tok := strings.TrimSpace(string(in.Credential))
	if tok == "" {
		return nil, fmt.Errorf("%w: no personal access token", ErrCredential)
	}
	q.Set("api-version", APIVersion)
	req, err := http.NewRequestWithContext(context.WithValue(ctx, baseKey{}, o.base),
		http.MethodGet, o.api+rel+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("azuredevops: %s: %w", what, err)
	}
	req.SetBasicAuth("", tok)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	switch {
	case errors.Is(err, ErrRoute):
		return nil, fmt.Errorf("azuredevops: %s: %w", what, err)
	case err != nil:
		return nil, fmt.Errorf("%w: azuredevops: %s: %w", kernel.ErrUnavailable, what, err)
	case resp.StatusCode == http.StatusNonAuthoritativeInfo &&
		!strings.Contains(resp.Header.Get("Content-Type"), "json"):
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: %s: answered with the sign-in page", ErrCredential, what)
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, classify(resp, what)
}

// decode GETs rel into v, answering the response's continuation token.
func (p *Provider) decode(
	ctx context.Context,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
	v any,
) (string, error) {
	resp, err := p.get(ctx, in, rel, q, what)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return "", fmt.Errorf("%w: azuredevops: %s: %w", kernel.ErrUnavailable, what, err)
	}
	return resp.Header.Get("X-Ms-Continuationtoken"), nil
}

// list is a list as Azure DevOps answers one.
type list[T any] struct {
	Value []T `json:"value"`
}

// all collects every page of the list at rel, following each answer's
// continuation token.
func all[T any](
	ctx context.Context,
	p *Provider,
	in port.Integration,
	rel string,
	q url.Values,
	what string,
) ([]T, error) {
	q.Set("$top", strconv.Itoa(top))
	var out []T
	for {
		var pg list[T]
		next, err := p.decode(ctx, in, rel, q, what, &pg)
		if err != nil {
			return nil, err
		}
		out = append(out, pg.Value...)
		if next == "" || next == q.Get("continuationToken") {
			return out, nil
		}
		q.Set("continuationToken", next)
	}
}

// commit is a commit as Azure DevOps answers it.
type commit struct {
	CommitID string `json:"commitId"`
	Comment  string `json:"comment"`
	Author   struct {
		Name string    `json:"name"`
		Date time.Time `json:"date"`
	} `json:"author"`
}

// ref is a branch or a tag, with the object it points at and, for an
// annotated tag, the commit it peels to.
type ref struct {
	Name           string `json:"name"`
	ObjectID       string `json:"objectId"`
	PeeledObjectID string `json:"peeledObjectId"`
}

// out is c as the port names it, in repo, on o.
func out(o org, repo port.Repository, c commit) vcsapi.Commit {
	return vcsapi.Commit{
		Hash:       c.CommitID,
		URL:        webRepo(o, repo) + "/commit/" + c.CommitID,
		Message:    c.Comment,
		AuthorName: c.Author.Name,
		Timestamp:  c.Author.Date,
	}
}

// webRepo is repo's page on o's user-facing host.
func webRepo(o org, repo port.Repository) string {
	return o.web + "/" + url.PathEscape(repo.Namespace) + "/_git/" + url.PathEscape(repo.Name)
}

// ResolveRef resolves ref of repo to its commit: a branch, then a tag,
// which it names, peeled when it is annotated, then a full SHA.
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
	o, err := orgOf(in.Settings)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	at, tag := name, ""
	if !sha.MatchString(name) {
		if at, tag, err = p.ref(ctx, in, rp, name); err != nil {
			return vcsapi.Commit{}, err
		}
	}
	var c commit
	if _, err := p.decode(ctx, in, rp+"/commits/"+at, url.Values{}, "ref "+name, &c); err != nil {
		return vcsapi.Commit{}, err
	}
	got := out(o, repo, c)
	got.Tag = tag
	return got, nil
}

// ref is the commit the branch or tag name points at, and name again when
// it is a tag. The refs route filters by prefix, so only the ref named
// exactly counts.
func (p *Provider) ref(
	ctx context.Context,
	in port.Integration,
	rp, name string,
) (at, tag string, err error) {
	for _, kind := range []string{"heads", "tags"} {
		refs, err := all[ref](ctx, p, in, rp+"/refs",
			url.Values{"filter": {kind + "/" + name}, "peelTags": {"true"}}, "ref "+name)
		if err != nil {
			return "", "", err
		}
		for _, r := range refs {
			if r.Name != "refs/"+kind+"/"+name {
				continue
			}
			if kind == "heads" {
				return r.ObjectID, "", nil
			}
			if r.PeeledObjectID != "" {
				return r.PeeledObjectID, name, nil
			}
			return r.ObjectID, name, nil
		}
	}
	return "", "", fmt.Errorf("%w: azuredevops: ref %s", port.ErrNotFound, name)
}

// commitOf is name of repo as a full SHA, resolving a ref that is not one.
func (p *Provider) commitOf(
	ctx context.Context,
	repo port.Repository,
	name string,
) (string, error) {
	if sha.MatchString(name) {
		return name, nil
	}
	c, err := p.resolve(ctx, repo, name)
	return c.Hash, err
}

// ReadFile reads path at ref of repo, resolving a ref that is not a full
// SHA first.
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
	at, err := p.commitOf(ctx, repo, name)
	if err != nil {
		return nil, err
	}
	what := file + " at " + name
	resp, err := p.get(ctx, *repo.Integration, rp+"/items", url.Values{
		"path":                          {"/" + strings.TrimPrefix(file, "/")},
		"versionDescriptor.version":     {at},
		"versionDescriptor.versionType": {"commit"},
		"download":                      {"true"},
		"$format":                       {"octetStream"},
	}, what)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, port.MaxFileSize+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: azuredevops: %s: %w", kernel.ErrUnavailable, what, err)
	case len(b) > port.MaxFileSize:
		return nil, fmt.Errorf("%w: %s", port.ErrTooLarge, what)
	}
	return b, nil
}

// change is a file a commit diff names, with the path it was renamed
// from.
type change struct {
	Item struct {
		Path     string `json:"path"`
		IsFolder bool   `json:"isFolder"`
	} `json:"item"`
	SourceServerItem string `json:"sourceServerItem"`
}

// AffectedFiles lists, sorted, the paths that differ between head and its
// merge base with base in repo, a renamed file under both its names,
// resolving each ref that is not a full SHA first.
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
	from, err := p.commitOf(ctx, repo, base)
	if err != nil {
		return nil, err
	}
	to, err := p.commitOf(ctx, repo, head)
	if err != nil {
		return nil, err
	}
	var paths []string
	for skip := 0; ; {
		var pg struct {
			Changes []change `json:"changes"`
		}
		if _, err := p.decode(ctx, *repo.Integration, rp+"/diffs/commits", url.Values{
			"baseVersion": {from}, "baseVersionType": {"commit"},
			"targetVersion": {to}, "targetVersionType": {"commit"},
			"$top": {strconv.Itoa(top)}, "$skip": {strconv.Itoa(skip)},
		}, base+"..."+head, &pg); err != nil {
			return nil, err
		}
		for _, c := range pg.Changes {
			if c.Item.IsFolder {
				continue
			}
			for _, path := range []string{c.Item.Path, c.SourceServerItem} {
				if path != "" {
					paths = append(paths, strings.TrimPrefix(path, "/"))
				}
			}
		}
		if len(pg.Changes) < top {
			break
		}
		skip += len(pg.Changes)
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// Lend is in's personal access token as git checks out with it. Azure
// DevOps mints no token narrower than the integration's through its API,
// so a run reads every repository the integration does.
//
// ref: decision 0012 § "Source access for runs"
func (p *Provider) Lend(in port.Integration) port.Credential {
	return port.Credential{Username: PATUser, Token: in.Credential}
}

// SourceURL is repo's clone URL on its integration's user-facing host;
// empty when the repository has no integration or a host that is not an
// https URL.
func (p *Provider) SourceURL(repo port.Repository, _ string) string {
	if repo.Integration == nil {
		return ""
	}
	o, err := orgOf(repo.Integration.Settings)
	if err != nil {
		return ""
	}
	return webRepo(o, repo)
}

// projects lists, sorted, the projects in's integration reaches: its
// accessible projects, or every project of the organization the token
// reads when it names none.
func (p *Provider) projects(ctx context.Context, in port.Integration) ([]string, error) {
	if len(in.Settings.AccessibleProjects) > 0 {
		out := slices.Clone(in.Settings.AccessibleProjects)
		slices.Sort(out)
		return slices.Compact(out), nil
	}
	ps, err := all[struct {
		Name string `json:"name"`
	}](ctx, p, in, "/_apis/projects", url.Values{}, "projects")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ps))
	for _, pr := range ps {
		out = append(out, pr.Name)
	}
	slices.Sort(out)
	return out, nil
}

// repositories lists the names of project's repositories.
func (p *Provider) repositories(
	ctx context.Context,
	in port.Integration,
	project string,
) ([]string, error) {
	var rs list[struct {
		Name string `json:"name"`
	}]
	if _, err := p.decode(ctx, in, "/"+url.PathEscape(project)+"/_apis/git/repositories",
		url.Values{}, "repositories of "+project, &rs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rs.Value))
	for _, r := range rs.Value {
		out = append(out, r.Name)
	}
	return out, nil
}

// Repositories lists, sorted, the repositories of the projects in's
// integration reaches, each by its project and its name; a project the
// organization no longer has lists none.
func (p *Provider) Repositories(ctx context.Context, in port.Integration) ([]port.Listed, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	projects, err := p.projects(ctx, in)
	if err != nil {
		return nil, err
	}
	var out []port.Listed
	for _, project := range projects {
		rs, err := p.repositories(ctx, in, project)
		switch {
		case errors.Is(err, port.ErrNotFound):
			continue
		case err != nil:
			return nil, err
		}
		for _, r := range rs {
			out = append(out, port.Listed{Namespace: project, Name: r})
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

// Branches lists repo's branches, sorted.
func (p *Provider) Branches(ctx context.Context, repo port.Repository) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	refs, err := all[ref](ctx, p, *repo.Integration, rp+"/refs",
		url.Values{"filter": {"heads/"}}, "branches")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if b, ok := strings.CutPrefix(r.Name, "refs/heads/"); ok {
			out = append(out, b)
		}
	}
	slices.Sort(out)
	return out, nil
}

// classify maps an Azure DevOps answer that is not 2xx to the port's
// errors: a rate limit is a *port.RateLimited; a missing resource is
// port.ErrNotFound; a refused token, or one lacking a scope, is
// ErrCredential; a host that fails is kernel.ErrUnavailable.
func classify(resp *http.Response, what string) error {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
	msg := body.Message
	if msg == "" {
		msg = resp.Status
	}
	switch s := resp.StatusCode; {
	case s == http.StatusTooManyRequests:
		return rateLimit(resp)
	case s == http.StatusNotFound:
		return fmt.Errorf("%w: azuredevops: %s: %s", port.ErrNotFound, what, msg)
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return fmt.Errorf("%w: %s: %s", ErrCredential, what, msg)
	case s >= http.StatusInternalServerError:
		return fmt.Errorf("%w: azuredevops: %s: %s", kernel.ErrUnavailable, what, msg)
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
