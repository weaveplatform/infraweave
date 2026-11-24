package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// Kind is a token's iw_kind claim: the kind of credential it stands for.
type Kind string

// The iw_kind values.
//
// ref: decision 0006 § "Token kinds"
const (
	KindUser            Kind = "user"
	KindAPIKey          Kind = "api_key"
	KindOrganizationKey Kind = "organization_key"
	KindOAuth           Kind = "oauth"
	KindRun             Kind = "run"
	KindOperator        Kind = "operator"
	// KindWorker is a worker session token and KindRunner a run token:
	// the worker protocol's, which have no session row.
	KindWorker Kind = "worker"
	KindRunner Kind = "runner"
)

// Kinds are every Kind.
var Kinds = []Kind{
	KindUser, KindAPIKey, KindOrganizationKey, KindOAuth, KindRun, KindOperator, KindWorker,
	KindRunner,
}

// Leeway is the clock skew Verify allows on iat, nbf and exp, between
// replicas of one installation.
//
// ref: decision 0006 § "Constraints"
const Leeway = 30 * time.Second

// reloadEvery bounds how often a token naming an unknown kid reloads the
// public keys, so a flood of forged kids costs one query a second.
const reloadEvery = time.Second

var (
	// ErrToken is every reason Verify refuses a token; the error says which.
	ErrToken = errors.New("invalid token")
	// ErrRequest reports an Issue request missing a claim.
	ErrRequest = errors.New("invalid token request")
	// ErrNoKey reports a purpose with no active key after bootstrapping
	// one, which only a store that does not apply steps causes.
	ErrNoKey = errors.New("no active signing key")
)

// Store keeps the signing keys.
type Store interface {
	// Keys returns purpose's keys that are not retired.
	Keys(ctx context.Context, purpose Purpose) ([]Key, error)
	// Signer opens the private half of the key id.
	Signer(ctx context.Context, id kernel.ULID) (signer.Signer, error)
	// Rotate passes plan purpose's keys that are not retired and applies
	// the steps it returns, in order, in one transaction that no other
	// Rotate runs beside, so concurrent replicas agree on one active key.
	Rotate(ctx context.Context, purpose Purpose, plan func([]Key) []Step) error
}

// Request is a token to issue.
type Request struct {
	Kind    Kind
	Subject string
	// Session is the session the token stands for, its jti.
	Session  kernel.ULID
	Audience string
	TTL      time.Duration
	// Scope is an OAuth access token's scope claim, its scopes separated
	// by spaces; empty leaves the claim out.
	Scope string
	// WorkerSerial binds only worker tokens to their registration certificate.
	WorkerSerial int64
}

// Claims are a verified token's claims.
type Claims struct {
	Issuer       string
	Subject      string
	Audience     string
	Session      kernel.ULID
	Kind         Kind
	Scope        string
	WorkerSerial int64
	IssuedAt     time.Time
	Expiry       time.Time
}

// wire is the JWT's claim set: the registered claims and iw_kind.
type wire struct {
	jwt.Claims

	Kind         Kind            `json:"iw_kind"` //nolint:tagliatelle // the claim is named iw_kind on the wire
	Scope        string          `json:"scope,omitempty"`
	WorkerSerial json.RawMessage `json:"iw_worker_serial,omitempty"` //nolint:tagliatelle // worker-only signed credential generation
}

// Service issues and verifies the api-token purpose's JWTs and rotates its
// keys.
type Service struct {
	keys   *keySet
	issuer string
	// runIssuer is an in-run API token's iss, auth.run_api_token.issuer.
	runIssuer string
	now       func() time.Time
}

// New returns the service over store. issuer is every token's iss, and
// now is the clock. The api-token purpose's keys sign ES256 whatever
// policy's Algorithm.
func New(store Store, issuer string, policy Policy, now func() time.Time) *Service {
	policy.Algorithm = signer.ES256
	return &Service{
		keys: newKeySet(store, APIToken, policy, now), issuer: issuer, runIssuer: issuer, now: now,
	}
}

// WithRunIssuer makes iss the iss of in-run API tokens, which a run's
// tools may verify apart from every other token; empty keeps the issuer.
func (s *Service) WithRunIssuer(iss string) *Service {
	if iss != "" {
		s.runIssuer = iss
	}
	return s
}

// issuerOf is the iss a token of kind carries.
func (s *Service) issuerOf(kind Kind) string {
	if kind == KindRun {
		return s.runIssuer
	}
	return s.issuer
}

