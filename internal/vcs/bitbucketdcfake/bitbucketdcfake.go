package bitbucketdcfake

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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/gitmem"
)

// API is the path the REST API is served under, below the server's URL.
const API = "/rest/api/1.0"

// Fault is how the fake answers every request while it is set.
type Fault int

const (
	// None answers each request.
	None Fault = iota
	// RateLimited answers 429 with Bitbucket's X-RateLimit headers, which
	// name no reset.
	RateLimited
	// TooManyRequests answers 429 with Retry-After, the seconds until the
	// limit lifts, as Bitbucket's token-bucket limiter does.
	TooManyRequests
	// ServerError answers 502.
	ServerError
	// Hang answers nothing until the request's context ends.
	Hang
)

// Role is a permission a user holds on a repository, as Bitbucket names
// them REPO_READ, REPO_WRITE and REPO_ADMIN.
type Role int

const (
	// Read reads the repository.
	Read Role = iota + 1
	// Write pushes to it.
	Write
	// Admin administers it, its access tokens and webhooks included.
	Admin
)

// permissions are the roles Bitbucket's permission parameter names.
var permissions = map[string]Role{
	"": Read, "REPO_READ": Read, "REPO_WRITE": Write, "REPO_ADMIN": Admin,
}

// Author is the name and e-mail every seeded commit carries; a user whose
// e-mail it is, AuthorUser, is the commits' linked author.
const (
	AuthorName  = gitmem.AuthorName
	AuthorEmail = gitmem.AuthorEmail
	AuthorUser  = "author"
)

// Repo is a repository, held in memory by go-git, so refs, annotated
// tags, trees and their differences behave as Git's do.
type Repo = gitmem.Repo

// user is a Bitbucket user, named by its username, whose e-mail is the
// username at example.com.
type user struct {
	id   int
	name string
}

func (u *user) email() string { return u.name + "@example.com" }

// repository is a repository in a project, with its users' roles by
// username.
type repository struct {
	project string
	slug    string
	repo    *Repo
	roles   map[string]Role
}

// Fake is a Bitbucket Data Center REST API: users, their HTTP access
// tokens, and repositories in projects with the roles users hold on them,
// served under API.
type Fake struct {
	mu       sync.Mutex
	users    map[string]*user
	tokens   map[string]*user
	repos    map[string]*repository
	nextID   int
	fault    Fault
	scope    string
	until    time.Time
	requests int
}

// New returns a fake with no user, token or repository.
func New() *Fake {
	return &Fake{
		users: map[string]*user{}, tokens: map[string]*user{}, repos: map[string]*repository{},
	}
}

// Server is the fake served over TLS.
type Server struct {
	// URL is the server's base URL, an integration's API and user-facing
	// host.
	URL string
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
	return &Server{URL: srv.URL, CA: ca, srv: srv}
}

// userOf is the user name, created the first time. f.mu is held.
func (f *Fake) userOf(name string) *user {
	u, ok := f.users[name]
	if !ok {
		f.nextID++
		u = &user{id: f.nextID, name: name}
		f.users[name] = u
	}
	return u
}

// AddToken issues an HTTP access token of the user username, created the
// first time, and returns it. A request authenticates with it as a bearer
// token.
func (f *Fake) AddToken(username string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := "BBDC-" + secret()
	f.tokens[tok] = f.userOf(username)
	return tok
}

// Repo returns the repository slug in the project key, created empty the
// first time.
func (f *Fake) Repo(project, slug string) *Repo {
	return f.repositoryOf(project, slug).repo
}

func (f *Fake) repositoryOf(project, slug string) *repository {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := project + "/" + slug
	if r, ok := f.repos[key]; ok {
		return r
	}
	r := &repository{project: project, slug: slug, repo: gitmem.New(), roles: map[string]Role{}}
	f.repos[key] = r
	return r
}

// Grant gives the user username, created the first time, role on the
// repository slug in the project key.
func (f *Fake) Grant(project, slug, username string, role Role) {
	r := f.repositoryOf(project, slug)
	f.mu.Lock()
	defer f.mu.Unlock()
	r.roles[f.userOf(username).name] = role
}

// Fail answers every request with fault until it is set to None, a rate
// limit lifting after.
func (f *Fake) Fail(fault Fault, after time.Duration) { f.FailPath("", fault, after) }

// FailPath is Fail for the requests whose path under API starts with
// prefix alone, such as /repos while a test lists repositories.
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

