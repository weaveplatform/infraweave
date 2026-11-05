package sql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

// fakes stands in for the three providers. Each hands out a new token per
// call, mode-1, mode-2 and so on, and records what it was asked for.
type fakes struct {
	n        int
	fail     error
	endpoint string
	region   string
	user     string
	scopes   []string
}

func (f *fakes) next(mode string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	f.n++
	return fmt.Sprintf("%s-%d", mode, f.n), nil
}

type fakeCredential struct{ f *fakes }

func (c fakeCredential) GetToken(
	_ context.Context,
	o policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.f.scopes = o.Scopes
	tok, err := c.f.next(IAMAzure)
	return azcore.AccessToken{Token: tok, ExpiresOn: time.Now().Add(time.Hour)}, err
}

// fakeTokenSource's tokens are already expired, so ReuseTokenSource asks for
// a new one every time, as it does once a real token nears expiry.
type fakeTokenSource struct{ f *fakes }

func (s fakeTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.f.next(IAMGCP)
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{AccessToken: tok, Expiry: time.Now()}, nil
}

func useFakes(t *testing.T) *fakes {
	t.Helper()
	f := &fakes{}
	savedConfig, savedToken, savedAzure, savedGCP := awsConfig, awsToken, azureCredential, gcpTokenSource
	t.Cleanup(func() {
		awsConfig, awsToken, azureCredential, gcpTokenSource = savedConfig, savedToken, savedAzure, savedGCP
	})
	awsConfig = func(context.Context) (aws.Config, error) {
		return aws.Config{Region: "eu-west-2", Credentials: aws.AnonymousCredentials{}}, nil
	}
	awsToken = func(_ context.Context, endpoint, region, user string, _ aws.CredentialsProvider,
		_ ...func(*auth.BuildAuthTokenOptions),
	) (string, error) {
		f.endpoint, f.region, f.user = endpoint, region, user
		return f.next(IAMAWS)
	}
	azureCredential = func() (azcore.TokenCredential, error) { return fakeCredential{f}, nil }
	gcpTokenSource = func(_ context.Context, scopes ...string) (oauth2.TokenSource, error) {
		f.scopes = scopes
		return fakeTokenSource{f}, nil
	}
	return f
}

const tlsURL = "postgres://iw@db.example:5433/infraweave?sslmode=verify-full"

func TestUnitIAMAuth(t *testing.T) {
	t.Setenv("PGSSLMODE", "")
	for _, tc := range []struct {
		mode  string
		check func(t *testing.T, f *fakes)
	}{
		{IAMAWS, func(t *testing.T, f *fakes) {
			if f.endpoint != "db.example:5433" || f.region != "eu-west-2" || f.user != "iw" {
				t.Errorf("signed for %s in %s as %s", f.endpoint, f.region, f.user)
			}
		}},
		{IAMAzure, func(t *testing.T, f *fakes) {
			if !slices.Equal(f.scopes, []string{"https://ossrdbms-aad.database.windows.net/.default"}) {
				t.Errorf("scopes %v", f.scopes)
			}
		}},
		{IAMGCP, func(t *testing.T, f *fakes) {
			if !slices.Equal(f.scopes, []string{"https://www.googleapis.com/auth/sqlservice.login"}) {
				t.Errorf("scopes %v", f.scopes)
			}
		}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := useFakes(t)
			c := Defaults()
			c.URL, c.IAMAuth = tlsURL, tc.mode
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			tokens, err := newTokenSource(t.Context(), tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			pc, err := poolConfig("database.url", c.URL, c, tokens)
			if err != nil {
				t.Fatal(err)
			}
			// Each new connection gets a fresh token, on a copy, as pgxpool
			// passes BeforeConnect one.
			for i := 1; i <= 2; i++ {
				cc := pc.ConnConfig.Copy()
				if err := pc.BeforeConnect(t.Context(), cc); err != nil {
					t.Fatal(err)
				}
				if want := fmt.Sprintf("%s-%d", tc.mode, i); cc.Password != want {
					t.Errorf("connection %d password = %q, want %q", i, cc.Password, want)
				}
			}
			if pc.ConnConfig.Password != "" {
				t.Errorf("the pool kept a token: %q", pc.ConnConfig.Password)
			}
			tc.check(t, f)

			// A dedicated connection's settings carry a token of their own.
			pool, err := pgxpool.NewWithConfig(t.Context(), pc) // connects lazily
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			db := &DB{primary: pool, read: pool, iam: tc.mode, tokens: tokens}
			cc, err := db.ConnConfig(t.Context())
			if err != nil || cc.Password != tc.mode+"-3" {
				t.Errorf("ConnConfig password = %v, %v", cc, err)
			}

			f.fail = errors.New("credentials expired")
			cc = pc.ConnConfig.Copy()
			err = pc.BeforeConnect(t.Context(), cc)
			if !errors.Is(err, ErrIAM) ||
				!strings.Contains(
					err.Error(),
					"database.iam_auth "+tc.mode+": credentials expired",
				) {
				t.Errorf("a failing source: %v", err)
			}
			if _, err := db.ConnConfig(t.Context()); !errors.Is(err, ErrIAM) {
				t.Errorf("ConnConfig with a failing source: %v", err)
			}
		})
	}
}

