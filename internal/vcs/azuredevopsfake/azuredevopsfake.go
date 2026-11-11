package azuredevopsfake

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/gitmem"
)

// Organization is the one organization the fake serves, under its URL's
// path.
const Organization = "acme"

// The scopes a personal access token holds, as Azure DevOps names them.
const (
	// ScopeProject reads the organization's projects.
	ScopeProject = "vso.project"
	// ScopeCode reads repositories, their refs, commits, files and diffs.
	ScopeCode = "vso.code"
	// ScopeHooks reads and writes service hook subscriptions.
	ScopeHooks = "vso.hooks_write"
)

// Fault is how the fake answers every request while it is set.
type Fault int

const (
	// None answers each request.
	None Fault = iota
	// RateLimited answers 429 with Retry-After and X-RateLimit-Reset, the
	// time the limit lifts, as Azure DevOps's rate limits do.
	RateLimited
	// TooManyRequests answers 429 with Retry-After alone, the seconds
	// until the limit lifts.
	TooManyRequests
	// ServerError answers 503.
	ServerError
	// Hang answers nothing until the request's context ends.
	Hang
)

// Role is a user's role in a project, as Azure DevOps's default groups
// name them.
type Role int

const (
	// Reader reads the project and its repositories.
	Reader Role = iota + 1
	// Contributor pushes to its repositories.
	Contributor
	// Administrator administers it.
	Administrator
)

// Author is the name and e-mail every seeded commit carries.
const (
	AuthorName  = gitmem.AuthorName
	AuthorEmail = gitmem.AuthorEmail
)

// Repo is a repository, held in memory by go-git, so refs, annotated
// tags, trees and their differences behave as Git's do.
type Repo = gitmem.Repo

// sha is a full commit SHA, the only form the commit route resolves.
var sha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// token is a personal access token: its user and its scopes, every one
// when it names none.
type token struct {
	user   string
	scopes []string
}

// holds reports whether t holds scope.
func (t *token) holds(scope string) bool {
	return len(t.scopes) == 0 || slices.Contains(t.scopes, scope)
}

// project is a project, with its users' roles and its repositories by
// name.
type project struct {
	id    string
	name  string
	roles map[string]Role
	repos map[string]*repository
}

// repository is a repository of a project.
type repository struct {
	id   string
	name string
	repo *Repo
}

// Fake is an Azure DevOps organization: personal access tokens of its
// users, and projects with the roles users hold in them and their Git
// repositories, served under /acme.
type Fake struct {
	mu       sync.Mutex
	tokens   map[string]*token
	projects map[string]*project
	nextID   int
	fault    Fault
	scope    string
	until    time.Time
	requests int
}

// New returns a fake with no token, project or repository.
func New() *Fake {
	return &Fake{tokens: map[string]*token{}, projects: map[string]*project{}}
}

// Server is the fake served over TLS.
type Server struct {
	// URL is the server's base URL.
	URL string
	// OrganizationURL is the organization's URL, URL under /acme, which an
	// integration names.
	OrganizationURL string
	// CA is the PEM of the certificate the server presents, for a client
	// to trust, such as through vcs.ca_file.
	CA []byte

	srv *httptest.Server
}

// Pool is a certificate pool holding the server's certificate alone.
func (s *Server) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(s.srv.Certificate())
	return p
}

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Start serves the fake over TLS on a loopback port.
func (f *Fake) Start() *Server {
	srv := httptest.NewTLSServer(f.Handler())
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return &Server{
		URL: srv.URL, OrganizationURL: srv.URL + "/" + Organization, CA: ca, srv: srv,
	}
}

// id is a new identifier, as Azure DevOps's GUIDs stand. f.mu is held.
func (f *Fake) id() string {
	f.nextID++
	return "00000000-0000-0000-0000-" + strconv.FormatInt(int64(1e11+f.nextID), 10)[:12]
}

// AddToken issues a personal access token of user holding scopes, every
// scope when it names none, and returns it.
func (f *Fake) AddToken(user string, scopes ...string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := secret()
	f.tokens[tok] = &token{user: user, scopes: scopes}
	return tok
}

