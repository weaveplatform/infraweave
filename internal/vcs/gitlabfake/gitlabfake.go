package gitlabfake

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/gitmem"
)

// Fault is how the fake answers every request while it is set.
type Fault int

const (
	// None answers each request.
	None Fault = iota
	// RateLimited answers 429 with RateLimit-Remaining 0, the reset time
	// in RateLimit-Reset and the seconds until it in Retry-After, as
	// GitLab's rate limits do.
	RateLimited
	// TooManyRequests answers 429 with Retry-After alone, as a proxy in
	// front of GitLab does.
	TooManyRequests
	// ServerError answers 502.
	ServerError
	// Hang answers nothing until the request's context ends.
	Hang
)

// The access levels a member of a project holds, as GitLab numbers them.
const (
	Guest      = 10
	Reporter   = 20
	Developer  = 30
	Maintainer = 40
	Owner      = 50
)

// BotRefusal is how GitLab words its refusal of a project access token
// to a bot user, after "400 Bad request - ".
const BotRefusal = "User does not have permission to create project access token"

// Author is the name and e-mail every seeded commit carries.
const (
	AuthorName  = gitmem.AuthorName
	AuthorEmail = gitmem.AuthorEmail
)

// Repo is a project's repository, held in memory by go-git, so refs,
// annotated tags, trees and their differences behave as Git's do.
type Repo = gitmem.Repo

// user is a user tokens act as; bot marks the bot user of a group or
// project access token.
type user struct {
	id       int64
	username string
	bot      bool
}

// token is an access token of a user, with its scopes; mint is the
// record of a project access token the fake minted, nil for one AddToken
// issued.
type token struct {
	id     int64
	user   *user
	scopes []string
	mint   *Minted
}

// Minted is a project access token the fake minted, as its request named
// it: the project's full path, the token's name, scopes, access level and
// expiry date, and whether it has been revoked since.
type Minted struct {
	ID          int64
	Project     string
	Name        string
	Scopes      []string
	AccessLevel int
	ExpiresAt   string
	Revoked     bool
}

// project is a project in a namespace, with its members' access levels
// by user ID.
type project struct {
	id        int64
	namespace string
	path      string
	repo      *Repo
	members   map[int64]int
}

func (p *project) fullPath() string { return p.namespace + "/" + p.path }

// Fake is a GitLab API: users, their access tokens, and projects in groups
// and subgroups with their members, served under /api/v4.
type Fake struct {
	mu       sync.Mutex
	users    map[string]*user
	tokens   map[string]*token
	projects map[string]*project
	minted   []*Minted
	nextID   int64
	fault    Fault
	scope    string
	until    time.Time
	requests int
}

// New returns a fake with no user, token or project.
func New() *Fake {
	return &Fake{
		users: map[string]*user{}, tokens: map[string]*token{}, projects: map[string]*project{},
		nextID: 100,
	}
}

