package bitbucketcloudfake

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

// Fault is how the fake answers every request while it is set.
type Fault int

const (
	// None answers each request.
	None Fault = iota
	// RateLimited answers 429 with Bitbucket's X-RateLimit headers, which
	// name no reset, as Bitbucket's rate limits do.
	RateLimited
	// TooManyRequests answers 429 with Retry-After, the seconds until the
	// limit lifts, as a proxy in front of Bitbucket does.
	TooManyRequests
	// ServerError answers 502.
	ServerError
	// Hang answers nothing until the request's context ends.
	Hang
)

// Role is a permission an account holds on a repository, as Bitbucket
// names them.
type Role int

const (
	// Read reads the repository.
	Read Role = iota + 1
	// Write pushes to it.
	Write
	// Admin administers it, its webhooks included.
	Admin
)

// Author is the name and e-mail every seeded commit carries.
const (
	AuthorName  = gitmem.AuthorName
	AuthorEmail = gitmem.AuthorEmail
)

// Repo is a repository, held in memory by go-git, so refs, annotated
// tags, trees and their differences behave as Git's do.
type Repo = gitmem.Repo

// account is an Atlassian account, named by its e-mail.
type account struct {
	id    string
	email string
}

// nickname is a's Bitbucket nickname, its e-mail's local part.
func (a *account) nickname() string { n, _, _ := strings.Cut(a.email, "@"); return n }

// repository is a repository in a workspace, with its accounts' roles by
// e-mail.
type repository struct {
	workspace string
	slug      string
	repo      *Repo
	roles     map[string]Role
}

// Fake is a Bitbucket Cloud API: accounts, their API tokens, and
// repositories in workspaces with the roles accounts hold on them, served
// under /2.0.
type Fake struct {
	mu       sync.Mutex
	accounts map[string]*account
	tokens   map[string]*account
	repos    map[string]*repository
	nextID   int
	fault    Fault
	scope    string
	until    time.Time
	requests int
}

// New returns a fake with no account, token or repository.
func New() *Fake {
	return &Fake{
		accounts: map[string]*account{}, tokens: map[string]*account{},
		repos: map[string]*repository{},
	}
}

// Server is the fake served over TLS.
type Server struct {
	// URL is the server's base URL, the host commits and clones link to.
	URL string
	// APIHost is the API's base URL, URL under /2.0, which the
	// bitbucketcloud provider's Options name.
	APIHost string
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
	return &Server{URL: srv.URL, APIHost: srv.URL + "/2.0", CA: ca, srv: srv}
}

// accountOf is the account email, created the first time. f.mu is held.
func (f *Fake) accountOf(email string) *account {
	a, ok := f.accounts[email]
	if !ok {
		f.nextID++
		a = &account{id: "557058:" + strconv.Itoa(f.nextID), email: email}
		f.accounts[email] = a
	}
	return a
}

// AddToken issues an API token of the account email, created the first
// time, and returns it. A request authenticates with email and the token.
func (f *Fake) AddToken(email string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := "ATATT" + secret()
	f.tokens[tok] = f.accountOf(email)
	return tok
}

// Repo returns the repository slug in workspace, created empty the first
// time.
func (f *Fake) Repo(workspace, slug string) *Repo {
	return f.repositoryOf(workspace, slug).repo
}

func (f *Fake) repositoryOf(workspace, slug string) *repository {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := workspace + "/" + slug
	if r, ok := f.repos[key]; ok {
		return r
	}
	r := &repository{workspace: workspace, slug: slug, repo: gitmem.New(), roles: map[string]Role{}}
	f.repos[key] = r
	return r
}

// Grant gives the account email, created the first time, role on the
// repository slug in workspace, which makes it a member of workspace.
func (f *Fake) Grant(workspace, slug, email string, role Role) {
	r := f.repositoryOf(workspace, slug)
	f.mu.Lock()
	defer f.mu.Unlock()
	r.roles[f.accountOf(email).email] = role
}

// Fail answers every request with fault until it is set to None, a rate
// limit lifting after.
func (f *Fake) Fail(fault Fault, after time.Duration) { f.FailPath("", fault, after) }

// FailPath is Fail for the requests whose path under /2.0 starts with
// prefix alone, such as a workspace's repositories while the account
// reads.
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

// Handler serves the fake's routes under /2.0. A branch, a tag and a file
// path keep their slashes, as Bitbucket names them.
func (f *Fake) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(f.faults)
	r.Route("/2.0", func(r chi.Router) {
		r.Use(f.authenticated)
		r.Get("/user", f.user)
		r.Get("/user/workspaces", f.workspaces)
		r.Get("/repositories/{workspace}", f.list)
		r.Route("/repositories/{workspace}/{slug}", func(r chi.Router) {
			r.Use(f.member)
			r.Get("/refs/branches", f.branches)
			r.Get("/refs/branches/*", f.branch)
			r.Get("/refs/tags/*", f.tag)
			r.Get("/commit/{commit}", f.commit)
			r.Get("/src/{commit}/*", f.src)
			r.Get("/diffstat/{spec}", f.diffstat)
		})
	})
	return r
}