func TestUnitIAMAuthNone(t *testing.T) {
	useFakes(t)
	tokens, err := newTokenSource(t.Context(), IAMNone)
	if err != nil || tokens != nil {
		t.Fatalf("none: %v %v", tokens != nil, err)
	}
	c := Defaults()
	c.URL = "postgres://iw:secret@db.example/infraweave?sslmode=disable"
	if err := c.Validate(); err != nil {
		t.Errorf("none without TLS: %v", err)
	}
	pc, err := poolConfig("database.url", c.URL, c, nil)
	if err != nil || pc.BeforeConnect != nil || pc.ConnConfig.Password != "secret" {
		t.Errorf("none changed the pool: %v", err)
	}
}

// TestUnitIAMAuthNeedsTLS refuses every sslmode that could send the token in
// the clear, on either URL.
func TestUnitIAMAuthNeedsTLS(t *testing.T) {
	t.Setenv("PGSSLMODE", "")
	for _, mode := range []string{"", "?sslmode=disable", "?sslmode=allow", "?sslmode=prefer"} {
		for _, key := range []string{"database.url", "database.read_url"} {
			c := Defaults()
			c.URL, c.ReadURL, c.IAMAuth = tlsURL, tlsURL, IAMAWS
			weak := "postgres://iw@db.example/infraweave" + mode
			if key == "database.url" {
				c.URL = weak
			} else {
				c.ReadURL = weak
			}
			err := c.Validate()
			if !errors.Is(err, ErrConfig) ||
				!strings.Contains(err.Error(), key+"'s sslmode must be require") {
				t.Errorf("%s %q: %v", key, mode, err)
			}
		}
	}
	for _, mode := range []string{"require", "verify-ca", "verify-full"} {
		c := Defaults()
		c.URL, c.IAMAuth = "postgres://iw@db.example/infraweave?sslmode="+mode, IAMGCP
		if err := c.Validate(); err != nil {
			t.Errorf("sslmode=%s: %v", mode, err)
		}
	}
	c := Defaults()
	c.IAMAuth = "ibm"
	if err := c.Validate(); !errors.Is(err, ErrConfig) ||
		!strings.Contains(err.Error(), "database.iam_auth must be") {
		t.Errorf("an unknown provider: %v", err)
	}
}

func TestUnitIAMAuthSourceErrors(t *testing.T) {
	down := errors.New("no credentials")
	for _, tc := range []struct {
		mode   string
		break_ func()
		want   string
	}{
		{IAMAWS, func() { awsConfig = func(context.Context) (aws.Config, error) { return aws.Config{}, down } }, "aws: no credentials"},
		{IAMAWS, func() { awsConfig = func(context.Context) (aws.Config, error) { return aws.Config{}, nil } }, "aws: no region; set AWS_REGION"},
		{IAMAzure, func() { azureCredential = func() (azcore.TokenCredential, error) { return nil, down } }, "azure: no credentials"},
		{IAMGCP, func() {
			gcpTokenSource = func(context.Context, ...string) (oauth2.TokenSource, error) { return nil, down }
		}, "gcp: no credentials"},
	} {
		useFakes(t)
		tc.break_()
		c := Defaults()
		c.URL, c.IAMAuth = tlsURL, tc.mode
		if _, err := Open(
			t.Context(),
			c,
		); !errors.Is(err, ErrIAM) ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.mode, err, tc.want)
		}
	}
}
