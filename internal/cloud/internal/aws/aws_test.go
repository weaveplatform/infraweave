package aws_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/weaveplatform/infraweave/internal/cloud/awsfake"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/aws"
)

// SigV4 keys its HMAC with "AWS4" and the secret, and FIPS 140-only mode
// refuses an HMAC key under 112 bits, so the secret is as long as a real
// one.
const (
	serverKey  = "AKIASERVER0000000001"
	secret     = "infraweave-test-secret-0123456789abcdefghi" //nolint:gosec // a test credential
	serverAcct = "111111111111"
	roleARN    = "arn:aws:iam::222222222222:role/infraweave"
)

// environment leaves the SDK's default chain nothing to find but the
// static key it sets, when keyed: no profile, files, region, endpoint
// override or instance metadata.
func environment(t *testing.T, keyed bool) {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "AWS_SESSION_TOKEN",
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_STS",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	if keyed {
		t.Setenv("AWS_ACCESS_KEY_ID", serverKey)
		t.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	}
}

func once(o *sts.Options) { o.RetryMaxAttempts = 1 }

func fake(t *testing.T) (*awsfake.Fake, *aws.Client) {
	t.Helper()
	environment(t, true)
	f := awsfake.New()
	f.AddCaller(serverKey, serverAcct, "arn:aws:iam::111111111111:user/infraweave")
	f.AddRole(awsfake.Role{ARN: roleARN, Trusted: []string{serverAcct}, ExternalID: "acme@*"})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, aws.New(aws.Settings{STSEndpoint: srv.URL}, once)
}

func input() aws.AssumeInput {
	return aws.AssumeInput{RoleARN: roleARN, ExternalID: "acme@01J", SessionName: "infraweave@stack"}
}