type ctxKey int

const (
	accountKey ctxKey = iota
	repoKey
)

// faults counts each request and answers it as the fault set.
func (f *Fake) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		fault, until := f.fault, f.until
		if !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/2.0"), f.scope) {
			fault = None
		}
		f.mu.Unlock()
		switch fault {
		case RateLimited:
			w.Header().Set("X-RateLimit-Limit", "1000")
			w.Header().Set("X-RateLimit-Resource", "api-git")
			w.Header().Set("X-RateLimit-NearLimit", "true")
			answer(
				w,
				http.StatusTooManyRequests,
				problem("Rate limit for this resource has been exceeded"),
			)
		case TooManyRequests:
			w.Header().Set("Retry-After",
				strconv.Itoa(int(time.Until(until).Round(time.Second).Seconds())))
			answer(w, http.StatusTooManyRequests, problem("Too Many Requests"))
		case ServerError:
			answer(w, http.StatusBadGateway, problem("Bad Gateway"))
		case Hang:
			<-r.Context().Done()
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// authenticated admits a request whose basic credentials are an e-mail
// and an API token the fake issued that account.
func (f *Fake) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, tok, _ := r.BasicAuth()
		f.mu.Lock()
		a, ok := f.tokens[tok]
		f.mu.Unlock()
		if !ok || a.email != email {
			answer(w, http.StatusUnauthorized, problem("Invalid credentials"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey, a)))
	})
}

// member admits a request for a repository the account holds a role on,
// answering 404 for any other, as Bitbucket hides a private repository.
func (f *Fake) member(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := r.Context().Value(accountKey).(*account) //nolint:forcetypeassert // authenticated sets it
		f.mu.Lock()
		repo, ok := f.repos[chi.URLParam(r, "workspace")+"/"+chi.URLParam(r, "slug")]
		if ok {
			_, ok = repo.roles[a.email]
		}
		f.mu.Unlock()
		if !ok {
			answer(w, http.StatusNotFound, problem("Repository not found"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), repoKey, repo)))
	})
}

func (f *Fake) user(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(accountKey).(*account) //nolint:forcetypeassert // authenticated sets it
	answer(w, http.StatusOK, map[string]any{
		"type": "user", "account_id": a.id, "uuid": "{" + a.id + "}",
		"display_name": a.nickname(), "nickname": a.nickname(),
	})
}

// workspaces lists the workspaces the account holds a role in, by slug.
func (f *Fake) workspaces(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(accountKey).(*account) //nolint:forcetypeassert // authenticated sets it
	f.mu.Lock()
	seen := map[string]bool{}
	for _, repo := range f.repos {
		if _, ok := repo.roles[a.email]; ok {
			seen[repo.workspace] = true
		}
	}
	f.mu.Unlock()
	out := make([]map[string]any, 0, len(seen))
	for _, ws := range slices.Sorted(maps.Keys(seen)) {
		out = append(out, map[string]any{
			"type": "workspace_access", "administrator": false,
			"workspace": map[string]any{"type": "workspace_base", "slug": ws, "name": ws},
		})
	}
	paged(w, r, out)
}

// list lists the repositories of a workspace the account holds role on:
// member, any; contributor, Write or above; admin, Admin.
func (f *Fake) list(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(accountKey).(*account) //nolint:forcetypeassert // authenticated sets it
	least := map[string]Role{"": Read, "member": Read, "contributor": Write, "admin": Admin}[r.URL.Query().Get("role")]
	ws := chi.URLParam(r, "workspace")
	f.mu.Lock()
	var out []map[string]any
	for _, key := range slices.Sorted(maps.Keys(f.repos)) {
		repo := f.repos[key]
		if role := repo.roles[a.email]; repo.workspace == ws && least > 0 && role >= least {
			out = append(out, map[string]any{
				"type": "repository", "slug": repo.slug, "name": repo.slug,
				"full_name": key, "is_private": true,
			})
		}
	}
	f.mu.Unlock()
	paged(w, r, out)
}

