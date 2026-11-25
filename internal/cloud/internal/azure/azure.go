package azure

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
)

// The public cloud's endpoints, which Settings' empty fields mean.
const (
	DefaultAuthorityHost = "https://login.microsoftonline.com/"
	DefaultGraphEndpoint = "https://graph.microsoft.com"
)

// The resource every application asks a delegated permission of, and
// that permission: Microsoft Graph's User.Read. Admin consent needs at
// least one permission to grant, and an integration signs in no user.
//
// ref: report 12 § "3.1 Behaviour"
const (
	graphAppID = "00000003-0000-0000-c000-000000000000"
	userRead   = "e1fe6dd8-ba31-4d61-89e7-88639da4683d"
)

// MultipleOrgs is the signInAudience that lets any tenant's admin
// consent to an application.
const MultipleOrgs = "AzureADMultipleOrgs"

// Settings are cloud.azure: the operator's registered Entra application,
// whose credential azidentity's default chain finds, and the endpoints it
// is reached at. The application needs Microsoft Graph's
// Application.ReadWrite.OwnedBy application permission, with its tenant's
// admin consent, to create applications and delete the ones it owns.
type Settings struct {
	// TenantID is the tenant the operator's application is registered
	// in, which applications are created in.
	TenantID string `mapstructure:"tenant_id"`
	// ClientID is the operator's application's client ID. The default
	// chain reads its identity from AZURE_CLIENT_ID, which must name the
	// same application when it is set.
	ClientID string `mapstructure:"client_id"`
	// AuthorityHost is the Entra authority tokens come from, the public
	// cloud's when empty.
	AuthorityHost string `mapstructure:"authority_host"`
	// GraphEndpoint is Microsoft Graph's base URL, the public cloud's when
	// empty.
	GraphEndpoint string `mapstructure:"graph_endpoint"`
	// CAFile is a PEM bundle trusted beside the system's roots for the
	// authority and Graph, such as a proxy's or a test's.
	CAFile string `mapstructure:"ca_file"`
}

// Configured reports whether the installation has an application to
// create Entra applications with.
func (s Settings) Configured() bool { return s != Settings{} }

// ErrSettings reports a cloud.azure setting the server cannot start with.
var ErrSettings = errors.New("invalid cloud.azure setting")

// Validate refuses settings that name no application, an ID that is not
// a GUID, an endpoint that is not an https URL, a CA file that does not
// read as PEM, and an AZURE_CLIENT_ID that names another application than
// ClientID; getenv reads the environment.
func (s Settings) Validate(getenv func(string) string) error {
	if !s.Configured() {
		return nil
	}
	var errs []error
	for name, v := range map[string]string{"tenant_id": s.TenantID, "client_id": s.ClientID} {
		if !core.IsGUID(v) {
			errs = append(errs, fmt.Errorf("%w: cloud.azure.%s %q is not a GUID", ErrSettings, name, v))
		}
	}
	for name, v := range map[string]string{"authority_host": s.AuthorityHost, "graph_endpoint": s.GraphEndpoint} {
		if u, err := url.Parse(v); v != "" && (err != nil || u.Scheme != "https" || u.Host == "") {
			errs = append(errs, fmt.Errorf("%w: cloud.azure.%s %q is not an https URL", ErrSettings, name, v))
		}
	}
	if s.CAFile != "" {
		if _, err := roots(s.CAFile); err != nil {
			errs = append(errs, err)
		}
	}
	if env := getenv("AZURE_CLIENT_ID"); env != "" && !strings.EqualFold(env, s.ClientID) {
		errs = append(errs, fmt.Errorf("%w: AZURE_CLIENT_ID %q names another application than cloud.azure.client_id",
			ErrSettings, env))
	}
	return errors.Join(errs...)
}

// roots are the system's roots with the PEM certificates of file added.
func roots(file string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	b, err := os.ReadFile(file) //nolint:gosec // the operator's own setting names it
	if err != nil {
		return nil, fmt.Errorf("%w: cloud.azure.ca_file: %w", ErrSettings, err)
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("%w: cloud.azure.ca_file %s holds no PEM certificate", ErrSettings, file)
	}
	return pool, nil
}

// Application is an Entra application Graph created: AppID is its
// application (client) ID, ObjectID the ID Graph addresses it by.
type Application struct {
	AppID       string
	ObjectID    string
	DisplayName string
}

// GraphError is a Graph answer the client gave up on: its HTTP status,
// zero when no answer came, and Graph's error code and message.
type GraphError struct {
	Op      string
	Status  int
	Code    string
	Message string
	Err     error
}