// Server is the fake served over TLS.
type Server struct {
	// URL is the server's base URL, an API host the gitlab provider
	// reaches under /api/v4.
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

// userOf is the user username, created the first time. f.mu is held.
func (f *Fake) userOf(username string) *user {
	u, ok := f.users[username]
	if !ok {
		f.nextID++
		u = &user{id: f.nextID, username: username}
		f.users[username] = u
	}
	return u
}

// AddToken issues an access token acting as username, created the first
// time, holding scopes, such as api or read_api, and returns it.
func (f *Fake) AddToken(username string, scopes ...string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	tok := "glpat-" + secret()
	f.tokens[tok] = &token{id: f.nextID, user: f.userOf(username), scopes: scopes}
	return tok
}

// AddBotToken issues a token acting as the bot user username, created
// the first time, holding scopes, as a group or project access token
// does, and returns it.
func (f *Fake) AddBotToken(username string, scopes ...string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	tok := "glpat-" + secret()
	u := f.userOf(username)
	u.bot = true
	f.tokens[tok] = &token{id: f.nextID, user: u, scopes: scopes}
	return tok
}

// Repo returns the repository of the project name in namespace, a group
// or a subgroup's full path such as acme/platform, created empty the
// first time.
func (f *Fake) Repo(namespace, name string) *Repo {
	return f.projectOf(namespace, name).repo
}

func (f *Fake) projectOf(namespace, name string) *project {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := namespace + "/" + name
	if p, ok := f.projects[key]; ok {
		return p
	}
	f.nextID++
	p := &project{
		id:        f.nextID,
		namespace: namespace,
		path:      name,
		repo:      gitmem.New(),
		members:   map[int64]int{},
	}
	f.projects[key] = p
	return p
}

// AddMember makes username, created the first time, a member of the
// project name in namespace at level, such as Maintainer.
func (f *Fake) AddMember(namespace, name, username string, level int) {
	p := f.projectOf(namespace, name)
	f.mu.Lock()
	defer f.mu.Unlock()
	p.members[f.userOf(username).id] = level
}

// Minted lists the project access tokens the fake has minted, in the
// order it minted them.
func (f *Fake) Minted() []Minted {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Minted, 0, len(f.minted))
	for _, m := range f.minted {
		c := *m
		c.Scopes = slices.Clone(m.Scopes)
		out = append(out, c)
	}
	return out
}

// Fail answers every request with fault until it is set to None, a rate
// limit lifting after.
func (f *Fake) Fail(fault Fault, after time.Duration) { f.FailPath("", fault, after) }

// FailPath is Fail for the requests whose path under /api/v4 starts with
// prefix alone, such as a token's scopes while the user reads.
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

// Handler serves the fake's routes under /api/v4. A project is named by
// its ID or its URL-encoded full path, a ref and a file path URL-encoded,
// as GitLab names them. A token with neither the api nor the read_api
// scope reads a project's repository routes alone, as read_repository
// allows.
func (f *Fake) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(f.faults)
	r.Route("/api/v4", func(r chi.Router) {
		r.Use(f.authenticated, scoped)
		r.Get("/user", f.user)
		r.Get("/personal_access_tokens/self", f.self)
		r.Get("/projects", f.list)
		r.Route("/projects/{id}", func(r chi.Router) {
			r.Use(f.member)
			r.Get("/", f.project)
			r.Get("/repository/commits/{sha}", f.commit)
			r.Get("/repository/tags/{tag}", f.tag)
			r.Get("/repository/files/{file}/raw", f.raw)
			r.Get("/repository/compare", f.compare)
			r.Get("/repository/branches", f.branches)
			r.Post("/access_tokens", f.mint)
			r.Delete("/access_tokens/{token}", f.revoke)
		})
	})
	return r
}

type ctxKey int

const (
	tokenKey ctxKey = iota
	projectKey
)

// faults counts each request and answers it as the fault set.
func (f *Fake) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		fault, until := f.fault, f.until
		if !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/api/v4"), f.scope) {
			fault = None
		}
		f.mu.Unlock()
		retry := strconv.Itoa(int(time.Until(until).Round(time.Second).Seconds()))
		switch fault {
		case RateLimited:
			w.Header().Set("RateLimit-Limit", "2000")
			w.Header().Set("RateLimit-Observed", "2001")
			w.Header().Set("RateLimit-Remaining", "0")
			w.Header().Set("RateLimit-Reset", strconv.FormatInt(until.Unix(), 10))
			w.Header().Set("RateLimit-ResetTime", until.UTC().Format(http.TimeFormat))
			w.Header().Set("Retry-After", retry)
			answer(w, http.StatusTooManyRequests, message("Retry later"))
		case TooManyRequests:
			w.Header().Set("Retry-After", retry)
			answer(w, http.StatusTooManyRequests, message("Retry later"))
		case ServerError:
			answer(w, http.StatusBadGateway, message("502 Bad Gateway"))
		case Hang:
			<-r.Context().Done()
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// authenticated admits a request bearing a token the fake issued, in
// PRIVATE-TOKEN or as a bearer token.
func (f *Fake) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("PRIVATE-TOKEN")
		if raw == "" {
			raw, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		f.mu.Lock()
		tok, ok := f.tokens[raw]
		f.mu.Unlock()
		if !ok {
			answer(w, http.StatusUnauthorized, message("401 Unauthorized"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey, tok)))
	})
}

