package signedrun

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// The verifier's bounds. Keys answer for at most CacheFor; a token naming
// a key the cache lacks reads them again once. Each read gets at most
// FetchTimeout of what remains of its caller's deadline. Leeway is the
// clock skew allowed on iat, nbf and exp, the server's own.
//
// ref: decision 0006 § "Constraints"
const (
	CacheFor     = 5 * time.Minute
	FetchTimeout = 30 * time.Second
	Leeway       = 30 * time.Second
)

// Audience is the one aud of every run token, and Kind its iw_kind.
const (
	Audience = "infraweave-runner"
	Kind     = "runner"
)

// maxKeys bounds the keys an answer may name, the worker API's bound.
const maxKeys = 16

var (
	// ErrRefused is a run token that does not prove its run: the server's
	// keys were read and the token fails them.
	ErrRefused = errors.New("signed run admission refused")
	// ErrFailed is a verification that could not decide: the server's
	// keys could not be read or are no ES256 public keys.
	ErrFailed = errors.New("signed run admission failed")
)

// The fixed reasons of a refusal or a failure. None names the token, a
// claim's value, a key or the server's answer.
const (
	reasonMalformed  = "the run token is not an ES256 JWT"
	reasonUnknownKey = "the run token names no key the server verifies with"
	reasonSignature  = "the run token's signature does not verify"
	reasonIssuer     = "the run token's issuer is not the server's"
	reasonAudience   = "the run token's audience is not the runner's"
	reasonKind       = "the token is not a run token"
	reasonRun        = "the run token names another run"
	reasonClaims     = "the run token lacks its ID or time claims"
	reasonExpired    = "the run token has expired"
	reasonEarly      = "the run token is not yet valid"
	reasonUnread     = "the server's run token keys could not be read"
	reasonInvalid    = "the server's run token keys are invalid"
)

// Error is a refusal or failure: its kind, ErrRefused or ErrFailed, and
// a fixed reason. Its cause, which may name the server's answer, is
// reachable through errors.As and errors.Is but never in its text, which
// a run's PREPARING failure note carries.
type Error struct {
	Kind   error
	Reason string
	cause  error
}

func (e *Error) Error() string { return e.Kind.Error() + ": " + e.Reason }

// Unwrap is the kind and the cause.
func (e *Error) Unwrap() []error { return []error{e.Kind, e.cause} }

func refuse(reason string, cause error) error {
	return &Error{Kind: ErrRefused, Reason: reason, cause: cause}
}

func fail(reason string, cause error) error {
	return &Error{Kind: ErrFailed, Reason: reason, cause: cause}
}

// Trust is what admitted a run token: the server's issuer and the run
// tokens' audience, and the server's keys as an RFC 7517 JWK Set
// document, which a policy's io.jwt.decode_verify or a verifier it runs
// takes as is. It holds no private member.
type Trust struct {
	Issuer   string
	Audience string
	JWKS     string
}

// Keys reads the server's run token keys with a worker session token.
// *client.Client is one.
type Keys interface {
	RunTokenKeys(ctx context.Context, session string) (wire.RunTokenKeys, error)
}

// Verifier admits run tokens against the keys of the one server its Keys
// reaches. It is safe for concurrent use.
type Verifier struct {
	keys Keys
	now  func() time.Time

	mu     sync.Mutex
	cached *set
}

// set is one read of the server's keys.
type set struct {
	at    time.Time
	trust Trust
	byID  map[string]jose.JSONWebKey
}

// New is a verifier over keys, whose clock is now.
func New(keys Keys, now func() time.Time) *Verifier {
	return &Verifier{keys: keys, now: now}
}

