package entrafake

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Fault is how the fake answers a Graph request it fails.
type Fault int

const (
	// FaultNone answers the request.
	FaultNone Fault = iota
	// FaultServerError answers 500 InternalServerError.
	FaultServerError
	// FaultUnavailable answers 503 ServiceUnavailable with Retry-After, as
	// Graph answers when it sheds load.
	FaultUnavailable
	// FaultThrottle answers 429 TooManyRequests with Retry-After, Graph's
	// throttling.
	FaultThrottle
	// FaultCommitted creates the application a create asks for and then
	// answers 500 InternalServerError, as Graph does when it fails after
	// its write; any other request it answers 500 alone.
	FaultCommitted
)

// The grants a token request authenticates its client with.
const (
	GrantSecret    = "client_secret"
	GrantAssertion = "client_assertion"
)

// assertionType is the client_assertion_type of a federated assertion.
const assertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// The values Graph accepts as an application's signInAudience.
var audiences = []string{"AzureADMyOrg", "AzureADMultipleOrgs", "AzureADandPersonalMicrosoftAccount", "PersonalMicrosoftAccount"}

// ResourceAccess is a permission an application requires of a resource.
type ResourceAccess struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// RequiredResourceAccess is the permissions an application requires of
// the resource whose application ID is ResourceAppID.
type RequiredResourceAccess struct {
	ResourceAppID  string           `json:"resourceAppId"`
	ResourceAccess []ResourceAccess `json:"resourceAccess"`
}

// Application is a Graph application: ID is its object ID, AppID its
// application (client) ID, and Owner the client that created it.
type Application struct {
	ID                     string                   `json:"id"`
	AppID                  string                   `json:"appId"`
	DisplayName            string                   `json:"displayName"`
	SignInAudience         string                   `json:"signInAudience"`
	RequiredResourceAccess []RequiredResourceAccess `json:"requiredResourceAccess"`
	CreatedDateTime        time.Time                `json:"createdDateTime"`
	Owner                  string                   `json:"-"`
}

// Call is a request as the fake answered it: Tenant and Grant for a token
// request, Client the client the request authenticated as, empty when it
// did not, and Status the answer's HTTP status.
type Call struct {
	Method string
	Path   string
	Tenant string
	Client string
	Grant  string
	Scope  string
	Status int
}

// client is a registered client's credential: the secret or the assertion
// its token requests carry.
type client struct {
	secret, assertion string
}

// Fake is Entra's token endpoint and Graph's application routes.
type Fake struct {
	router chi.Router

	mu         sync.Mutex
	clients    map[string]map[string]client
	tokens     map[string]string
	apps       map[string]*Application
	unreplied  map[string]int
	replicas   int
	next       Fault
	nextN      int
	retryAfter int
	calls      []Call
}

// New returns a fake with no client and no application. Throttled and
// unavailable answers carry Retry-After: 1 until RetryAfter changes it.
func New() *Fake {
	f := &Fake{
		clients: map[string]map[string]client{}, tokens: map[string]string{},
		apps: map[string]*Application{}, unreplied: map[string]int{}, retryAfter: 1,
	}
	r := chi.NewRouter()
	r.Get("/{tenant}/v2.0/.well-known/openid-configuration", f.discovery)
	r.Post("/{tenant}/oauth2/v2.0/token", f.token)
	r.Route("/v1.0/applications", func(r chi.Router) {
		r.Post("/", f.create)
		r.Get("/", f.list)
		r.Get("/{id}", f.read)
		r.Delete("/{id}", f.remove)
	})
	f.router = r
	return f
}

// ServeHTTP answers a request to Entra or Graph.
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) { f.router.ServeHTTP(w, r) }

// AddSecret registers the client id of tenant, which authenticates with
// secret.
func (f *Fake) AddSecret(tenant, id, secret string) { f.add(tenant, id, client{secret: secret}) }

