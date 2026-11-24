package assembly

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/weaveplatform/infraweave/internal/cloud/awsfake"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/aws"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitValidate(t *testing.T) {
	for endpoint, ok := range map[string]bool{
		"": true, "http://127.0.0.1:4566": true, "https://sts.example.com": true,
		"ftp://sts": false, "sts.example.com": false, "http://": false, "http://[::1": false,
	} {
		err := Settings{AWS: aws.Settings{STSEndpoint: endpoint}}.Validate()
		if (err == nil) != ok || (err != nil && !errors.Is(err, ErrConfig)) {
			t.Errorf("%q: err = %v", endpoint, err)
		}
	}
}

func TestUnitMapped(t *testing.T) {
	broken := errors.New("broken")
	if err := mapped(nil); err != nil {
		t.Fatalf("mapped(nil) = %v", err)
	}
	if err := mapped(aws.ErrNoCredentials); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("no credentials = %v", err)
	}
	if r, ok := errors.AsType[*core.Refused](mapped(&aws.Refused{Code: "AccessDenied", Message: "no"})); !ok ||
		r.Message != "no" {
		t.Fatalf("refused = %v", r)
	}
	if u, ok := errors.AsType[*core.Upstream](mapped(broken)); !ok || !errors.Is(u, broken) {
		t.Fatalf("upstream = %v", u)
	}
}

// options answers err for every feature.
type options struct{ err error }

func (o options) AllowAccount(context.Context, kernel.OrganizationID, kernel.AccountID, string) error {
	return o.err
}

func TestUnitEnabled(t *testing.T) {
	broken := errors.New("broken")
	org := func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
		return kernel.OrganizationID{}, nil
	}
	for _, tc := range []struct {
		err  error
		want bool
		fail bool
	}{
		{nil, true, false},
		{&featuresapi.Refusal{}, false, false},
		{broken, false, true},
	} {
		ok, err := enabled(options{tc.err}, org)(t.Context(), kernel.NewAccountID())
		if ok != tc.want || (err != nil) != tc.fail {
			t.Errorf("%v: enabled = %v, %v", tc.err, ok, err)
		}
	}
	lost := func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
		return kernel.OrganizationID{}, broken
	}
	if _, err := enabled(options{}, lost)(t.Context(), kernel.NewAccountID()); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnitNilDomain(t *testing.T) {
	var d *Domain
	if d.API() != (CloudAPI{}) {
		t.Fatal("a nil domain's partial is not the zero partial")
	}
}

// The server's AWS identity the fake knows. SigV4 keys its HMAC with the
// secret, which FIPS 140-only mode refuses under 112 bits, so it is as long
// as a real one.
const (
	serverKey = "AKIASERVER0000000002"
	secret    = "infraweave-assembly-secret-0123456789abcd" //nolint:gosec // a test credential
)

// TestUnitRolesOverSTS assumes a role through the domain's Roles over the
// fake STS: the session tags, region and duration reach STS, and the
// credentials STS issued come back; a refusal maps to *core.Refused.
func TestUnitRolesOverSTS(t *testing.T) {
	dir := t.TempDir()
	for _, k := range []string{
		"AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION",
		"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_STS",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_ACCESS_KEY_ID", serverKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	f := awsfake.New()
	f.AddCaller(serverKey, "111111111111", "arn:aws:iam::111111111111:user/infraweave")
	const role = "arn:aws:iam::222222222222:role/deploy"
	f.AddRole(awsfake.Role{ARN: role, Trusted: []string{"111111111111"}, ExternalID: "acme@*@web@read"})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	r := roles{aws.New(aws.Settings{STSEndpoint: srv.URL}, func(o *sts.Options) { o.RetryMaxAttempts = 1 })}

	tags := map[string]string{core.TagRun: "01JC", core.TagStack: "web", core.TagSpace: "app"}
	s, err := r.Assume(t.Context(), core.Assumption{
		RoleARN: role, ExternalID: "acme@01J@web@read", SessionName: "01JC@web@acme",
		Region: "eu-west-1", Duration: 20 * time.Minute, Tags: tags,
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	c := calls[0]
	if c.Issued.AccessKeyID != s.AccessKeyID || c.Issued.SecretAccessKey != s.SecretAccessKey ||
		c.Issued.SessionToken != s.SessionToken || s.SecretAccessKey == "" {
		t.Fatal("the session is not the credentials STS issued")
	}
	if c.Region != "eu-west-1" || c.Duration != 20*time.Minute || c.SessionName != "01JC@web@acme" ||
		len(c.Tags) != 3 || c.Tags[core.TagSpace] != "app" {
		t.Fatalf("call = %+v", c)
	}

	_, err = r.Assume(t.Context(), core.Assumption{RoleARN: role, ExternalID: "acme@01J@web@write", SessionName: "s"})
	if _, ok := errors.AsType[*core.Refused](err); !ok {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestUnitBackEdges(t *testing.T) {
	d := New(Deps{})
	if d.Credentials() == nil || d.StackCredentials() == nil {
		t.Fatal("the domain answers no back edge")
	}
}