// Handler serves the fake's routes under API. A tag and a file path keep
// their slashes, as Bitbucket names them.
func (f *Fake) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(f.faults)
	r.Route(API, func(r chi.Router) {
		r.Use(f.authenticated)
		r.Get("/profile/recent/repos", f.recent)
		r.Get("/repos", f.list)
		r.Route("/projects/{project}/repos/{slug}", func(r chi.Router) {
			r.Use(f.member)
			r.Get("/branches", f.branches)
			r.Get("/tags/*", f.tag)
			r.Get("/commits/{commit}", f.commit)
			r.Get("/raw/*", f.raw)
			r.Get("/compare/changes", f.changes)
		})
	})
	return r
}

type ctxKey int

const (
	userKey ctxKey = iota
	repoKey
)

// faults counts each request and answers it as the fault set.
func (f *Fake) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		fault, until := f.fault, f.until
		if !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, API), f.scope) {
			fault = None
		}
		f.mu.Unlock()
		switch fault {
		case RateLimited:
			w.Header().Set("X-RateLimit-Limit", "60")
			w.Header().Set("X-RateLimit-Interval-Seconds", "1")
			w.Header().Set("X-RateLimit-Fill-Rate", "5")
			answer(w, http.StatusTooManyRequests, problem("Too many requests"))
		case TooManyRequests:
			w.Header().Set("Retry-After",
				strconv.Itoa(int(time.Until(until).Round(time.Second).Seconds())))
			answer(w, http.StatusTooManyRequests, problem("Too many requests"))
		case ServerError:
			answer(w, http.StatusBadGateway, problem("Bad Gateway"))
		case Hang:
			<-r.Context().Done()
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// authenticated admits a request whose bearer token the fake issued.
func (f *Fake) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		u, known := f.tokens[tok]
		f.mu.Unlock()
		if !ok || !known {
			answer(
				w,
				http.StatusUnauthorized,
				problem("Authentication failed. Please check your credentials and try again."),
			)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

// member admits a request for a repository the user holds a role on,
// answering 404 for any other, as Bitbucket hides a repository a user
// cannot read.
func (f *Fake) member(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.Context().Value(userKey).(*user) //nolint:forcetypeassert // authenticated sets it
		key := chi.URLParam(r, "project") + "/" + chi.URLParam(r, "slug")
		f.mu.Lock()
		repo, ok := f.repos[key]
		if ok {
			_, ok = repo.roles[u.name]
		}
		f.mu.Unlock()
		if !ok {
			answer(w, http.StatusNotFound, problem("Repository "+key+" does not exist."))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), repoKey, repo)))
	})
}

// readable lists, sorted, the repositories u holds least or above on.
func (f *Fake) readable(u *user, least Role) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, key := range slices.Sorted(maps.Keys(f.repos)) {
		repo := f.repos[key]
		if role := repo.roles[u.name]; role >= least {
			out = append(out, map[string]any{
				"slug": repo.slug, "name": repo.slug, "scmId": "git", "public": false,
				"project": map[string]any{"key": repo.project, "name": repo.project},
			})
		}
	}
	return out
}

// recent lists the repositories the user reads, as its recently viewed
// ones; it answers only an authenticated user.
func (f *Fake) recent(w http.ResponseWriter, r *http.Request) {
	u := r.Context().Value(userKey).(*user) //nolint:forcetypeassert // authenticated sets it
	paged(w, r, f.readable(u, Read))
}

// list lists the repositories the user holds the permission parameter's
// role or above on.
func (f *Fake) list(w http.ResponseWriter, r *http.Request) {
	u := r.Context().Value(userKey).(*user) //nolint:forcetypeassert // authenticated sets it
	least, ok := permissions[r.URL.Query().Get("permission")]
	if !ok {
		answer(w, http.StatusBadRequest, problem("Unknown permission"))
		return
	}
	paged(w, r, f.readable(u, least))
}

// branches lists the repository's branches whose name holds filterText.
func (f *Fake) branches(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	bs, err := repo.repo.Branches()
	if err != nil {
		answer(w, http.StatusInternalServerError, problem(err.Error()))
		return
	}
	filter := r.URL.Query().Get("filterText")
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		if strings.Contains(b.Name, filter) {
			out = append(out, map[string]any{
				"id": "refs/heads/" + b.Name, "displayId": b.Name, "type": "BRANCH",
				"latestCommit": b.Commit, "latestChangeset": b.Commit,
			})
		}
	}
	paged(w, r, out)
}