// projectOf is the project name, created the first time. f.mu is held.
func (f *Fake) projectOf(name string) *project {
	p, ok := f.projects[name]
	if !ok {
		p = &project{
			id: f.id(), name: name, roles: map[string]Role{}, repos: map[string]*repository{},
		}
		f.projects[name] = p
	}
	return p
}

// Repo returns the repository name of project, both created empty the
// first time.
func (f *Fake) Repo(projectName, name string) *Repo {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projectOf(projectName)
	r, ok := p.repos[name]
	if !ok {
		r = &repository{id: f.id(), name: name, repo: gitmem.New()}
		p.repos[name] = r
	}
	return r.repo
}

// Grant gives user role in project, created the first time, which lets
// it read the project and its repositories.
func (f *Fake) Grant(projectName, user string, role Role) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectOf(projectName).roles[user] = role
}

// Fail answers every request with fault until it is set to None, a rate
// limit lifting after.
func (f *Fake) Fail(fault Fault, after time.Duration) { f.FailPath("", fault, after) }

// FailPath is Fail for the requests whose path under the organization
// starts with prefix alone, such as /_apis/hooks while the projects read.
func (f *Fake) FailPath(prefix string, fault Fault, after time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fault, f.scope, f.until = fault, prefix, time.Now().Add(after).Truncate(time.Second)
}

// Requests counts the requests the fake has received.
func (f *Fake) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// Handler serves the fake's routes under /acme.
func (f *Fake) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(f.faults)
	r.Route("/"+Organization, func(r chi.Router) {
		r.Use(f.authenticated)
		r.With(f.scoped(ScopeProject)).Get("/_apis/projects", f.listProjects)
		r.With(f.scoped(ScopeHooks)).Get("/_apis/hooks/subscriptions", f.subscriptions)
		r.Route("/{project}/_apis/git/repositories", func(r chi.Router) {
			r.Use(f.scoped(ScopeCode), f.member)
			r.Get("/", f.repositories)
			r.Route("/{repository}", func(r chi.Router) {
				r.Use(f.repository)
				r.Get("/refs", f.refs)
				r.Get("/commits/{commit}", f.commit)
				r.Get("/items", f.items)
				r.Get("/diffs/commits", f.diffs)
			})
		})
	})
	return r
}

type ctxKey int

const (
	tokenKey ctxKey = iota
	projectKey
	repoKey
)

// faults counts each request and answers it as the fault set.
func (f *Fake) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		fault, until := f.fault, f.until
		if !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/"+Organization), f.scope) {
			fault = None
		}
		f.mu.Unlock()
		wait := strconv.Itoa(int(time.Until(until).Round(time.Second).Seconds()))
		switch fault {
		case RateLimited:
			w.Header().Set("Retry-After", wait)
			w.Header().Set("X-RateLimit-Resource", "Core")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(until.Unix(), 10))
			answer(
				w,
				http.StatusTooManyRequests,
				problem("TF400733: The request has been throttled."),
			)
		case TooManyRequests:
			w.Header().Set("Retry-After", wait)
			answer(w, http.StatusTooManyRequests, problem("Too Many Requests"))
		case ServerError:
			answer(w, http.StatusServiceUnavailable, problem("Service Unavailable"))
		case Hang:
			<-r.Context().Done()
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// authenticated admits a request whose basic password is a personal
// access token the fake issued. A request with no credential gets the
// sign-in page with 203, as Azure DevOps answers an anonymous one; a
// token the fake did not issue gets 401.
func (f *Fake) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, tok, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNonAuthoritativeInfo)
			_, _ = w.Write([]byte("<html><title>Azure DevOps Services | Sign In</title></html>"))
			return
		}
		f.mu.Lock()
		t, known := f.tokens[tok]
		f.mu.Unlock()
		if !known {
			answer(w, http.StatusUnauthorized, problem("TF400813: The user is not authorized."))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey, t)))
	})
}

