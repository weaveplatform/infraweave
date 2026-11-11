package githubfake

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/gitmem"
)

// Fault is how the fake answers every request while it is set.
type Fault int

const (
	// None answers each request.
	None Fault = iota
	// RateLimited answers 403 with X-RateLimit-Remaining 0 and the reset
	// time in X-RateLimit-Reset, GitHub's primary rate limit.
	RateLimited
	// TooManyRequests answers 429 with Retry-After, GitHub's secondary
	// rate limit.
	TooManyRequests
	// ServerError answers 502.
	ServerError
	// Hang answers nothing until the request's context ends.
	Hang
)

// Permissions and Events are what a new App holds unless a test changes
// them: everything the github provider needs.
var (
	Permissions = map[string]string{
		"checks": "write", "contents": "read", "metadata": "read",
		"pull_requests": "write", "statuses": "write",
	}
	Events = []string{"pull_request", "push"}
)

// App is a GitHub App the fake knows, with the PEM private key it signs
// its JWTs with.
type App struct {
	ID            int64
	Slug          string
	Name          string
	Owner         string
	PrivateKey    []byte
	WebhookSecret string
	Permissions   map[string]string
	Events        []string

	public *rsa.PublicKey
}

// Installation is an App's installation on an owner, reaching some of its
// repositories.
type Installation struct {
	ID        int64
	AppID     int64
	Owner     string
	OwnerType string
	Repos     []string
}

// TokenRequest is a request for an installation token as the fake
// received it: the installation, and the repositories and permissions the
// request narrowed the token to, nil when it named none.
type TokenRequest struct {
	Installation int64
	Repositories []string
	Permissions  map[string]string
}

// token is what an installation token reaches: its installation's
// repositories, or repos alone when it is not nil, with perms, or its
// App's permissions when nil.
type token struct {
	install int64
	repos   []string
	perms   map[string]string
}

// Fake is a GitHub API: Apps, their installations and tokens, and
// repositories, served under /api/v3 as GitHub Enterprise Server serves
// them.
type Fake struct {
	mu       sync.Mutex
	apps     map[int64]*App
	installs map[int64]*Installation
	repos    map[string]*Repo
	tokens   map[string]token
	minted   []TokenRequest
	codes    map[string]string
	nextID   int64
	fault    Fault
	scope    string
	until    time.Time
	requests int
	issued   int
	logins   map[string]string
}

// New returns a fake with no App, installation or repository, whose
// account AuthorLogin owns the e-mail AuthorEmail.
func New() *Fake {
	return &Fake{
		apps: map[int64]*App{}, installs: map[int64]*Installation{}, repos: map[string]*Repo{},
		tokens: map[string]token{}, codes: map[string]string{}, nextID: 1000,
		logins: map[string]string{AuthorEmail: AuthorLogin},
	}
}

// Unlink makes email belong to no GitHub account, so a commit it
// authored answers with a null author, as GitHub's own does.
func (f *Fake) Unlink(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.logins, email)
}

// Server is the fake served over TLS.
type Server struct {
	// URL is the server's base URL, an API host the github provider
	// reaches under /api/v3.
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

// AddApp registers an App owned by owner with a new 2048-bit key, the
// default Permissions and Events, and returns it.
func (f *Fake) AddApp(slug, owner string) App {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	must(err)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	a := &App{
		ID: f.nextID, Slug: slug, Name: slug, Owner: owner,
		PrivateKey: pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		}),
		WebhookSecret: secret(),
		Permissions:   maps.Clone(Permissions),
		Events:        slices.Clone(Events),
		public:        &key.PublicKey,
	}
	f.apps[a.ID] = a
	return *a
}

// SetApp changes app's permissions and events.
func (f *Fake) SetApp(app int64, permissions map[string]string, events []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apps[app].Permissions, f.apps[app].Events = permissions, events
}

// Install installs app on owner, an Organization or a User, reaching
// repos, and returns the installation's ID.
func (f *Fake) Install(app int64, owner, ownerType string, repos ...string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.installs[f.nextID] = &Installation{
		ID: f.nextID, AppID: app, Owner: owner, OwnerType: ownerType, Repos: repos,
	}
	return f.nextID
}

// Repo returns owner's repository name, created empty the first time.
func (f *Fake) Repo(owner, name string) *Repo {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := owner + "/" + name
	if r, ok := f.repos[key]; ok {
		return r
	}
	r := gitmem.New()
	f.repos[key] = r
	return r
}