// Issue signs a token for r with the active key, creating the first key
// if there is none. Its times are whole seconds, as the claims carry them.
func (s *Service) Issue(ctx context.Context, r Request) (string, Claims, error) {
	if !slices.Contains(Kinds, r.Kind) || r.Subject == "" || r.Session.IsZero() ||
		r.Audience == "" || r.TTL < time.Second ||
		r.Kind == KindWorker && r.WorkerSerial <= 0 || r.Kind != KindWorker && r.WorkerSerial != 0 {
		return "", Claims{}, fmt.Errorf("%w: %+v", ErrRequest, r)
	}
	now := s.now().UTC().Truncate(time.Second)
	c := Claims{
		Issuer:       s.issuerOf(r.Kind),
		Subject:      r.Subject,
		Audience:     r.Audience,
		Session:      r.Session,
		Kind:         r.Kind,
		Scope:        r.Scope,
		WorkerSerial: r.WorkerSerial,
		IssuedAt:     now,
		Expiry:       now.Add(r.TTL),
	}
	var serial json.RawMessage
	if c.Kind == KindWorker {
		serial = strconv.AppendInt(nil, c.WorkerSerial, 10)
	}
	raw, err := s.keys.sign(ctx, wire{
		Claims: jwt.Claims{
			Issuer:    c.Issuer,
			Subject:   c.Subject,
			Audience:  jwt.Audience{c.Audience},
			ID:        c.Session.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Expiry:    jwt.NewNumericDate(c.Expiry),
		},
		Kind:         c.Kind,
		Scope:        c.Scope,
		WorkerSerial: serial,
	})
	if err != nil {
		return "", Claims{}, err
	}
	return raw, c, nil
}

// Rotate applies the api-token purpose's rotation due now, one half of
// authn.keys.rotate.
func (s *Service) Rotate(ctx context.Context) error { return s.keys.rotate(ctx) }

// Verify checks raw's signature against the key its kid names, and its
// claims: ES256 only, the issuer of its iw_kind, exactly the one audience given, a
// session ULID, a known iw_kind, and iat, nbf and exp within Leeway of now.
// Every refusal wraps ErrToken; a store that cannot be read is not one.
func (s *Service) Verify(ctx context.Context, raw, audience string) (Claims, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{signer.ES256})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	kid := tok.Headers[0].KeyID // a compact JWS has exactly one signature
	key, ok, err := s.keys.publicKey(ctx, kid)
	if err != nil {
		return Claims{}, err
	}
	if !ok {
		return Claims{}, fmt.Errorf("%w: unknown key %q", ErrToken, kid)
	}
	var w wire
	if err := tok.Claims(key.Key, &w); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	session, err := kernel.ParseULID(w.ID)
	var serial int64
	var serialErr error
	if len(w.WorkerSerial) != 0 {
		serialErr = json.Unmarshal(w.WorkerSerial, &serial)
	}
	switch {
	case w.IssuedAt == nil || w.NotBefore == nil || w.Expiry == nil:
		return Claims{}, fmt.Errorf("%w: iat, nbf and exp are required", ErrToken)
	case len(w.Audience) != 1 || w.Audience[0] != audience:
		return Claims{}, fmt.Errorf(
			"%w: aud %q is not %q",
			ErrToken,
			[]string(w.Audience),
			audience,
		)
	case err != nil:
		return Claims{}, fmt.Errorf("%w: jti: %w", ErrToken, err)
	case w.Subject == "":
		return Claims{}, fmt.Errorf("%w: no sub", ErrToken)
	case w.Kind == KindWorker && (len(w.WorkerSerial) == 0 || serialErr != nil || serial <= 0):
		return Claims{}, fmt.Errorf("%w: positive iw_worker_serial is required for workers", ErrToken)
	case w.Kind != KindWorker && len(w.WorkerSerial) != 0:
		return Claims{}, fmt.Errorf("%w: iw_worker_serial is worker-only", ErrToken)
	case !slices.Contains(Kinds, w.Kind):
		return Claims{}, fmt.Errorf("%w: iw_kind %q", ErrToken, w.Kind)
	}
	if err := w.ValidateWithLeeway(jwt.Expected{
		Issuer:      s.issuerOf(w.Kind),
		AnyAudience: jwt.Audience{audience},
		Time:        s.now(),
	}, Leeway); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return Claims{
		Issuer:       w.Issuer,
		Subject:      w.Subject,
		Audience:     audience,
		Session:      session,
		Kind:         w.Kind,
		Scope:        w.Scope,
		WorkerSerial: serial,
		IssuedAt:     w.IssuedAt.Time().UTC(),
		Expiry:       w.Expiry.Time().UTC(),
	}, nil
}

// VerifyingKeys are the iss of every token but an in-run API token, and
// the public half of each key that verifies a token, the store's keys
// that are not retired, as ES256 signing keys. It reads the store each
// call, so a key a rotation publishes is answered at once.
func (s *Service) VerifyingKeys(ctx context.Context) (string, []jose.JSONWebKey, error) {
	keys, err := s.keys.store.Keys(ctx, APIToken)
	if err != nil {
		return "", nil, fmt.Errorf("read signing keys: %w", err)
	}
	out := make([]jose.JSONWebKey, 0, len(keys))
	for _, k := range keys {
		pub := k.Public.Public()
		pub.Algorithm, pub.Use = string(signer.ES256), "sig"
		out = append(out, pub)
	}
	return s.issuer, out, nil
}
