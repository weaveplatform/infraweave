package azure

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/cloud/entrafake"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
)

// The operator's tenant and application in the tests.
const (
	tenant = "11111111-2222-3333-4444-555555555555"
	client = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	secret = "s3cret"
)

// fixture is a fake Entra and Graph served over TLS with a client of it
// authenticating through the environment's client secret.
type fixture struct {
	fake *entrafake.Fake
	srv  *entrafake.Server
	set  Settings
}

func start(t *testing.T) *fixture {
	t.Helper()
	fake := entrafake.New()
	fake.AddSecret(tenant, client, secret)
	srv := fake.Start()
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, srv.CA, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "EnvironmentCredential")
	t.Setenv("AZURE_TENANT_ID", tenant)
	t.Setenv("AZURE_CLIENT_ID", client)
	t.Setenv("AZURE_CLIENT_SECRET", secret)
	return &fixture{fake: fake, srv: srv, set: Settings{
		TenantID: tenant, ClientID: client, AuthorityHost: srv.URL + "/", GraphEndpoint: srv.URL, CAFile: ca,
	}}
}

// client is a client with short delays, so retries cost milliseconds.
func (f *fixture) client() *Client {
	return New(f.set, Options{Backoff: time.Millisecond, Deadline: 5 * time.Second})
}

// graphCalls are the recorded Graph requests, token requests left out.
func (f *fixture) graphCalls() []entrafake.Call {
	var out []entrafake.Call
	for _, c := range f.fake.Calls() {
		if strings.HasPrefix(c.Path, "/v1.0/") {
			out = append(out, c)
		}
	}
	return out
}

func TestUnitCreateAndDelete(t *testing.T) {
	f := start(t)
	c := f.client()
	app, err := c.CreateApplication(t.Context(), "infraweave-acme-01")
	if err != nil {
		t.Fatal(err)
	}
	apps := f.fake.Applications()
	if len(apps) != 1 || apps[0].ID != app.ObjectID || apps[0].AppID != app.AppID ||
		apps[0].DisplayName != "infraweave-acme-01" || app.DisplayName != "infraweave-acme-01" {
		t.Fatalf("applications %+v, created %+v", apps, app)
	}
	got := apps[0]
	if got.SignInAudience != MultipleOrgs || got.Owner != client {
		t.Errorf("audience %q owner %q", got.SignInAudience, got.Owner)
	}
	if r := got.RequiredResourceAccess; len(r) != 1 || r[0].ResourceAppID != graphAppID ||
		len(r[0].ResourceAccess) != 1 || r[0].ResourceAccess[0].ID != userRead ||
		r[0].ResourceAccess[0].Type != "Scope" {
		t.Errorf("required resource access %+v", r)
	}
	var token entrafake.Call
	for _, call := range f.fake.Calls() {
		if strings.HasSuffix(call.Path, "/token") {
			token = call
		}
	}
	if token.Tenant != tenant || token.Grant != entrafake.GrantSecret || !strings.HasPrefix(token.Scope, f.srv.URL+"/.default") {
		t.Errorf("token request %+v", token)
	}
	if err := c.DeleteApplication(t.Context(), app.ObjectID); err != nil {
		t.Fatal(err)
	}
	if apps := f.fake.Applications(); len(apps) != 0 {
		t.Errorf("applications after the delete: %+v", apps)
	}
	// An application deleted already is deleted, after the replication
	// delay's retries.
	if err := c.DeleteApplication(t.Context(), app.ObjectID); err != nil {
		t.Fatalf("a second delete: %v", err)
	}
	if n := len(f.graphCalls()); n != 2+1+replicationRetries {
		t.Errorf("%d Graph calls, want %d", n, 3+replicationRetries)
	}
}

func TestUnitRetries(t *testing.T) {
	f := start(t)
	c := f.client()
	f.fake.RetryAfter(0)
	f.fake.FailNext(entrafake.FaultThrottle, 2)
	app, err := c.CreateApplication(t.Context(), "throttled")
	if err != nil {
		t.Fatalf("a throttled create: %v", err)
	}
	f.fake.FailNext(entrafake.FaultServerError, 1)
	f.fake.DelayReplication(0)
	if err := c.DeleteApplication(t.Context(), app.ObjectID); err != nil {
		t.Fatalf("a delete after a server error: %v", err)
	}
	// A just-created application Graph has yet to replicate answers 404,
	// which a delete retries until it is found.
	f.fake.DelayReplication(2)
	app, err = c.CreateApplication(t.Context(), "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteApplication(t.Context(), app.ObjectID); err != nil {
		t.Fatal(err)
	}
	if apps := f.fake.Applications(); len(apps) != 0 {
		t.Errorf("the replicated application survived: %+v", apps)
	}
}