func (f *Fake) branches(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	bs, err := repo.repo.Branches()
	if err != nil {
		answer(w, http.StatusInternalServerError, problem(err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		out = append(out, map[string]any{
			"type": "branch", "name": b.Name, "target": map[string]any{"hash": b.Commit},
		})
	}
	paged(w, r, out)
}

func (f *Fake) branch(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	name := rest(r)
	bs, _ := repo.repo.Branches() //nolint:errcheck // a failure lists none
	for _, b := range bs {
		if b.Name == name {
			c, err := repo.repo.Resolve(b.Commit)
			if err == nil {
				answer(w, http.StatusOK, map[string]any{
					"type": "branch", "name": name, "target": f.commitJSON(r, repo, c),
				})
				return
			}
		}
	}
	answer(w, http.StatusNotFound, problem("Branch not found"))
}

func (f *Fake) tag(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	name := rest(r)
	_, msg, c, err := repo.repo.TagOf(name)
	if err != nil {
		answer(w, http.StatusNotFound, problem("Tag not found"))
		return
	}
	answer(w, http.StatusOK, map[string]any{
		"type": "tag", "name": name, "message": msg, "target": f.commitJSON(r, repo, c),
	})
}

// commit resolves a full SHA, or the prefix of one, as Bitbucket does.
func (f *Fake) commit(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	want := chi.URLParam(r, "commit")
	bs, _ := repo.repo.Branches() //nolint:errcheck // a failure lists none
	c, err := repo.repo.Resolve(want)
	if err != nil {
		for _, b := range bs {
			if strings.HasPrefix(b.Commit, want) {
				c, err = repo.repo.Resolve(b.Commit)
			}
		}
	}
	if err != nil {
		answer(w, http.StatusNotFound, problem("Commit not found"))
		return
	}
	answer(w, http.StatusOK, f.commitJSON(r, repo, c))
}

func (f *Fake) src(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	b, err := repo.repo.File(chi.URLParam(r, "commit"), rest(r))
	if err != nil {
		answer(w, http.StatusNotFound, problem("No such file or directory"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	//nolint:gosec // a file's bytes as text/plain with nosniff, never HTML
	_, _ = w.Write(b)
}

// diffstat lists the files that differ between the spec's two commits,
// head..base, as Bitbucket names a range.
func (f *Fake) diffstat(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	spec, _ := url.PathUnescape(chi.URLParam(r, "spec"))
	head, base, ok := strings.Cut(spec, "..")
	changes, err := repo.repo.Changes(r.Context(), base, head)
	if !ok || err != nil {
		answer(w, http.StatusNotFound, problem("Commit not found"))
		return
	}
	side := func(p string) any {
		if p == "" {
			return nil
		}
		return map[string]any{"path": p, "type": "commit_file"}
	}
	out := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		status := "modified"
		switch {
		case ch.From == "":
			status = "added"
		case ch.To == "":
			status = "removed"
		case ch.From != ch.To:
			status = "renamed"
		}
		out = append(out, map[string]any{
			"type": "diffstat", "status": status, "old": side(ch.From), "new": side(ch.To),
		})
	}
	paged(w, r, out)
}

// commitJSON is c as Bitbucket answers a commit, its author linked to the
// account of its e-mail when the fake has one.
func (f *Fake) commitJSON(r *http.Request, repo *repository, c *object.Commit) map[string]any {
	author := map[string]any{"type": "author", "raw": c.Author.Name + " <" + c.Author.Email + ">"}
	f.mu.Lock()
	if a, ok := f.accounts[c.Author.Email]; ok {
		author["user"] = map[string]any{
			"type":         "user",
			"account_id":   a.id,
			"display_name": a.nickname(),
			"nickname":     a.nickname(),
		}
	}
	f.mu.Unlock()
	parents := make([]map[string]string, 0, len(c.ParentHashes))
	for _, h := range c.ParentHashes {
		parents = append(parents, map[string]string{"hash": h.String()})
	}
	return map[string]any{
		"type": "commit", "hash": c.Hash.String(), "message": c.Message,
		"date": c.Author.When.UTC().Format(time.RFC3339), "author": author, "parents": parents,
		"links": map[string]any{"html": map[string]string{
			"href": "https://" + r.Host + "/" + repo.workspace + "/" + repo.slug + "/commits/" + c.Hash.String(),
		}},
	}
}

// paged answers the page of items the request's page and pagelen name,
// in Bitbucket's envelope, its next link carrying the request's query.
func paged(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	q := r.URL.Query()
	per, err := strconv.Atoi(q.Get("pagelen"))
	if err != nil || per <= 0 {
		per = 10
	}
	n, err := strconv.Atoi(q.Get("page"))
	if err != nil || n <= 0 {
		n = 1
	}
	start := min((n-1)*per, len(items))
	end := min(start+per, len(items))
	body := map[string]any{
		"pagelen": per, "page": n, "size": len(items), "values": items[start:end],
	}
	if end < len(items) {
		q.Set("page", strconv.Itoa(n+1))
		body["next"] = "https://" + r.Host + r.URL.EscapedPath() + "?" + q.Encode()
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
	return map[string]any{"type": "error", "error": map[string]string{"message": m}}
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