// AddAssertion registers the client id of tenant, which authenticates
// with the federated assertion, as workload identity does.
func (f *Fake) AddAssertion(tenant, id, assertion string) {
	f.add(tenant, id, client{assertion: assertion})
}

func (f *Fake) add(tenant, id string, c client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.clients[tenant] == nil {
		f.clients[tenant] = map[string]client{}
	}
	f.clients[tenant][id] = c
}

// FailNext answers the next n Graph requests with fault; token requests
// are never failed.
func (f *Fake) FailNext(fault Fault, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next, f.nextN = fault, n
}

// RetryAfter sets the seconds a throttled or unavailable answer's
// Retry-After names.
func (f *Fake) RetryAfter(seconds int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retryAfter = seconds
}

// DelayReplication makes each application created afterwards answer 404
// to its first n reads and deletes, as one Graph has not yet replicated
// does.
func (f *Fake) DelayReplication(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replicas = n
}

// Applications returns a copy of every application that exists, oldest
// first.
func (f *Fake) Applications() []Application {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Application, 0, len(f.apps))
	for _, a := range f.apps {
		c := *a
		c.RequiredResourceAccess = slices.Clone(a.RequiredResourceAccess)
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Application) int {
		if c := a.CreatedDateTime.Compare(b.CreatedDateTime); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Calls returns a copy of every request answered since New or Reset, in
// arrival order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Reset forgets the recorded calls and pending faults, keeping clients,
// tokens and applications.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
	f.next, f.nextN, f.replicas = FaultNone, 0, 0
	clear(f.unreplied)
}

func (f *Fake) record(c Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

// base is the fake's own URL, as the request reached it.
func base(r *http.Request) string { return "https://" + r.Host }

func (f *Fake) discovery(w http.ResponseWriter, r *http.Request) {
	tenant := chi.URLParam(r, "tenant")
	root := base(r) + "/" + tenant
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                root + "/v2.0",
		"authorization_endpoint":                root + "/oauth2/v2.0/authorize",
		"token_endpoint":                        root + "/oauth2/v2.0/token",
		"jwks_uri":                              root + "/discovery/v2.0/keys",
		"response_types_supported":              []string{"code", "id_token", "token"},
		"subject_types_supported":               []string{"pairwise"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"tenant_region_scope":                   "EU",
	})
	f.record(Call{Method: r.Method, Path: r.URL.Path, Tenant: tenant, Status: http.StatusOK})
}

// token answers a client credentials grant for a registered client that
// proves itself with its secret or its assertion.
func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	c := Call{Method: r.Method, Path: r.URL.Path, Tenant: chi.URLParam(r, "tenant")}
	defer func() { f.record(c) }()
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" {
		c.Status = oauthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"AADSTS70003: The app requested an unsupported grant type.")
		return
	}
	c.Scope = r.PostForm.Get("scope")
	id := r.PostForm.Get("client_id")
	secret, assertion := r.PostForm.Get("client_secret"), r.PostForm.Get("client_assertion")
	c.Grant = GrantSecret
	if assertion != "" {
		c.Grant = GrantAssertion
	}
	f.mu.Lock()
	known, ok := f.clients[c.Tenant][id]
	f.mu.Unlock()
	switch {
	case !ok:
		c.Status = oauthError(w, http.StatusBadRequest, "unauthorized_client",
			"AADSTS700016: Application with identifier '"+id+"' was not found in the directory.")
		return
	case assertion != "" && (r.PostForm.Get("client_assertion_type") != assertionType ||
		known.assertion == "" || assertion != known.assertion):
		c.Status = oauthError(w, http.StatusUnauthorized, "invalid_client",
			"AADSTS700213: No matching federated identity record found for presented assertion.")
		return
	case assertion == "" && (known.secret == "" || secret != known.secret):
		c.Status = oauthError(w, http.StatusUnauthorized, "invalid_client",
			"AADSTS7000215: Invalid client secret provided.")
		return
	}
	tok := "eyJ0eXAiOiJKV1QifQ." + random(24)
	f.mu.Lock()
	f.tokens[tok] = id
	f.mu.Unlock()
	c.Client, c.Status = id, http.StatusOK
	writeJSON(w, http.StatusOK, map[string]any{
		"token_type": "Bearer", "expires_in": 3599, "ext_expires_in": 3599, "access_token": tok,
	})
}