func TestUnitPersistentFailures(t *testing.T) {
	f := start(t)
	c := New(f.set, Options{Backoff: time.Millisecond, Deadline: 300 * time.Millisecond})
	f.fake.RetryAfter(0)
	f.fake.FailNext(entrafake.FaultUnavailable, 1<<30)
	_, err := c.CreateApplication(t.Context(), "down")
	var g *GraphError
	if !errors.As(err, &g) || g.Status != http.StatusServiceUnavailable || g.Code != "serviceNotAvailable" {
		t.Fatalf("err = %v, want Graph's 503", err)
	}
	if !strings.Contains(err.Error(), "503 serviceNotAvailable") {
		t.Errorf("error %q", err)
	}
	if len(f.graphCalls()) < 2 {
		t.Errorf("the create was not retried: %+v", f.graphCalls())
	}
	// A Retry-After past the deadline fails at once.
	f.fake.Reset()
	f.fake.RetryAfter(60)
	f.fake.FailNext(entrafake.FaultThrottle, 1<<30)
	if err := c.DeleteApplication(t.Context(), "x"); !errors.As(err, &g) || g.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want Graph's 429", err)
	}
	if n := len(f.graphCalls()); n != 1 {
		t.Errorf("%d calls, want the one that named a wait past the deadline", n)
	}
	// A cancelled context stops the retries.
	f.fake.Reset()
	f.fake.FailNext(entrafake.FaultServerError, 1<<30)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.CreateApplication(ctx, "cancelled"); err == nil {
		t.Fatal("a cancelled create succeeded")
	}
}

func TestUnitAuthenticationFailure(t *testing.T) {
	f := start(t)
	t.Setenv("AZURE_CLIENT_SECRET", "wrong")
	_, err := f.client().CreateApplication(t.Context(), "refused")
	var g *GraphError
	if !errors.As(err, &g) || g.Status != 0 || g.Err == nil {
		t.Fatalf("err = %v, want the credential's failure", err)
	}
	if !strings.Contains(err.Error(), "create application refused") {
		t.Errorf("error %q", err)
	}
	for _, c := range f.fake.Calls() {
		if strings.HasPrefix(c.Path, "/v1.0/") {
			t.Errorf("Graph was called without a token: %+v", c)
		}
	}
}