// Verify admits raw as the run token of run: an ES256 JWT whose
// signature a key the server answers verifies, whose iss is the server's,
// whose one aud is the runner's, whose iw_kind is runner, whose sub is
// run, and whose iat, nbf and exp hold within Leeway of now. session
// reads the keys when the cache is empty, older than CacheFor, or lacks
// the token's kid, which reads them once more. A token that fails is an
// *Error of ErrRefused; keys that cannot be read or are invalid, an
// *Error of ErrFailed.
//
// ref: decision 0006 § "Token kinds"
func (v *Verifier) Verify(ctx context.Context, session, raw, run string) (Trust, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		return Trust{}, refuse(reasonMalformed, err)
	}
	kid := tok.Headers[0].KeyID // a compact JWS has exactly one signature
	s, err := v.read(ctx, session, false)
	if err != nil {
		return Trust{}, err
	}
	key, ok := s.byID[kid]
	if !ok {
		if s, err = v.read(ctx, session, true); err != nil {
			return Trust{}, err
		}
		if key, ok = s.byID[kid]; !ok {
			return Trust{}, refuse(reasonUnknownKey, nil)
		}
	}
	var claims struct {
		jwt.Claims

		Kind string `json:"iw_kind"` //nolint:tagliatelle // the claim is named iw_kind on the wire
	}
	if err := tok.Claims(key.Key, &claims); err != nil {
		return Trust{}, refuse(reasonSignature, err)
	}
	if err := check(claims.Claims, claims.Kind, s.trust.Issuer, run, v.now()); err != nil {
		return Trust{}, err
	}
	return s.trust, nil
}

// check refuses claims that are not a run token of run issued by issuer
// and valid at now.
func check(c jwt.Claims, kind, issuer, run string, now time.Time) error {
	switch {
	case c.Issuer != issuer:
		return refuse(reasonIssuer, nil)
	case len(c.Audience) != 1 || c.Audience[0] != Audience:
		return refuse(reasonAudience, nil)
	case kind != Kind:
		return refuse(reasonKind, nil)
	case c.Subject != run:
		return refuse(reasonRun, nil)
	case c.ID == "" || c.IssuedAt == nil || c.NotBefore == nil || c.Expiry == nil:
		return refuse(reasonClaims, nil)
	}
	err := c.ValidateWithLeeway(jwt.Expected{Time: now}, Leeway)
	switch {
	case errors.Is(err, jwt.ErrExpired):
		return refuse(reasonExpired, err)
	case err != nil:
		return refuse(reasonEarly, err)
	}
	return nil
}

// read answers the cached keys while they are younger than CacheFor and
// fresh is false, and otherwise reads them from the server. A read that
// fails keeps the cache as it was.
func (v *Verifier) read(ctx context.Context, session string, fresh bool) (*set, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if c := v.cached; !fresh && c != nil && now.Sub(c.at) < CacheFor && !now.Before(c.at) {
		return c, nil
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	answer, err := v.keys.RunTokenKeys(ctx, session)
	if err != nil {
		return nil, fail(reasonUnread, err)
	}
	s, err := parse(answer)
	if err != nil {
		return nil, fail(reasonInvalid, err)
	}
	s.at = now
	v.cached = s
	return s, nil
}

// errKeys is an answer that names no issuer, another audience, too many
// keys, or a key that is no distinct ES256 public key on P-256.
var errKeys = errors.New("the answer is no set of ES256 public keys")

// parse checks answer and keeps its keys by ID, as a JWK Set document too.
func parse(answer wire.RunTokenKeys) (*set, error) {
	if answer.Issuer == "" || answer.Audience != Audience || len(answer.Keys) > maxKeys {
		return nil, errKeys
	}
	s := &set{byID: make(map[string]jose.JSONWebKey, len(answer.Keys))}
	jwks := jose.JSONWebKeySet{Keys: make([]jose.JSONWebKey, 0, len(answer.Keys))}
	for _, k := range answer.Keys {
		// A struct of strings always encodes.
		encoded, _ := json.Marshal(k)
		var jwk jose.JSONWebKey
		if err := jwk.UnmarshalJSON(encoded); err != nil {
			return nil, fmt.Errorf("%w: %w", errKeys, err)
		}
		_, dup := s.byID[k.Kid]
		if dup || k.Kid == "" || !jwk.IsPublic() || !jwk.Valid() || jwk.Algorithm != string(jose.ES256) ||
			k.Crv != wire.P256 || k.Kty != wire.EC || k.Use != wire.Sig {
			return nil, errKeys
		}
		s.byID[k.Kid] = jwk
		jwks.Keys = append(jwks.Keys, jwk)
	}
	slices.SortFunc(jwks.Keys, func(a, b jose.JSONWebKey) int { return cmp.Compare(a.KeyID, b.KeyID) })
	// Public ES256 keys that decoded always encode.
	doc, _ := json.Marshal(jwks)
	s.trust = Trust{Issuer: answer.Issuer, Audience: answer.Audience, JWKS: string(doc)}
	return s, nil
}