// ManifestCode registers a code that converts once into a new App owned
// by owner, as GitHub redirects back with after its manifest form.
func (f *Fake) ManifestCode(owner string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := secret()
	f.codes[code] = owner
	return code
}

// App returns the App id, false when the fake lacks it.
func (f *Fake) App(id int64) (App, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.apps[id]
	if !ok {
		return App{}, false
	}
	return *a, true
}

// Fail answers every request with fault until it is set to None, a rate
// limit lifting after.
func (f *Fake) Fail(fault Fault, after time.Duration) { f.FailPath("", fault, after) }

// FailPath is Fail for the requests whose path under /api/v3 starts with
// prefix alone, such as a token's while the installation it is for is
// found.
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

// Tokens counts the installation tokens the fake has issued.
func (f *Fake) Tokens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued
}

// TokenRequests are the installation token requests the fake has
// answered with a token, in the order it received them.
func (f *Fake) TokenRequests() []TokenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.minted)
}

// Handler serves the fake's routes under /api/v3.
func (f *Fake) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(f.faults)
	r.Route("/api/v3", func(r chi.Router) {
		r.Post("/app-manifests/{code}/conversions", f.convert)
		r.Group(func(r chi.Router) {
			r.Use(f.asApp)
			r.Get("/app", f.app)
			r.Get("/app/installations", f.installations)
			r.Post("/app/installations/{id}/access_tokens", f.accessToken)
			r.Get("/repos/{owner}/{repo}/installation", f.repoInstallation)
		})
		r.Group(func(r chi.Router) {
			r.Use(f.asInstallation)
			r.Get("/installation/repositories", f.installationRepos)
			r.Route("/repos/{owner}/{repo}", func(r chi.Router) {
				r.Use(f.reachable)
				r.Get("/commits/*", f.commit)
				r.Get("/git/ref/tags/*", f.tagRef)
				r.Get("/contents/*", f.contents)
				r.Get("/compare/*", f.compare)
				r.Get("/branches", f.branches)
			})
		})
	})
	return r
}

// errNoApp is a JWT whose issuer names no App the fake knows.
var errNoApp = errors.New("githubfake: no such App")

type ctxKey int

const (
	appKey ctxKey = iota
	installKey
	tokenKey
	repoKey
)

// faults counts each request and answers it as the fault set.
func (f *Fake) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		fault, until := f.fault, f.until
		if !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/api/v3"), f.scope) {
			fault = None
		}
		f.mu.Unlock()
		switch fault {
		case RateLimited:
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(until.Unix(), 10))
			answer(w, http.StatusForbidden, message("API rate limit exceeded"))
		case TooManyRequests:
			w.Header().
				Set("Retry-After", strconv.Itoa(int(time.Until(until).Round(time.Second).Seconds())))
			answer(w, http.StatusTooManyRequests, map[string]string{
				"message":           "You have exceeded a secondary rate limit.",
				"documentation_url": "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api#about-secondary-rate-limits",
			})
		case ServerError:
			answer(w, http.StatusBadGateway, message("Server Error"))
		case Hang:
			<-r.Context().Done()
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// asApp admits a request bearing a JWT an App signed, RS256, its issuer
// the App's ID.
func (f *Fake) asApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var app *App
		_, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			iss, err := t.Claims.GetIssuer()
			if err != nil {
				return nil, err //nolint:wrapcheck // jwt names the claim
			}
			id, err := strconv.ParseInt(iss, 10, 64)
			if err != nil {
				return nil, err //nolint:wrapcheck // strconv names the value
			}
			f.mu.Lock()
			app = f.apps[id]
			f.mu.Unlock()
			if app == nil {
				return nil, errNoApp
			}
			return app.public, nil
		}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired())
		if !ok || err != nil {
			answer(w, http.StatusUnauthorized, message("A JSON web token could not be decoded"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), appKey, app)))
	})
}

// asInstallation admits a request bearing an installation token.
func (f *Fake) asInstallation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(auth, "token ")
		if !ok {
			tok, _ = strings.CutPrefix(auth, "Bearer ")
		}
		f.mu.Lock()
		t := f.tokens[tok]
		in, ok := f.installs[t.install]
		f.mu.Unlock()
		if !ok {
			answer(w, http.StatusUnauthorized, message("Bad credentials"))
			return
		}
		ctx := context.WithValue(r.Context(), installKey, in)
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, tokenKey, t)))
	})
}