// scoped refuses a token holding neither api nor read_api anything but
// reading a project's repository.
func scoped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Context().Value(tokenKey).(*token) //nolint:forcetypeassert // authenticated sets it
		rest, _ := strings.CutPrefix(r.URL.EscapedPath(), "/api/v4/projects/")
		_, rest, _ = strings.Cut(rest, "/")
		if !slices.Contains(tok.scopes, "api") && !slices.Contains(tok.scopes, "read_api") &&
			(r.Method != http.MethodGet || !strings.HasPrefix(rest, "repository/")) {
			answer(w, http.StatusForbidden, map[string]string{"error": "insufficient_scope"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// member admits a request for a project the token's user is a member of,
// answering 404 for any other, as GitLab hides what a token cannot see.
func (f *Fake) member(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Context().Value(tokenKey).(*token) //nolint:forcetypeassert // authenticated sets it
		id := param(r, "id")
		f.mu.Lock()
		p, ok := f.projects[id]
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			for _, q := range f.projects {
				if q.id == n {
					p, ok = q, true
				}
			}
		}
		if ok {
			_, ok = p.members[tok.user.id]
		}
		f.mu.Unlock()
		if !ok {
			answer(w, http.StatusNotFound, message("404 Project Not Found"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectKey, p)))
	})
}

func (f *Fake) user(w http.ResponseWriter, r *http.Request) {
	tok := r.Context().Value(tokenKey).(*token) //nolint:forcetypeassert // authenticated sets it
	answer(w, http.StatusOK, map[string]any{
		"id": tok.user.id, "username": tok.user.username, "name": tok.user.username,
		"state": "active", "web_url": host(r) + "/" + tok.user.username,
	})
}

func (f *Fake) self(w http.ResponseWriter, r *http.Request) {
	tok := r.Context().Value(tokenKey).(*token) //nolint:forcetypeassert // authenticated sets it
	answer(w, http.StatusOK, map[string]any{
		"id": tok.id, "name": "infraweave", "revoked": false, "active": true,
		"scopes": tok.scopes, "user_id": tok.user.id, "expires_at": nil,
	})
}

// list lists the projects the token's user is a member of, at
// min_access_level or above when the request names one, ordered by ID.
func (f *Fake) list(w http.ResponseWriter, r *http.Request) {
	tok := r.Context().Value(tokenKey).(*token) //nolint:forcetypeassert // authenticated sets it
	least, _ := strconv.Atoi(r.URL.Query().Get("min_access_level"))
	f.mu.Lock()
	var mine []*project
	for _, key := range slices.Sorted(maps.Keys(f.projects)) {
		p := f.projects[key]
		if level, ok := p.members[tok.user.id]; ok && level >= least {
			mine = append(mine, p)
		}
	}
	f.mu.Unlock()
	slices.SortFunc(mine, func(a, b *project) int { return int(a.id - b.id) })
	out := make([]map[string]any, 0, len(mine))
	for _, p := range mine {
		out = append(out, projectJSON(r, p))
	}
	paged(w, r, out)
}

func (f *Fake) project(w http.ResponseWriter, r *http.Request) {
	answer(
		w,
		http.StatusOK,
		projectJSON(r, r.Context().Value(projectKey).(*project)),
	) //nolint:forcetypeassert // member sets it
}