func oauthError(w http.ResponseWriter, status int, code, msg string) int {
	writeJSON(w, status, map[string]any{
		"error": code, "error_description": msg, "error_codes": []int{}, "trace_id": uuid.NewString(),
		"correlation_id": uuid.NewString(),
	})
	return status
}

// graph authenticates a Graph request and takes the fault it meets,
// answering it when either refuses it; ok is false once it has answered.
// A create that meets FaultCommitted is let through with committed set,
// for create to answer after its write.
func (f *Fake) graph(w http.ResponseWriter, r *http.Request, c *Call) (ok, committed bool) {
	tok, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	who, known := f.tokens[tok]
	fault := FaultNone
	if known && f.nextN > 0 {
		f.nextN--
		fault = f.next
	}
	retryAfter := f.retryAfter
	f.mu.Unlock()
	if !found || !known {
		c.Status = graphError(w, http.StatusUnauthorized, "InvalidAuthenticationToken",
			"Access token is empty or invalid.")
		return false, false
	}
	c.Client = who
	switch fault {
	case FaultCommitted:
		if r.Method == http.MethodPost {
			return true, true
		}
		c.Status = serverError(w)
	case FaultServerError:
		c.Status = serverError(w)
	case FaultUnavailable:
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		c.Status = graphError(w, http.StatusServiceUnavailable, "serviceNotAvailable",
			"The service is temporarily unavailable.")
	case FaultThrottle:
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		c.Status = graphError(w, http.StatusTooManyRequests, "TooManyRequests",
			"Too many requests from the application.")
	case FaultNone:
		return true, false
	}
	return false, false
}

func serverError(w http.ResponseWriter) int {
	return graphError(w, http.StatusInternalServerError, "InternalServerError", "An internal server error occurred.")
}

func graphError(w http.ResponseWriter, status int, code, msg string) int {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": msg,
		"innerError": map[string]any{"request-id": uuid.NewString(), "date": time.Now().UTC()},
	}})
	return status
}

// create creates an application from the body's displayName,
// signInAudience and requiredResourceAccess.
func (f *Fake) create(w http.ResponseWriter, r *http.Request) {
	c := Call{Method: r.Method, Path: r.URL.Path}
	defer func() { f.record(c) }()
	ok, committed := f.graph(w, r, &c)
	if !ok {
		return
	}
	var in Application
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		c.Status = graphError(w, http.StatusBadRequest, "BadRequest", "The request body is not JSON.")
		return
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		c.Status = graphError(w, http.StatusBadRequest, "Request_BadRequest",
			"Invalid value specified for property 'displayName' of resource 'Application'.")
		return
	}
	if in.SignInAudience == "" {
		in.SignInAudience = "AzureADMyOrg"
	}
	if !slices.Contains(audiences, in.SignInAudience) {
		c.Status = graphError(w, http.StatusBadRequest, "Request_BadRequest",
			"Invalid value specified for property 'signInAudience' of resource 'Application'.")
		return
	}
	app := &Application{
		ID: uuid.NewString(), AppID: uuid.NewString(), DisplayName: in.DisplayName,
		SignInAudience: in.SignInAudience, RequiredResourceAccess: in.RequiredResourceAccess,
		CreatedDateTime: time.Now().UTC(), Owner: c.Client,
	}
	if app.RequiredResourceAccess == nil {
		app.RequiredResourceAccess = []RequiredResourceAccess{}
	}
	f.mu.Lock()
	f.apps[app.ID] = app
	if f.replicas > 0 {
		f.unreplied[app.ID] = f.replicas
	}
	out := *app
	f.mu.Unlock()
	if committed {
		c.Status = serverError(w)
		return
	}
	c.Status = http.StatusCreated
	writeJSON(w, http.StatusCreated, out)
}