// reachable admits a request for a repository the installation reaches,
// and the token's repositories name when it is narrowed, answering 404
// for any other, as GitHub hides what a token cannot see; a token
// narrowed to permissions without contents is refused with 403, since
// every repository route reads contents.
func (f *Fake) reachable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, name := chi.URLParam(r, "owner"), chi.URLParam(r, "repo")
		in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation sets it
		t := r.Context().Value(tokenKey).(token)            //nolint:forcetypeassert // as above
		f.mu.Lock()
		repo, ok := f.repos[owner+"/"+name]
		f.mu.Unlock()
		if !ok || in.Owner != owner || !slices.Contains(in.Repos, name) ||
			t.repos != nil && !slices.Contains(t.repos, name) {
			answer(w, http.StatusNotFound, message("Not Found"))
			return
		}
		if t.perms != nil && t.perms["contents"] == "" {
			answer(w, http.StatusForbidden, message("Resource not accessible by integration"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), repoKey, repo)))
	})
}

func (f *Fake) app(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(appKey).(*App) //nolint:forcetypeassert // asApp sets it
	f.mu.Lock()
	defer f.mu.Unlock()
	answer(w, http.StatusOK, map[string]any{
		"id": a.ID, "slug": a.Slug, "name": a.Name, "owner": map[string]string{"login": a.Owner},
		"html_url":    host(r) + "/apps/" + a.Slug,
		"permissions": a.Permissions, "events": a.Events,
	})
}

func (f *Fake) installations(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(appKey).(*App) //nolint:forcetypeassert // asApp sets it
	f.mu.Lock()
	var out []map[string]any
	for _, id := range slices.Sorted(maps.Keys(f.installs)) {
		if in := f.installs[id]; in.AppID == a.ID {
			out = append(out, installation(r, in))
		}
	}
	f.mu.Unlock()
	paged(w, r, out, func(page []map[string]any) any { return page })
}