func (f *Fake) commit(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	c, err := p.repo.Resolve(param(r, "sha"))
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Commit Not Found"))
		return
	}
	answer(w, http.StatusOK, commitJSON(r, p, c))
}

func (f *Fake) tag(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	name := param(r, "tag")
	sha, msg, c, err := p.repo.TagOf(name)
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Tag Not Found"))
		return
	}
	answer(w, http.StatusOK, map[string]any{
		"name": name, "message": msg, "target": sha, "commit": commitJSON(r, p, c),
		"release": nil, "protected": false,
	})
}

func (f *Fake) raw(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	b, err := p.repo.File(r.URL.Query().Get("ref"), param(r, "file"))
	if err != nil {
		answer(w, http.StatusNotFound, message("404 File Not Found"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	//nolint:gosec // a file's bytes as text/plain with nosniff, never HTML
	_, _ = w.Write(b)
}

// compare lists the diffs between from and to, with no pagination, as
// GitLab's comparison answers.
func (f *Fake) compare(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	q := r.URL.Query()
	changes, err := p.repo.Changes(r.Context(), q.Get("from"), q.Get("to"))
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Ref Not Found"))
		return
	}
	diffs := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		diffs = append(diffs, map[string]any{
			"old_path": or(ch.From, ch.To), "new_path": or(ch.To, ch.From),
			"new_file": ch.From == "", "deleted_file": ch.To == "",
			"renamed_file": ch.From != "" && ch.To != "" && ch.From != ch.To,
		})
	}
	answer(w, http.StatusOK, map[string]any{
		"diffs": diffs, "compare_timeout": false, "compare_same_ref": q.Get("from") == q.Get("to"),
	})
}

func (f *Fake) branches(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	bs, err := p.repo.Branches()
	if err != nil {
		answer(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		out = append(
			out,
			map[string]any{"name": b.Name, "commit": map[string]string{"id": b.Commit}},
		)
	}
	paged(w, r, out)
}

// mint mints a project access token whose bot user is a member of the
// project alone, at the level the request names, as a token with the api
// scope and Maintainer or above may.
func (f *Fake) mint(w http.ResponseWriter, r *http.Request) {
	tok := r.Context().Value(tokenKey).(*token)   //nolint:forcetypeassert // authenticated sets it
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	// GitLab's field names are snake case.
	var in struct {
		Name        string   `json:"name"`
		Scopes      []string `json:"scopes"`
		AccessLevel int      `json:"access_level"` //nolint:tagliatelle // as above
		ExpiresAt   string   `json:"expires_at"`   //nolint:tagliatelle // as above
	}
	bad := json.NewDecoder(r.Body).Decode(&in) != nil
	exp, err := time.Parse(time.DateOnly, in.ExpiresAt)
	f.mu.Lock()
	defer f.mu.Unlock()
	level := p.members[tok.user.id]
	if !slices.Contains(tok.scopes, "api") || level < Maintainer {
		answer(w, http.StatusForbidden, message("403 Forbidden"))
		return
	}
	// GitLab refuses a bot user with 400, not 403, whatever its level.
	if tok.user.bot {
		answer(w, http.StatusBadRequest,
			message("400 Bad request - "+BotRefusal))
		return
	}
	if bad || err != nil || in.Name == "" || len(in.Scopes) == 0 || in.AccessLevel > level ||
		!exp.After(time.Now()) {
		answer(w, http.StatusBadRequest, message("400 Bad request"))
		return
	}
	f.nextID++
	bot := &user{
		id: f.nextID, username: fmt.Sprintf("project_%d_bot_%s", p.id, secret()[:8]), bot: true,
	}
	f.users[bot.username] = bot
	p.members[bot.id] = in.AccessLevel
	m := &Minted{
		ID: f.nextID, Project: p.fullPath(), Name: in.Name, Scopes: in.Scopes,
		AccessLevel: in.AccessLevel, ExpiresAt: in.ExpiresAt,
	}
	f.minted = append(f.minted, m)
	raw := "glpat-" + secret()
	f.tokens[raw] = &token{id: m.ID, user: bot, scopes: in.Scopes, mint: m}
	answer(w, http.StatusCreated, map[string]any{
		"id": m.ID, "name": m.Name, "scopes": m.Scopes, "access_level": m.AccessLevel,
		"expires_at": m.ExpiresAt, "user_id": bot.id, "active": true, "revoked": false,
		"token": raw,
	})
}

// revoke revokes the project's minted token the route names, removing its
// bot user from the project, as a token with the api scope and Maintainer
// or above may; one the project never minted, or revoked already, is 404.
func (f *Fake) revoke(w http.ResponseWriter, r *http.Request) {
	tok := r.Context().Value(tokenKey).(*token)   //nolint:forcetypeassert // authenticated sets it
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	id, _ := strconv.ParseInt(chi.URLParam(r, "token"), 10, 64)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(tok.scopes, "api") || p.members[tok.user.id] < Maintainer {
		answer(w, http.StatusForbidden, message("403 Forbidden"))
		return
	}
	for raw, t := range f.tokens {
		if t.mint != nil && t.mint.ID == id && t.mint.Project == p.fullPath() {
			delete(f.tokens, raw)
			delete(p.members, t.user.id)
			t.mint.Revoked = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	answer(w, http.StatusNotFound, message("404 Not found"))
}

func projectJSON(r *http.Request, p *project) map[string]any {
	return map[string]any{
		"id": p.id, "name": p.path, "path": p.path, "path_with_namespace": p.fullPath(),
		"web_url": host(r) + "/" + p.fullPath(), "visibility": "private",
		"namespace": map[string]any{
			"name": path.Base(p.namespace), "path": path.Base(p.namespace),
			"full_path": p.namespace, "kind": "group",
		},
	}
}

func commitJSON(r *http.Request, p *project, c *object.Commit) map[string]any {
	parents := make([]string, 0, len(c.ParentHashes))
	for _, h := range c.ParentHashes {
		parents = append(parents, h.String())
	}
	title, _, _ := strings.Cut(c.Message, "\n")
	when := c.Author.When.UTC().Format(time.RFC3339)
	return map[string]any{
		"id":              c.Hash.String(),
		"short_id":        c.Hash.String()[:8],
		"title":           title,
		"message":         c.Message,
		"author_name":     c.Author.Name,
		"author_email":    c.Author.Email,
		"authored_date":   when,
		"committer_name":  c.Committer.Name,
		"committer_email": c.Committer.Email,
		"committed_date":  when,
		"created_at":      when,
		"parent_ids":      parents,
		"web_url":         fmt.Sprintf("%s/%s/-/commit/%s", host(r), p.fullPath(), c.Hash),
	}
}

// paged answers the page of items the request's page and per_page name,
// with GitLab's offset pagination headers.
func paged(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	q := r.URL.Query()
	per, err := strconv.Atoi(q.Get("per_page"))
	if err != nil || per <= 0 {
		per = 20
	}
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	start := min((page-1)*per, len(items))
	end := min(start+per, len(items))
	pages := max((len(items)+per-1)/per, 1)
	w.Header().Set("X-Page", strconv.Itoa(page))
	w.Header().Set("X-Per-Page", strconv.Itoa(per))
	w.Header().Set("X-Total", strconv.Itoa(len(items)))
	w.Header().Set("X-Total-Pages", strconv.Itoa(pages))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(page + 1)
	}
	w.Header().Set("X-Next-Page", next)
	answer(w, http.StatusOK, items[start:end])
}

// param is the route's parameter name, URL-decoded: GitLab names a
// project's path, a ref and a file path with their slashes encoded.
func param(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func host(r *http.Request) string { return "https://" + r.Host }

func message(m string) map[string]string { return map[string]string{"message": m} }

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
