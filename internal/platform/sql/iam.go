package sql

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The database.iam_auth values: none uses the password in the URL; the
// others fetch a token from the provider's default credential chain for
// every new connection and send it as the password.
const (
	IAMNone  = "none"
	IAMAWS   = "aws"
	IAMAzure = "azure"
	IAMGCP   = "gcp"
)

// The OAuth scopes Azure Database for PostgreSQL and Cloud SQL accept a
// token for.
const (
	azureScope = "https://ossrdbms-aad.database.windows.net/.default"
	gcpScope   = "https://www.googleapis.com/auth/sqlservice.login"
)

// ErrIAM marks a token that could not be had. Its message names the
// provider and never holds a token.
var ErrIAM = errors.New("database.iam_auth")

// tokenSource returns the password for one new connection to cc's server.
type tokenSource func(ctx context.Context, cc *pgx.ConnConfig) (string, error)

// The providers' entry points, which the tests replace.
var (
	awsConfig       = func(ctx context.Context) (aws.Config, error) { return awsconfig.LoadDefaultConfig(ctx) }
	awsToken        = auth.BuildAuthToken
	azureCredential = func() (azcore.TokenCredential, error) { return azidentity.NewDefaultAzureCredential(nil) }
	gcpTokenSource  = google.DefaultTokenSource
)

// newTokenSource builds mode's source, or nil for none. The credential
// chains read the environment and the instance metadata, as each SDK does;
// the AWS one must yield a region, which the token is signed for.
func newTokenSource(ctx context.Context, mode string) (tokenSource, error) {
	switch mode {
	case IAMAWS:
		cfg, err := awsConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w %s: %w", ErrIAM, mode, err)
		}
		if cfg.Region == "" {
			return nil, fmt.Errorf("%w %s: no region; set AWS_REGION", ErrIAM, mode)
		}
		return func(ctx context.Context, cc *pgx.ConnConfig) (string, error) {
			endpoint := net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port)))
			return awsToken(ctx, endpoint, cfg.Region, cc.User, cfg.Credentials)
		}, nil
	case IAMAzure:
		cred, err := azureCredential()
		if err != nil {
			return nil, fmt.Errorf("%w %s: %w", ErrIAM, mode, err)
		}
		return func(ctx context.Context, _ *pgx.ConnConfig) (string, error) {
			t, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureScope}})
			return t.Token, err //nolint:wrapcheck // wrapped once, in withToken
		}, nil
	case IAMGCP:
		// The source refreshes its token for as long as the pools live,
		// so it must not end with the context that opened them.
		ts, err := gcpTokenSource(context.WithoutCancel(ctx), gcpScope)
		if err != nil {
			return nil, fmt.Errorf("%w %s: %w", ErrIAM, mode, err)
		}
		ts = oauth2.ReuseTokenSource(nil, ts)
		return func(context.Context, *pgx.ConnConfig) (string, error) {
			t, err := ts.Token()
			if err != nil {
				return "", err //nolint:wrapcheck // wrapped once, in withToken
			}
			return t.AccessToken, nil
		}, nil
	}
	return nil, nil //nolint:nilnil // none: the URL's password stands
}

// withToken sets cc's password to a token from tokens.
func withToken(ctx context.Context, mode string, tokens tokenSource, cc *pgx.ConnConfig) error {
	pw, err := tokens(ctx, cc)
	if err != nil {
		return fmt.Errorf("%w %s: %w", ErrIAM, mode, err)
	}
	cc.Password = pw
	return nil
}

// requiresTLS reports whether every connection attempt cc allows uses TLS:
// sslmode require, verify-ca or verify-full. prefer and allow fall back to
// plaintext, which would send the token, a bearer credential, in the clear.
func requiresTLS(cc *pgconn.Config) bool {
	if cc.TLSConfig == nil {
		return false
	}
	for _, f := range cc.Fallbacks {
		if f.TLSConfig == nil {
			return false
		}
	}
	return true
}