// TestUnitWorkloadIdentity authenticates with a federated assertion read
// from AZURE_FEDERATED_TOKEN_FILE, as a pod with workload identity does.
func TestUnitWorkloadIdentity(t *testing.T) {
	f := start(t)
	f.fake.AddAssertion(tenant, client, "eyJhbGciOiJSUzI1NiJ9.assertion.sig")
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("eyJhbGciOiJSUzI1NiJ9.assertion.sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "WorkloadIdentityCredential")
	t.Setenv("AZURE_CLIENT_SECRET", "")
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", file)
	if _, err := f.client().CreateApplication(t.Context(), "federated"); err != nil {
		t.Fatal(err)
	}
	var grants []string
	for _, c := range f.fake.Calls() {
		if c.Grant != "" {
			grants = append(grants, c.Grant)
		}
	}
	if len(grants) != 1 || grants[0] != entrafake.GrantAssertion {
		t.Errorf("grants %v, want one assertion", grants)
	}
}

func TestUnitMalformedAnswer(t *testing.T) {
	fake := entrafake.New()
	fake.AddSecret(tenant, client, secret)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1.0/applications" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":""}`))
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "EnvironmentCredential")
	t.Setenv("AZURE_TENANT_ID", tenant)
	t.Setenv("AZURE_CLIENT_ID", client)
	t.Setenv("AZURE_CLIENT_SECRET", secret)
	c := New(Settings{
		TenantID: tenant, ClientID: client, AuthorityHost: srv.URL, GraphEndpoint: srv.URL, CAFile: ca,
	}, Options{})
	_, err := c.CreateApplication(t.Context(), "empty")
	var g *GraphError
	if !errors.As(err, &g) || g.Code != "MalformedAnswer" || g.Status != http.StatusCreated {
		t.Fatalf("err = %v, want a malformed answer", err)
	}
}

func TestUnitDefaults(t *testing.T) {
	c := New(Settings{}, Options{})
	if c.graph() != DefaultGraphEndpoint || c.opts.Backoff != DefaultBackoff || c.opts.Deadline != DefaultDeadline {
		t.Errorf("defaults %q %v %v", c.graph(), c.opts.Backoff, c.opts.Deadline)
	}
	if c := New(Settings{GraphEndpoint: "https://graph.example/"}, Options{}); c.graph() != "https://graph.example" {
		t.Errorf("graph %q", c.graph())
	}
	if _, err := New(Settings{CAFile: "/nonexistent"}, Options{}).CreateApplication(t.Context(), "x"); !errors.Is(err, ErrSettings) {
		t.Errorf("err = %v, want the CA file's failure", err)
	}
}

func TestUnitRetryAfter(t *testing.T) {
	if _, ok := retryAfter(nil); ok {
		t.Error("no answer has a Retry-After")
	}
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat))
	if d, ok := retryAfter(&http.Response{Header: h}); !ok || d != 0 {
		t.Errorf("a past date: %v %v", d, ok)
	}
	h.Set("Retry-After", "soon")
	if _, ok := retryAfter(&http.Response{Header: h}); ok {
		t.Error("an unreadable Retry-After was read")
	}
	if retryable(&GraphError{Err: errors.New("bad request")}, false) {
		t.Error("a failure that is no network error was retried")
	}
	if !retryable(&GraphError{Status: http.StatusBadGateway}, false) ||
		retryable(&GraphError{Status: http.StatusBadRequest}, true) {
		t.Error("retryable statuses")
	}
	if (&GraphError{Op: "op", Status: 500}).Error() != "microsoft graph: op: 500" {
		t.Error("an error without a code")
	}
}

func TestUnitValidate(t *testing.T) {
	none := func(string) string { return "" }
	if err := (Settings{}).Validate(none); err != nil {
		t.Errorf("unset: %v", err)
	}
	good := Settings{TenantID: tenant, ClientID: client}
	if err := good.Validate(none); err != nil {
		t.Errorf("good: %v", err)
	}
	if err := good.Validate(func(string) string { return strings.ToUpper(client) }); err != nil {
		t.Errorf("AZURE_CLIENT_ID in upper case: %v", err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := Settings{
		TenantID: "acme", ClientID: client, AuthorityHost: "http://login.example",
		GraphEndpoint: "https://", CAFile: ca,
	}
	err := bad.Validate(func(string) string { return tenant })
	for _, want := range []string{"tenant_id", "authority_host", "graph_endpoint", "holds no PEM", "AZURE_CLIENT_ID"} {
		if !errors.Is(err, ErrSettings) || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %s refused", err, want)
		}
	}
}

func TestUnitApplications(t *testing.T) {
	f := start(t)
	a := Applications{C: f.client()}
	app, err := a.Create(t.Context(), "adapter")
	if err != nil || app.AppID == "" || app.ObjectID == "" {
		t.Fatalf("create %+v %v", app, err)
	}
	if err := a.Delete(t.Context(), app.ObjectID); err != nil {
		t.Fatal(err)
	}
	f.fake.FailNext(entrafake.FaultUnavailable, 1<<30)
	a.C = New(f.set, Options{Backoff: time.Millisecond, Deadline: 50 * time.Millisecond})
	_, err = a.Create(t.Context(), "down")
	g, ok := errors.AsType[*core.GraphFailure](err)
	if !ok || g.Status != http.StatusServiceUnavailable || g.Code != "serviceNotAvailable" {
		t.Fatalf("err = %v, want a GraphFailure", err)
	}
	if err := failure(errors.New("other")); err.(*core.GraphFailure).Status != 0 { //nolint:forcetypeassert,errorlint // failure answers its own type
		t.Error("a failure without an answer has a status")
	}
}