// scoped admits a request whose token holds scope, answering 401 as
// Azure DevOps refuses a token lacking one.
func (f *Fake) scoped(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !tokenOf(r).holds(scope) {
				answer(w, http.StatusUnauthorized,
					problem("The personal access token lacks the scope "+scope+"."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// member admits a request for a project the token's user holds a role
// in, answering 404 for any other, as Azure DevOps hides a project.
func (f *Fake) member(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := param(r, "project")
		f.mu.Lock()
		p, ok := f.projects[name]
		if ok {
			_, ok = p.roles[tokenOf(r).user]
		}
		f.mu.Unlock()
		if !ok {
			answer(
				w,
				http.StatusNotFound,
				problem("TF200016: The following project does not exist: "+name+"."),
			)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectKey, p)))
	})
}

// repository admits a request for a repository of the project.
func (f *Fake) repository(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
		name := param(r, "repository")
		f.mu.Lock()
		repo, ok := p.repos[name]
		f.mu.Unlock()
		if !ok {
			answer(w, http.StatusNotFound, problem("TF401019: The Git repository with name or "+
				"identifier "+name+" does not exist or you do not have permissions for the "+
				"operation you are attempting."))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), repoKey, repo)))
	})
}

func tokenOf(r *http.Request) *token {
	return r.Context().Value(tokenKey).(*token) //nolint:forcetypeassert // authenticated sets it
}

func repoOf(r *http.Request) *repository {
	return r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // repository sets it
}

// listProjects lists the projects the token's user holds a role in, by
// name, a page at a time.
func (f *Fake) listProjects(w http.ResponseWriter, r *http.Request) {
	user := tokenOf(r).user
	f.mu.Lock()
	var out []map[string]any
	for _, name := range slices.Sorted(maps.Keys(f.projects)) {
		if p := f.projects[name]; p.roles[user] > 0 {
			out = append(out, map[string]any{"id": p.id, "name": p.name, "state": "wellFormed"})
		}
	}
	f.mu.Unlock()
	paged(w, r, out)
}

// subscriptions lists no service hook subscription.
func (f *Fake) subscriptions(w http.ResponseWriter, _ *http.Request) {
	answer(w, http.StatusOK, map[string]any{"count": 0, "value": []any{}})
}

func (f *Fake) repositories(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	f.mu.Lock()
	out := make([]map[string]any, 0, len(p.repos))
	for _, name := range slices.Sorted(maps.Keys(p.repos)) {
		out = append(out, map[string]any{
			"id": p.repos[name].id, "name": name,
			"project": map[string]string{"id": p.id, "name": p.name},
		})
	}
	f.mu.Unlock()
	answer(w, http.StatusOK, map[string]any{"count": len(out), "value": out})
}

// refs lists the branches whose names under refs/ start with the filter,
// a page at a time, and the tag a filter names in full, peeled when
// peelTags is set.
func (f *Fake) refs(w http.ResponseWriter, r *http.Request) {
	repo := repoOf(r).repo
	filter := r.URL.Query().Get("filter")
	var out []map[string]any
	bs, _ := repo.Branches() //nolint:errcheck // a failure lists none
	for _, b := range bs {
		if name := "heads/" + b.Name; strings.HasPrefix(name, filter) {
			out = append(out, map[string]any{"name": "refs/" + name, "objectId": b.Commit})
		}
	}
	if tag, ok := strings.CutPrefix(filter, "tags/"); ok {
		if obj, _, c, err := repo.TagOf(tag); err == nil {
			ref := map[string]any{"name": "refs/tags/" + tag, "objectId": obj}
			if r.URL.Query().Get("peelTags") == "true" && obj != c.Hash.String() {
				ref["peeledObjectId"] = c.Hash.String()
			}
			out = append(out, ref)
		}
	}
	paged(w, r, out)
}

func (f *Fake) commit(w http.ResponseWriter, r *http.Request) {
	repo := repoOf(r)
	id := chi.URLParam(r, "commit")
	c, err := repo.repo.Resolve(id)
	if !sha.MatchString(id) || err != nil {
		answer(w, http.StatusNotFound, problem("TF401175: The version descriptor <Commit: "+id+
			"> could not be resolved to a version in the repository "+repo.name+"."))
		return
	}
	answer(w, http.StatusOK, commitJSON(r, repo, c))
}