// list answers the applications a $filter of displayName eq a quoted
// string names, oldest first, as Graph's default query does; any other
// filter, or none, answers 400 Request_UnsupportedQuery. The listing sees
// an application DelayReplication holds back, as Graph's may.
func (f *Fake) list(w http.ResponseWriter, r *http.Request) {
	c := Call{Method: r.Method, Path: r.URL.Path}
	defer func() { f.record(c) }()
	if ok, _ := f.graph(w, r, &c); !ok {
		return
	}
	name, ok := displayNameEq(r.URL.Query().Get("$filter"))
	if !ok {
		c.Status = graphError(w, http.StatusBadRequest, "Request_UnsupportedQuery",
			"Unsupported or invalid query filter clause specified for property 'displayName' of resource 'Application'.")
		return
	}
	out := []Application{}
	for _, a := range f.Applications() {
		if a.DisplayName == name {
			out = append(out, a)
		}
	}
	c.Status = http.StatusOK
	writeJSON(w, http.StatusOK, map[string]any{"value": out})
}

// displayNameEq is the string filter compares displayName to with eq, an
// OData literal in single quotes that doubles a quote it holds.
func displayNameEq(filter string) (string, bool) {
	lit, ok := strings.CutPrefix(filter, "displayName eq '")
	if !ok || !strings.HasSuffix(lit, "'") {
		return "", false
	}
	lit = strings.TrimSuffix(lit, "'")
	if strings.Contains(strings.ReplaceAll(lit, "''", ""), "'") {
		return "", false
	}
	return strings.ReplaceAll(lit, "''", "'"), true
}

// find is the application id, or ok false, answered as Graph answers an
// object it does not have, when it does not exist or has yet to
// replicate.
func (f *Fake) find(w http.ResponseWriter, id string, c *Call) (Application, bool) {
	f.mu.Lock()
	app, ok := f.apps[id]
	if ok && f.unreplied[id] > 0 {
		f.unreplied[id]--
		ok = false
	}
	var out Application
	if ok {
		out = *app
	}
	f.mu.Unlock()
	if !ok {
		c.Status = graphError(w, http.StatusNotFound, "Request_ResourceNotFound",
			"Resource '"+id+"' does not exist or one of its queried reference-property objects are not present.")
	}
	return out, ok
}

func (f *Fake) read(w http.ResponseWriter, r *http.Request) {
	c := Call{Method: r.Method, Path: r.URL.Path}
	defer func() { f.record(c) }()
	if ok, _ := f.graph(w, r, &c); !ok {
		return
	}
	app, ok := f.find(w, chi.URLParam(r, "id"), &c)
	if !ok {
		return
	}
	c.Status = http.StatusOK
	writeJSON(w, http.StatusOK, app)
}

func (f *Fake) remove(w http.ResponseWriter, r *http.Request) {
	c := Call{Method: r.Method, Path: r.URL.Path}
	defer func() { f.record(c) }()
	if ok, _ := f.graph(w, r, &c); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, ok := f.find(w, id, &c); !ok {
		return
	}
	f.mu.Lock()
	delete(f.apps, id)
	f.mu.Unlock()
	c.Status = http.StatusNoContent
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client gone mid-answer is its own failure
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand never fails
	return hex.EncodeToString(b)
}

// Server is the fake served over TLS: URL is both the authority host and
// the Graph endpoint, and CA the PEM of the certificate it presents, for
// a client to trust.
type Server struct {
	URL string
	CA  []byte

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
	srv := httptest.NewTLSServer(f)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return &Server{URL: srv.URL, CA: ca, srv: srv}
}