func (f *Fake) accessToken(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(appKey).(*App) //nolint:forcetypeassert // asApp sets it
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) > 0 && json.Unmarshal(raw, &body) != nil {
		answer(w, http.StatusBadRequest, message("Problems parsing JSON"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	in, ok := f.installs[id]
	if !ok || in.AppID != a.ID {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	for _, name := range body.Repositories {
		if !slices.Contains(in.Repos, name) {
			answer(
				w,
				http.StatusUnprocessableEntity,
				message(
					"There is at least one repository that does not exist or is not accessible",
				),
			)
			return
		}
	}
	for perm, level := range body.Permissions {
		if !covers(a.Permissions[perm], level) {
			answer(w, http.StatusUnprocessableEntity,
				message("The permissions requested are not granted to this installation."))
			return
		}
	}
	tok := "ghs_" + secret()
	f.tokens[tok] = token{install: id, repos: body.Repositories, perms: body.Permissions}
	f.minted = append(f.minted, TokenRequest{
		Installation: id, Repositories: body.Repositories, Permissions: body.Permissions,
	})
	f.issued++
	answer(w, http.StatusCreated, map[string]any{
		"token": tok, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

// covers reports whether an App's level of a permission grants want: a
// write grants a read.
func covers(held, want string) bool {
	return held == want || held == "write" && want == "read"
}

func (f *Fake) repoInstallation(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(appKey).(*App) //nolint:forcetypeassert // asApp sets it
	owner, name := chi.URLParam(r, "owner"), chi.URLParam(r, "repo")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(f.installs)) {
		in := f.installs[id]
		if in.AppID == a.ID && in.Owner == owner && slices.Contains(in.Repos, name) {
			if _, ok := f.repos[owner+"/"+name]; ok {
				answer(w, http.StatusOK, installation(r, in))
				return
			}
		}
	}
	answer(w, http.StatusNotFound, message("Not Found"))
}

func (f *Fake) installationRepos(w http.ResponseWriter, r *http.Request) {
	in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation sets it
	t := r.Context().Value(tokenKey).(token)            //nolint:forcetypeassert // as above
	names := in.Repos
	if t.repos != nil {
		names = t.repos
	}
	repos := make([]map[string]any, 0, len(names))
	for _, name := range names {
		repos = append(repos, map[string]any{
			"name": name, "full_name": in.Owner + "/" + name,
			"owner": map[string]string{"login": in.Owner},
		})
	}
	paged(w, r, repos, func(page []map[string]any) any {
		return map[string]any{"total_count": len(repos), "repositories": page}
	})
}

func (f *Fake) commit(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	ref := chi.URLParam(r, "*")
	c, err := repo.Resolve(ref)
	if err != nil {
		answer(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+ref))
		return
	}
	// GitHub names the account that owns the author's e-mail, and null
	// when none does.
	f.mu.Lock()
	var author map[string]string
	if login, ok := f.logins[c.Author.Email]; ok {
		author = map[string]string{"login": login}
	}
	f.mu.Unlock()
	answer(w, http.StatusOK, map[string]any{
		"sha": c.Hash.String(),
		"html_url": fmt.Sprintf("%s/%s/%s/commit/%s",
			host(r), chi.URLParam(r, "owner"), chi.URLParam(r, "repo"), c.Hash),
		"commit": map[string]any{
			"message": c.Message,
			"author": map[string]string{
				"name": c.Author.Name, "email": c.Author.Email,
				"date": c.Author.When.UTC().Format(time.RFC3339),
			},
		},
		"author": author,
	})
}

func (f *Fake) tagRef(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	tag := chi.URLParam(r, "*")
	sha, _, _, err := repo.TagOf(tag)
	if err != nil {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	answer(w, http.StatusOK, map[string]any{
		"ref": "refs/tags/" + tag, "object": map[string]string{"sha": sha, "type": "tag"},
	})
}

func (f *Fake) contents(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	b, err := repo.File(r.URL.Query().Get("ref"), chi.URLParam(r, "*"))
	if err != nil {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	w.Header().Set("Content-Type", "application/vnd.github.raw")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	//nolint:gosec // a file's bytes in GitHub's raw media type, never HTML
	_, _ = w.Write(b)
}

func (f *Fake) compare(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	base, head, ok := strings.Cut(chi.URLParam(r, "*"), "...")
	changes, err := repo.Changes(r.Context(), base, head)
	if !ok || err != nil {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	files := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		switch {
		case ch.To == "":
			files = append(files, map[string]any{"filename": ch.From, "status": "removed"})
		case ch.From != "" && ch.From != ch.To:
			files = append(files, map[string]any{
				"filename": ch.To, "previous_filename": ch.From, "status": "renamed",
			})
		default:
			files = append(files, map[string]any{"filename": ch.To})
		}
	}
	paged(w, r, files, func(page []map[string]any) any {
		return map[string]any{"status": "ahead", "files": page}
	})
}

func (f *Fake) branches(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	bs, err := repo.Branches()
	if err != nil {
		answer(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		out = append(
			out,
			map[string]any{"name": b.Name, "commit": map[string]string{"sha": b.Commit}},
		)
	}
	paged(w, r, out, func(page []map[string]any) any { return page })
}

// convert turns a manifest code into a new App, once, as GitHub's
// conversion does.
func (f *Fake) convert(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	f.mu.Lock()
	owner, ok := f.codes[code]
	delete(f.codes, code)
	f.mu.Unlock()
	if !ok {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	a := f.AddApp("infraweave-"+code[:8], owner)
	answer(w, http.StatusCreated, map[string]any{
		"id": a.ID, "slug": a.Slug, "name": a.Name, "owner": map[string]string{"login": owner},
		"html_url": host(r) + "/apps/" + a.Slug,
		"pem":      string(a.PrivateKey), "webhook_secret": a.WebhookSecret,
	})
}

func installation(r *http.Request, in *Installation) map[string]any {
	return map[string]any{
		"id": in.ID, "app_id": in.AppID,
		"account":  map[string]string{"login": in.Owner, "type": in.OwnerType},
		"html_url": fmt.Sprintf("%s/settings/installations/%d", host(r), in.ID),
	}
}

// paged answers the page of items the request's page and per_page name,
// linking the next page as GitHub's Link header does.
func paged(
	w http.ResponseWriter,
	r *http.Request,
	items []map[string]any,
	body func([]map[string]any) any,
) {
	q := r.URL.Query()
	per, err := strconv.Atoi(q.Get("per_page"))
	if err != nil || per <= 0 {
		per = 30
	}
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	start := min((page-1)*per, len(items))
	end := min(start+per, len(items))
	if end < len(items) {
		q.Set("page", strconv.Itoa(page+1))
		q.Set("per_page", strconv.Itoa(per))
		w.Header().
			Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next"`, host(r), r.URL.Path, q.Encode()))
	}
	if items == nil {
		items = []map[string]any{}
	}
	answer(w, http.StatusOK, body(items[start:end]))
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