func (f *Fake) tag(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	name := rest(r)
	sha, _, c, err := repo.repo.TagOf(name)
	if err != nil {
		answer(w, http.StatusNotFound, problem("Tag "+name+" does not exist."))
		return
	}
	answer(w, http.StatusOK, map[string]any{
		"id": "refs/tags/" + name, "displayId": name, "type": "TAG",
		"latestCommit": c.Hash.String(), "hash": sha,
	})
}

// commit resolves a full SHA, or the prefix of one, as Bitbucket does.
func (f *Fake) commit(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	want := chi.URLParam(r, "commit")
	c, err := repo.repo.Resolve(want)
	if err != nil {
		bs, _ := repo.repo.Branches() //nolint:errcheck // a failure lists none
		for _, b := range bs {
			if strings.HasPrefix(b.Commit, want) {
				c, err = repo.repo.Resolve(b.Commit)
			}
		}
	}
	if err != nil {
		answer(w, http.StatusNotFound, problem("Commit '"+want+"' does not exist in repository."))
		return
	}
	answer(w, http.StatusOK, f.commitJSON(c))
}

// raw answers a file at the commit the at parameter names.
func (f *Fake) raw(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	b, err := repo.repo.File(r.URL.Query().Get("at"), rest(r))
	if err != nil {
		answer(w, http.StatusNotFound, problem("The path does not exist at the revision."))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	//nolint:gosec // a file's bytes as text/plain with nosniff, never HTML
	_, _ = w.Write(b)
}

// changes lists the files that differ between the commits from and to,
// from being the one compared, as Bitbucket names them.
func (f *Fake) changes(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	q := r.URL.Query()
	changes, err := repo.repo.Changes(r.Context(), q.Get("to"), q.Get("from"))
	if q.Get("from") == "" || q.Get("to") == "" || err != nil {
		answer(w, http.StatusNotFound, problem("A commit does not exist in repository."))
		return
	}
	path := func(p string) any {
		if p == "" {
			return nil
		}
		return map[string]any{"toString": p, "name": p[strings.LastIndex(p, "/")+1:]}
	}
	out := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		typ, at, src := "MODIFY", ch.To, ""
		switch {
		case ch.From == "":
			typ = "ADD"
		case ch.To == "":
			typ, at = "DELETE", ch.From
		case ch.From != ch.To:
			typ, src = "MOVE", ch.From
		}
		c := map[string]any{"type": typ, "path": path(at), "nodeType": "FILE"}
		if src != "" {
			c["srcPath"] = path(src)
		}
		out = append(out, c)
	}
	paged(w, r, out)
}

// commitJSON is c as Bitbucket answers a commit, its author the user of
// its e-mail when the fake has one.
func (f *Fake) commitJSON(c *object.Commit) map[string]any {
	author := map[string]any{"name": c.Author.Name, "emailAddress": c.Author.Email}
	f.mu.Lock()
	for _, u := range f.users {
		if u.email() == c.Author.Email {
			author = map[string]any{
				"name": u.name, "emailAddress": u.email(), "id": u.id,
				"displayName": u.name, "slug": u.name, "type": "NORMAL", "active": true,
			}
		}
	}
	f.mu.Unlock()
	parents := make([]map[string]string, 0, len(c.ParentHashes))
	for _, h := range c.ParentHashes {
		parents = append(parents, map[string]string{"id": h.String(), "displayId": h.String()[:11]})
	}
	return map[string]any{
		"id": c.Hash.String(), "displayId": c.Hash.String()[:11], "message": c.Message,
		"author": author, "authorTimestamp": c.Author.When.UnixMilli(),
		"committer": author, "committerTimestamp": c.Committer.When.UnixMilli(),
		"parents": parents,
	}
}

// paged answers the page of items the request's start and limit name, in
// Bitbucket's envelope.
func paged(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = 25
	}
	start, err := strconv.Atoi(q.Get("start"))
	if err != nil || start < 0 {
		start = 0
	}
	start = min(start, len(items))
	end := min(start+limit, len(items))
	if items == nil {
		items = []map[string]any{}
	}
	body := map[string]any{
		"size": end - start, "limit": limit, "start": start,
		"isLastPage": end >= len(items), "values": items[start:end],
	}
	if end < len(items) {
		body["nextPageStart"] = end
	}
	answer(w, http.StatusOK, body)
}

// rest is the route's trailing wildcard, URL-decoded.
func rest(r *http.Request) string {
	v := chi.URLParam(r, "*")
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}

func problem(m string) map[string]any {
	return map[string]any{"errors": []map[string]any{{"context": nil, "message": m}}}
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// secret is 16 random bytes in hex.
func secret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