func (e *GraphError) Error() string {
	switch {
	case e.Err != nil:
		return fmt.Sprintf("microsoft graph: %s: %v", e.Op, e.Err)
	case e.Code != "":
		return fmt.Sprintf("microsoft graph: %s: %d %s: %s", e.Op, e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("microsoft graph: %s: %d", e.Op, e.Status)
}

func (e *GraphError) Unwrap() error { return e.Err }

// Options change how a Client reaches Entra and Graph, for a test.
type Options struct {
	// Transport replaces the HTTP client both are reached through.
	Transport policy.Transporter
	// Backoff is the first retry's delay, doubled for each retry after up
	// to MaxBackoff; DefaultBackoff when zero.
	Backoff time.Duration
	// Deadline bounds a call whose context has no deadline;
	// DefaultDeadline when zero.
	Deadline time.Duration
}

// The retry delays and the bound of a call without a deadline. Graph
// asks a throttled client to wait seconds, and an object it has just
// created reaches its other replicas within seconds, so a call spends
// most of the request's deadline retrying.
const (
	DefaultBackoff  = 500 * time.Millisecond
	MaxBackoff      = 8 * time.Second
	DefaultDeadline = 30 * time.Second
)

// replicationRetries is how many 404s a delete of an application retries
// before it takes the application as deleted already.
const replicationRetries = 4

// Client creates and deletes Entra applications through Graph as the
// operator's application, with azidentity's default credential chain.
type Client struct {
	settings Settings
	opts     Options

	mu       sync.Mutex
	pipeline *runtime.Pipeline
}

// New returns a client for s. It builds the credential on first use, so
// New neither fails nor reaches the network.
func New(s Settings, o Options) *Client {
	if o.Backoff <= 0 {
		o.Backoff = DefaultBackoff
	}
	if o.Deadline <= 0 {
		o.Deadline = DefaultDeadline
	}
	return &Client{settings: s, opts: o}
}

// graph is Graph's base URL.
func (c *Client) graph() string {
	if c.settings.GraphEndpoint == "" {
		return DefaultGraphEndpoint
	}
	return strings.TrimSuffix(c.settings.GraphEndpoint, "/")
}

// load builds the credential and the Graph pipeline once it succeeds; a
// failure is not kept, so the next call tries again.
func (c *Client) load() (*runtime.Pipeline, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pipeline != nil {
		return c.pipeline, nil
	}
	o := azcore.ClientOptions{Transport: c.opts.Transport}
	if o.Transport == nil && c.settings.CAFile != "" {
		pool, err := roots(c.settings.CAFile)
		if err != nil {
			return nil, err
		}
		t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // net/http's own type
		t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		o.Transport = &http.Client{Transport: t}
	}
	if c.settings.AuthorityHost != "" {
		o.Cloud = cloud.Configuration{ActiveDirectoryAuthorityHost: c.settings.AuthorityHost}
	}
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		ClientOptions: o, TenantID: c.settings.TenantID,
		// Instance discovery asks the public cloud whether an authority is
		// valid, which a sovereign cloud's or a test's is not.
		DisableInstanceDiscovery: c.settings.AuthorityHost != "",
	})
	if err != nil {
		return nil, fmt.Errorf("azure: the operator's credential: %w", err)
	}
	scope := c.graph() + "/.default"
	// The client retries itself, since Graph's replication delay answers a
	// 404 the pipeline's policy would not retry.
	o.Retry = policy.RetryOptions{MaxRetries: -1}
	c.pipeline = new(runtime.NewPipeline("infraweave", "", runtime.PipelineOptions{
		PerRetry: []policy.Policy{runtime.NewBearerTokenPolicy(cred, []string{scope}, nil)},
	}, &o))
	return c.pipeline, nil
}

// CreateApplication creates a multi-tenant application named displayName
// that asks Graph's delegated User.Read, retrying throttling and server
// errors within the call's deadline.
func (c *Client) CreateApplication(ctx context.Context, displayName string) (Application, error) {
	body := map[string]any{
		"displayName":    displayName,
		"signInAudience": MultipleOrgs,
		"requiredResourceAccess": []any{map[string]any{
			"resourceAppId":  graphAppID,
			"resourceAccess": []any{map[string]any{"id": userRead, "type": "Scope"}},
		}},
	}
	var out struct {
		ID          string `json:"id"`
		AppID       string `json:"appId"`
		DisplayName string `json:"displayName"`
	}
	op := "create application " + displayName
	resp, err := c.do(ctx, op, http.MethodPost, "/v1.0/applications", body, 0)
	if err != nil {
		return Application{}, err
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only body
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" || out.AppID == "" {
		return Application{}, &GraphError{
			Op: op, Status: resp.StatusCode, Code: "MalformedAnswer",
			Message: "the answer names no application",
		}
	}
	return Application{AppID: out.AppID, ObjectID: out.ID, DisplayName: out.DisplayName}, nil
}

