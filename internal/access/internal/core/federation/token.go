package federation

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The scope claim's values: a read token for a phase that only plans, a
// write token for one that may change infrastructure.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// The callerType claim's values.
const (
	CallerStack  = "stack"
	CallerModule = "module"
)

// ErrNoToken reports a run that receives no federation token, a parse
// run's, or a request missing what a token names.
var ErrNoToken = errors.New("no OIDC federation token for the run")

// Scope is the scope claim of the token a run of type t receives for the
// phase its handover in state phase starts. A proposed run reads; a task,
// a destroy run and a module's test run write throughout; a tracked run
// plans with read and applies with write from PREPARING_APPLY on, unless
// its stack deploys automatically, whose plan applies in the same phase
// and so writes from the start. A parse run, or one of an unknown type,
// gets no token, ErrNoToken.
//
// ref: report 12 section 6.4
// ref: decision 0006 § "OIDC federation token"
func Scope(t kernel.RunType, phase kernel.RunState, autodeploy bool) (string, error) {
	switch t {
	case kernel.RunTypeProposed:
		return ScopeRead, nil
	case kernel.RunTypeTask, kernel.RunTypeDestroy, kernel.RunTypeTesting:
		return ScopeWrite, nil
	case kernel.RunTypeTracked:
		if autodeploy || phase == kernel.RunStatePreparingApply || phase == kernel.RunStateApplying {
			return ScopeWrite, nil
		}
		return ScopeRead, nil
	}
	return "", fmt.Errorf("%w: a %s run", ErrNoToken, t)
}

// Audience is the aud of every token of the account whose slug is
// account: prefix, auth.federation.audience, or the issuer's host without
// its scheme when prefix is empty, then "/" and the slug. The setting is
// a prefix rather than the whole audience, so a trust policy of one
// account never accepts another's token on the same installation.
func Audience(prefix, issuer, account string) string {
	if prefix == "" {
		if u, err := url.Parse(issuer); err == nil {
			prefix = u.Host
		}
	}
	return strings.TrimSuffix(prefix, "/") + "/" + account
}

// SpacePath is the spacePath claim of a space whose ancestry is spaces,
// the slugs from the account's top space down to it: each slug after a
// "/". The top space's slug is the account's, where Spacelift's path
// starts at root.
//
// ref: decision 0007 § "Spaces"
func SpacePath(spaces []string) string {
	return "/" + strings.Join(spaces, "/")
}

// Token is the claim set of an OIDC federation token: exactly Claims,
// with aud one string. NumericDates are whole seconds.
//
// ref: report 12 section 6.3
type Token struct {
	Issuer     string `json:"iss"`
	Subject    string `json:"sub"`
	Audience   string `json:"aud"`
	Expiry     int64  `json:"exp"`
	IssuedAt   int64  `json:"iat"`
	ID         string `json:"jti"`
	NotBefore  int64  `json:"nbf"`
	SpaceID    string `json:"spaceId"`
	SpacePath  string `json:"spacePath"`
	CallerType string `json:"callerType"`
	CallerID   string `json:"callerId"`
	RunType    string `json:"runType"`
	RunID      string `json:"runId"`
	Scope      string `json:"scope"`
}

// Expires is when t expires.
func (t Token) Expires() time.Time { return time.Unix(t.Expiry, 0).UTC() }

// Issue is what one token is built from: the issuer and the audience
// setting, the account's slug and subject template, the ancestry of the
// caller's space, the caller and its run, the phase and the stack's
// autodeploy, which pick the scope, the token's jti, the time it is
// issued at and its lifetime.
type Issue struct {
	Issuer   string
	Audience string
	Account  string
	Template string
	// Spaces are the slugs of the spaces from the account's top space
	// down to the caller's.
	Spaces []string
	// CallerType is CallerStack or CallerModule, and CallerID its slug.
	CallerType, CallerID string
	RunType              kernel.RunType
	Run                  kernel.ULID
	Phase                kernel.RunState
	Autodeploy           bool
	ID                   kernel.ULID
	Now                  time.Time
	TTL                  time.Duration
}

// NewToken is the claim set in describes: iss the issuer; aud Audience's;
// sub the account's template rendered for the run; iat and nbf now, in
// whole seconds, and exp a lifetime later; jti the token's ID; spaceId
// the caller's space's slug and spacePath its ancestry; the caller and
// the run; and the scope Scope picks. A request missing a part, a caller
// of another type or a run that gets no token is ErrNoToken, and a
// subject RenderSubject refuses is ErrInvalid.
func NewToken(in Issue) (Token, error) {
	scope, err := Scope(in.RunType, in.Phase, in.Autodeploy)
	if err != nil {
		return Token{}, err
	}
	switch {
	case in.CallerType != CallerStack && in.CallerType != CallerModule:
		return Token{}, fmt.Errorf("%w: caller type %q", ErrNoToken, in.CallerType)
	case in.CallerID == "" || in.Account == "" || in.Issuer == "" || len(in.Spaces) == 0 ||
		in.Run.IsZero() || in.ID.IsZero() || in.TTL < time.Second:
		return Token{}, fmt.Errorf("%w: an incomplete request for run %s", ErrNoToken, in.Run)
	}
	c := Caller{
		SpaceID:   in.Spaces[len(in.Spaces)-1],
		SpacePath: SpacePath(in.Spaces),
		Type:      in.CallerType,
		ID:        in.CallerID,
		RunID:     in.Run.String(),
		RunType:   string(in.RunType),
		Scope:     scope,
	}
	sub, err := RenderSubject(in.Template, c)
	if err != nil {
		return Token{}, err
	}
	now := in.Now.Truncate(time.Second)
	return Token{
		Issuer:     in.Issuer,
		Subject:    sub,
		Audience:   Audience(in.Audience, in.Issuer, in.Account),
		Expiry:     now.Add(in.TTL).Unix(),
		IssuedAt:   now.Unix(),
		ID:         in.ID.String(),
		NotBefore:  now.Unix(),
		SpaceID:    c.SpaceID,
		SpacePath:  c.SpacePath,
		CallerType: c.Type,
		CallerID:   c.ID,
		RunType:    c.RunType,
		RunID:      c.RunID,
		Scope:      c.Scope,
	}, nil
}
