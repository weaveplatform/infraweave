package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go"
)

// DefaultRegion is the STS region a call goes to when neither its input
// nor the SDK's environment names one.
const DefaultRegion = "us-east-1"

// accessDenied is STS's error code for a trust policy or condition that
// refuses the caller.
const accessDenied = "AccessDenied"

// ErrNoCredentials is the answer when the SDK's default chain yields no
// credentials for the server's own identity, so no STS call is made.
var ErrNoCredentials = errors.New("aws: no credentials for the server's identity")

// Settings are the server's AWS settings.
type Settings struct {
	// STSEndpoint is the base URL every STS call goes to, such as a proxy,
	// a VPC endpoint or a test's fake. Empty leaves it to the SDK, which
	// picks the regional endpoint of the call's region.
	STSEndpoint string `mapstructure:"sts_endpoint"`
}

// AssumeInput is a role to assume and the session to assume it as.
type AssumeInput struct {
	RoleARN     string
	ExternalID  string
	SessionName string
	// Duration is the session's lifetime, STS's default of an hour when
	// zero; STS takes it in whole seconds.
	Duration time.Duration
	// Region picks the regional STS endpoint and the signing region; empty
	// is the SDK environment's region, or DefaultRegion.
	Region string
	// Tags are the session tags, sent in key order.
	Tags map[string]string
}

// Credentials are temporary credentials STS issued for an assumed role.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
}

// Refused is STS refusing the role to the caller: the trust policy does
// not name it, or a condition such as the external ID fails.
type Refused struct {
	Code    string
	Message string
}

func (r *Refused) Error() string { return "sts refused: " + r.Code + ": " + r.Message }

// Client calls STS as the server's own identity, with credentials from the
// SDK's default chain.
type Client struct {
	settings Settings
	opts     []func(*sts.Options)

	loadMu sync.Mutex
	sts    *sts.Client
	creds  awssdk.CredentialsProvider
	region string

	accountMu sync.Mutex
	account   string
}

// New returns a client for s. It loads the SDK's configuration on first
// use, so New neither fails nor blocks. opts change the STS client after
// s does, as a test's HTTP client or retry limit does.
func New(s Settings, opts ...func(*sts.Options)) *Client {
	return &Client{settings: s, opts: opts}
}

// load reads the SDK's default configuration once it succeeds; a failure
// is not kept, so the next call tries again.
func (c *Client) load(ctx context.Context) (*sts.Client, awssdk.CredentialsProvider, error) {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	if c.sts != nil {
		return c.sts, c.creds, nil
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("aws: load configuration: %w", err)
	}
	c.region = cmp.Or(cfg.Region, DefaultRegion)
	opts := make([]func(*sts.Options), 0, 1+len(c.opts))
	opts = append(opts, func(o *sts.Options) {
		o.Region = c.region
		if c.settings.STSEndpoint != "" {
			o.BaseEndpoint = awssdk.String(c.settings.STSEndpoint)
		}
	})
	c.sts = sts.NewFromConfig(cfg, append(opts, c.opts...)...)
	c.creds = cfg.Credentials
	return c.sts, c.creds, nil
}

// client is the STS client once the server's identity has credentials.
// The default chain caches what it retrieves, so checking each call costs
// one retrieval per credential lifetime.
func (c *Client) client(ctx context.Context) (*sts.Client, error) {
	client, creds, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	if creds == nil {
		return nil, ErrNoCredentials
	}
	v, err := creds.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCredentials, err)
	}
	if !v.HasKeys() {
		return nil, ErrNoCredentials
	}
	return client, nil
}

// CallerAccount is the AWS account ID of the server's own identity, from
// sts:GetCallerIdentity on the first call that succeeds and from memory
// after it.
func (c *Client) CallerAccount(ctx context.Context) (string, error) {
	c.accountMu.Lock()
	defer c.accountMu.Unlock()
	if c.account != "" {
		return c.account, nil
	}
	client, err := c.client(ctx)
	if err != nil {
		return "", err
	}
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", wrap("get caller identity", err)
	}
	c.account = awssdk.ToString(out.Account)
	return c.account, nil
}

// AssumeRole assumes in.RoleARN with sts:AssumeRole. A refusal with STS's
// AccessDenied is *Refused; any other failure names the role and STS's
// error code when it has one.
func (c *Client) AssumeRole(ctx context.Context, in AssumeInput) (Credentials, error) {
	client, err := c.client(ctx)
	if err != nil {
		return Credentials{}, err
	}
	req := &sts.AssumeRoleInput{RoleArn: awssdk.String(in.RoleARN), RoleSessionName: awssdk.String(in.SessionName)}
	if in.ExternalID != "" {
		req.ExternalId = awssdk.String(in.ExternalID)
	}
	if in.Duration > 0 {
		req.DurationSeconds = awssdk.Int32(seconds(in.Duration))
	}
	for _, k := range slices.Sorted(maps.Keys(in.Tags)) {
		req.Tags = append(req.Tags, types.Tag{Key: awssdk.String(k), Value: awssdk.String(in.Tags[k])})
	}
	var region []func(*sts.Options)
	if in.Region != "" {
		region = append(region, func(o *sts.Options) { o.Region = in.Region })
	}
	out, err := client.AssumeRole(ctx, req, region...)
	if api := smithy.APIError(nil); errors.As(err, &api) && api.ErrorCode() == accessDenied {
		return Credentials{}, &Refused{Code: api.ErrorCode(), Message: api.ErrorMessage()}
	}
	if err != nil {
		return Credentials{}, wrap("assume role "+in.RoleARN, err)
	}
	if out.Credentials == nil {
		return Credentials{}, fmt.Errorf("aws: assume role %s: %w", in.RoleARN, errNoCredentialsIssued)
	}
	return Credentials{
		AccessKeyID:     awssdk.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: awssdk.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    awssdk.ToString(out.Credentials.SessionToken),
		Expiration:      awssdk.ToTime(out.Credentials.Expiration),
	}, nil
}

var errNoCredentialsIssued = errors.New("sts answered without credentials")

// Code is STS's error code in err's chain, or "" when err carries none,
// such as a transport failure.
func Code(err error) string {
	var refused *Refused
	if errors.As(err, &refused) {
		return refused.Code
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

func wrap(op string, err error) error {
	if code := Code(err); code != "" {
		return fmt.Errorf("aws: %s: %s: %w", op, code, err)
	}
	return fmt.Errorf("aws: %s: %w", op, err)
}

// seconds is d in whole seconds, held to the range of int32, which STS's
// DurationSeconds is; STS refuses anything above 43200 itself.
func seconds(d time.Duration) int32 {
	s := int64(d / time.Second)
	switch {
	case s > math.MaxInt32:
		return math.MaxInt32
	case s < 0:
		return 0
	}
	return int32(s)
}