// DeleteApplication deletes the application whose object ID is id. A 404
// is retried as Graph's replication delay a few times, then taken as an
// application deleted already.
func (c *Client) DeleteApplication(ctx context.Context, id string) error {
	op := "delete application " + id
	resp, err := c.do(ctx, op, http.MethodDelete, "/v1.0/applications/"+url.PathEscape(id), nil, replicationRetries)
	var gone *GraphError
	if errors.As(err, &gone) && gone.Status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	return resp.Body.Close() //nolint:wrapcheck // closing a drained body
}

// do sends one Graph request until it succeeds, retrying 429 and 5xx
// answers, a transport failure and up to notFound 404s, each after
// Retry-After or the backoff, while the call's deadline allows the wait.
func (c *Client) do(ctx context.Context, op, method, path string, body any, notFound int) (*http.Response, error) {
	pl, err := c.load()
	if err != nil {
		return nil, &GraphError{Op: op, Err: err}
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.opts.Deadline)
		defer cancel()
	}
	delay := c.opts.Backoff
	var answered *GraphError
	for attempt := 0; ; attempt++ {
		resp, fail := c.send(ctx, pl, op, method, path, body)
		switch {
		case fail == nil:
			return resp, nil
		case fail.Err != nil && ctx.Err() != nil && answered != nil:
			// The deadline cut a retry short; Graph's last answer says
			// more about the failure than the deadline does.
			return nil, answered
		case fail.Err == nil:
			answered = fail
		}
		wait, retry := delay, retryable(fail, attempt < notFound)
		if fail.Status == http.StatusTooManyRequests || fail.Status == http.StatusServiceUnavailable {
			if after, ok := retryAfter(resp); ok {
				wait = after
			}
		}
		if !retry || !fits(ctx, wait) {
			return nil, fail
		}
		select {
		case <-ctx.Done():
			return nil, fail
		case <-time.After(wait):
		}
		delay = min(delay*2, MaxBackoff)
	}
}

// send sends the request once and answers a 2xx answer, or the failure,
// with the answer it came in when there was one.
func (c *Client) send(ctx context.Context, pl *runtime.Pipeline, op, method, path string, body any) (*http.Response, *GraphError) {
	req, err := runtime.NewRequest(ctx, method, c.graph()+path)
	if err != nil {
		return nil, &GraphError{Op: op, Err: err}
	}
	if body != nil {
		if err := runtime.MarshalAsJSON(req, body); err != nil {
			return nil, &GraphError{Op: op, Err: err}
		}
	}
	resp, err := pl.Do(req)
	if err != nil {
		return nil, &GraphError{Op: op, Err: err}
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only body
	var answer struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(b, &answer) // a body that is not Graph's error leaves the code empty
	return resp, &GraphError{Op: op, Status: resp.StatusCode, Code: answer.Error.Code, Message: answer.Error.Message}
}

// retryable reports whether fail may pass on another try: throttling, a
// server error, a transport failure, or a 404 when notFound allows it. A
// failure to authenticate is not retried.
func retryable(fail *GraphError, notFound bool) bool {
	switch {
	case fail.Err != nil:
		var auth *azidentity.AuthenticationFailedError
		var netErr net.Error
		return !errors.As(fail.Err, &auth) && errors.As(fail.Err, &netErr)
	case fail.Status == http.StatusNotFound:
		return notFound
	}
	return fail.Status == http.StatusTooManyRequests || fail.Status >= http.StatusInternalServerError
}

// retryAfter is resp's Retry-After, in seconds or as an HTTP date.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	v := resp.Header.Get("Retry-After")
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0), true
	}
	return 0, false
}

// fits reports whether waiting wait still leaves the call time to retry.
func fits(ctx context.Context, wait time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > wait
}

// Applications is the client as the service's core.Applications, its
// failures as *core.GraphFailure.
type Applications struct{ C *Client }

var _ core.Applications = Applications{}

// Create creates a multi-tenant application named displayName.
func (a Applications) Create(ctx context.Context, displayName string) (core.Application, error) {
	app, err := a.C.CreateApplication(ctx, displayName)
	if err != nil {
		return core.Application{}, failure(err)
	}
	return core.Application{AppID: app.AppID, ObjectID: app.ObjectID}, nil
}

// Delete deletes the application whose object ID is id.
func (a Applications) Delete(ctx context.Context, id string) error {
	return failure(a.C.DeleteApplication(ctx, id))
}

// failure is err as a *core.GraphFailure.
func failure(err error) error {
	if err == nil {
		return nil
	}
	f := &core.GraphFailure{Err: err}
	if g, ok := errors.AsType[*GraphError](err); ok {
		f.Status, f.Code = g.Status, g.Code
	}
	return f
}