func TestUnitCallerAccountCached(t *testing.T) {
	f, c := fake(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if got, err := c.CallerAccount(t.Context()); err != nil || got != serverAcct {
				t.Errorf("CallerAccount = %q, %v", got, err)
			}
		})
	}
	wg.Wait()
	if calls := f.Calls(); len(calls) != 1 || calls[0].Action != "GetCallerIdentity" || calls[0].Region != aws.DefaultRegion {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitCallerAccountFailureNotCached(t *testing.T) {
	f, c := fake(t)
	f.FailNext(awsfake.FaultServerError, 1)
	_, err := c.CallerAccount(t.Context())
	if aws.Code(err) != "InternalFailure" || !strings.Contains(err.Error(), "get caller identity: InternalFailure") {
		t.Fatalf("error = %v", err)
	}
	if got, err := c.CallerAccount(t.Context()); err != nil || got != serverAcct {
		t.Fatalf("CallerAccount = %q, %v", got, err)
	}
	if calls := f.Calls(); len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitNoCredentials(t *testing.T) {
	f, c := fake(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	if _, err := c.CallerAccount(t.Context()); !errors.Is(err, aws.ErrNoCredentials) {
		t.Fatalf("CallerAccount = %v", err)
	}
	if _, err := c.AssumeRole(t.Context(), input()); !errors.Is(err, aws.ErrNoCredentials) {
		t.Fatalf("AssumeRole = %v", err)
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitConfigurationError(t *testing.T) {
	f, c := fake(t)
	t.Setenv("AWS_PROFILE", "missing")
	_, err := c.CallerAccount(t.Context())
	if err == nil || errors.Is(err, aws.ErrNoCredentials) || !strings.Contains(err.Error(), "load configuration") {
		t.Fatalf("error = %v", err)
	}
	// A failed load is tried again.
	t.Setenv("AWS_PROFILE", "")
	if _, err := c.CallerAccount(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls := f.Calls(); len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitAssumeRole(t *testing.T) {
	f, c := fake(t)
	in := input()
	in.Duration = 45 * time.Minute
	in.Region = "eu-central-1"
	in.Tags = map[string]string{"stack": "prod", "account": "acme"}
	creds, err := c.AssumeRole(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(creds.AccessKeyID, "ASIA") || creds.SecretAccessKey == "" || creds.SessionToken == "" {
		t.Fatal("credentials are not shaped as STS's are")
	}
	if left := time.Until(creds.Expiration); left < 44*time.Minute || left > 46*time.Minute {
		t.Fatalf("expires in %v", left)
	}
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	got := calls[0]
	if got.RoleARN != roleARN || got.ExternalID != "acme@01J" || got.SessionName != "infraweave@stack" || got.Duration != 45*time.Minute ||
		got.Region != "eu-central-1" || got.AccessKeyID != serverKey || len(got.Tags) != 2 || got.Tags["stack"] != "prod" || got.Tags["account"] != "acme" {
		t.Fatalf("call = %+v", got)
	}

	f.AddRole(awsfake.Role{ARN: "arn:aws:iam::222222222222:role/open", Trusted: []string{serverAcct}})
	if _, err := c.AssumeRole(t.Context(), aws.AssumeInput{RoleARN: "arn:aws:iam::222222222222:role/open", SessionName: "s"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Calls()[1]; got.ExternalID != "" || got.Duration != 0 || got.Tags != nil || got.Region != aws.DefaultRegion {
		t.Fatalf("call = %+v", got)
	}
}

func TestUnitAssumeRoleRefused(t *testing.T) {
	_, c := fake(t)
	in := input()
	in.ExternalID = "other@01J"
	_, err := c.AssumeRole(t.Context(), in)
	var refused *aws.Refused
	if !errors.As(err, &refused) || refused.Code != "AccessDenied" || aws.Code(err) != "AccessDenied" {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "AccessDenied: User: ") || !strings.Contains(refused.Message, "on resource: "+roleARN) {
		t.Fatalf("error = %v", err)
	}
}

func TestUnitAssumeRoleFaults(t *testing.T) {
	f, c := fake(t)
	for name, tc := range map[string]struct {
		fault awsfake.Fault
		code  string
	}{
		"server error": {awsfake.FaultServerError, "InternalFailure"},
		"throttle":     {awsfake.FaultThrottle, "Throttling"},
		"malformed":    {awsfake.FaultMalformed, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f.FailRole(roleARN, tc.fault)
			defer f.FailRole(roleARN, awsfake.FaultNone)
			_, err := c.AssumeRole(t.Context(), input())
			var refused *aws.Refused
			if err == nil || errors.As(err, &refused) || aws.Code(err) != tc.code || !strings.Contains(err.Error(), "assume role "+roleARN) {
				t.Fatalf("error = %v", err)
			}
			if tc.code != "" && !strings.Contains(err.Error(), roleARN+": "+tc.code+": ") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	f.FailNext(awsfake.FaultTimeout, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.AssumeRole(ctx, input()); !errors.Is(err, context.DeadlineExceeded) || aws.Code(err) != "" {
		t.Fatalf("error = %v", err)
	}
}

func TestUnitAssumeRoleDurationBound(t *testing.T) {
	f, c := fake(t)
	in := input()
	in.Duration = 1 << 62
	if _, err := c.AssumeRole(t.Context(), in); aws.Code(err) != "ValidationError" {
		t.Fatalf("error = %v", err)
	}
	if got := f.Calls()[0].Duration; got != 2147483647*time.Second {
		t.Fatalf("duration sent = %v", got)
	}
}

// router answers the SDK's requests with handler in process, keeping the
// host each was addressed to, so endpoint resolution is seen without the
// network.
type router struct {
	handler http.Handler
	mu      sync.Mutex
	hosts   []string
}

func (r *router) Do(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.hosts = append(r.hosts, req.URL.Host)
	r.mu.Unlock()
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func (r *router) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hosts[len(r.hosts)-1]
}

func routed(t *testing.T, h http.Handler) (*router, *aws.Client) {
	t.Helper()
	rt := &router{handler: h}
	return rt, aws.New(aws.Settings{}, once, func(o *sts.Options) { o.HTTPClient = rt })
}

func TestUnitRegionalEndpoint(t *testing.T) {
	_, _ = fake(t)
	f := awsfake.New()
	f.AddCaller(serverKey, serverAcct, "arn:aws:iam::111111111111:user/infraweave")
	f.AddRole(awsfake.Role{ARN: roleARN, Trusted: []string{serverAcct}})
	rt, c := routed(t, f)
	in := aws.AssumeInput{RoleARN: roleARN, SessionName: "s"}

	if _, err := c.CallerAccount(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := rt.last(); got != "sts.us-east-1.amazonaws.com" {
		t.Fatalf("caller identity host = %s", got)
	}
	in.Region = "eu-west-2"
	if _, err := c.AssumeRole(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if got := rt.last(); got != "sts.eu-west-2.amazonaws.com" {
		t.Fatalf("assume role host = %s", got)
	}
	if got := f.Calls()[1].Region; got != "eu-west-2" {
		t.Fatalf("signing region = %s", got)
	}
}

func TestUnitEnvironmentRegion(t *testing.T) {
	_, _ = fake(t)
	t.Setenv("AWS_REGION", "ap-southeast-2")
	f := awsfake.New()
	f.AddCaller(serverKey, serverAcct, "arn:aws:iam::111111111111:user/infraweave")
	f.AddRole(awsfake.Role{ARN: roleARN, Trusted: []string{serverAcct}})
	rt, c := routed(t, f)
	if _, err := c.AssumeRole(t.Context(), aws.AssumeInput{RoleARN: roleARN, SessionName: "s"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.last(); got != "sts.ap-southeast-2.amazonaws.com" {
		t.Fatalf("host = %s", got)
	}
}

func TestUnitNoCredentialsIssued(t *testing.T) {
	_, _ = fake(t)
	_, c := routed(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte("<AssumeRoleResponse><AssumeRoleResult></AssumeRoleResult></AssumeRoleResponse>"))
	}))
	if _, err := c.AssumeRole(t.Context(), input()); err == nil || !strings.Contains(err.Error(), "without credentials") {
		t.Fatalf("error = %v", err)
	}
}

func TestUnitCode(t *testing.T) {
	if aws.Code(nil) != "" || aws.Code(errors.New("dial tcp: refused")) != "" {
		t.Fatal("Code found a code where there is none")
	}
	if got := (&aws.Refused{Code: "AccessDenied", Message: "no"}).Error(); got != "sts refused: AccessDenied: no" {
		t.Fatalf("Error() = %q", got)
	}
}