// items answers a file's bytes at a commit.
func (f *Fake) items(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	file := strings.TrimPrefix(q.Get("path"), "/")
	b, err := repoOf(r).repo.File(q.Get("versionDescriptor.version"), file)
	if q.Get("versionDescriptor.versionType") != "commit" || err != nil {
		answer(
			w,
			http.StatusNotFound,
			problem("TF401174: The item '/"+file+"' could not be found."),
		)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	//nolint:gosec // a file's bytes as application/octet-stream with nosniff, never HTML
	_, _ = w.Write(b)
}

// diffs lists the files and the folders holding them that differ between
// two commits, a page at a time by $top and $skip, a rename naming its
// source.
func (f *Fake) diffs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base, target := q.Get("baseVersion"), q.Get("targetVersion")
	changes, err := repoOf(r).repo.Changes(r.Context(), base, target)
	if !sha.MatchString(base) || !sha.MatchString(target) || err != nil {
		answer(w, http.StatusNotFound, problem("TF401175: The version descriptor could not be "+
			"resolved to a version in the repository."))
		return
	}
	folders := map[string]bool{}
	var out []map[string]any
	for _, ch := range changes {
		item, kind, source := ch.To, "edit", ""
		switch {
		case ch.From == "":
			kind = "add"
		case ch.To == "":
			item, kind = ch.From, "delete"
		case ch.From != ch.To:
			kind, source = "rename", "/"+ch.From
		}
		if dir := path.Dir(item); dir != "." {
			folders[dir] = true
		}
		c := map[string]any{
			"item": map[string]any{"path": "/" + item, "gitObjectType": "blob"}, "changeType": kind,
		}
		if source != "" {
			c["sourceServerItem"] = source
		}
		out = append(out, c)
	}
	for _, dir := range slices.Sorted(maps.Keys(folders)) {
		out = append(out, map[string]any{
			"item": map[string]any{
				"path":          "/" + dir,
				"isFolder":      true,
				"gitObjectType": "tree",
			},
			"changeType": "edit",
		})
	}
	per, skip := intOf(q.Get("$top"), 100), intOf(q.Get("$skip"), 0)
	start := min(skip, len(out))
	end := min(start+per, len(out))
	answer(w, http.StatusOK, map[string]any{
		"allChangesIncluded": end == len(out), "changes": out[start:end],
		"baseCommit": base, "targetCommit": target,
	})
}

// commitJSON is c as Azure DevOps answers a commit of repo.
func commitJSON(r *http.Request, repo *repository, c *object.Commit) map[string]any {
	person := func(s object.Signature) map[string]string {
		return map[string]string{
			"name": s.Name, "email": s.Email, "date": s.When.UTC().Format(time.RFC3339),
		}
	}
	return map[string]any{
		"commitId": c.Hash.String(), "comment": strings.TrimSuffix(c.Message, "\n"),
		"author": person(c.Author), "committer": person(c.Committer),
		"remoteUrl": "https://" + r.Host + "/" + Organization + "/" +
			url.PathEscape(
				param(r, "project"),
			) + "/_git/" + url.PathEscape(repo.name) + "/commit/" + c.Hash.String(),
	}
}

// paged answers the page of items $top and continuationToken name, the
// next page's token in X-MS-ContinuationToken.
func paged(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	q := r.URL.Query()
	per, start := intOf(q.Get("$top"), 100), intOf(q.Get("continuationToken"), 0)
	start = min(start, len(items))
	end := min(start+per, len(items))
	if end < len(items) {
		w.Header().Set("X-MS-ContinuationToken", strconv.Itoa(end))
	}
	page := items[start:end]
	if page == nil {
		page = []map[string]any{}
	}
	answer(w, http.StatusOK, map[string]any{"count": len(page), "value": page})
}

// param is the route's parameter name, URL-decoded.
func param(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}

// intOf is s as a number no less than zero, or def.
func intOf(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n
	}
	return def
}

func problem(m string) map[string]any {
	return map[string]any{"$id": "1", "message": m, "typeKey": "", "errorCode": 0}
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// secret is 26 random bytes in hex, as long as a personal access token.
func secret() string {
	b := make([]byte, 26)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
