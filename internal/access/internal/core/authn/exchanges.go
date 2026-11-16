package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ExchangeMargin is how long before its token expires a kept exchange is
// traded again, so a request never carries a token about to lapse.
//
// ref: decision 0010 § "Who may call what"
const ExchangeMargin = 30 * time.Second

// maxExchanges bounds the kept exchanges; past it, they start again empty.
const maxExchanges = 1 << 12

// Exchanges authenticates API keys sent with every request, as the state
// backend's clients send one, through the tokens Exchange trades them
// for: each kept by key ID, with the SHA-256 of the secret that traded
// it, until ExchangeMargin before it expires, so a client opens one
// session per token rather than one per request. A kept token stops when
// its session does, as disabling or deleting the key revokes it; the
// token is then dropped and the key exchanged again, which a revoked key
// fails. The secret is kept only as its digest, compared in constant
// time. Operator and organization keys are refused before any exchange,
// since their tokens reach no account.
type Exchanges struct {
	s    *Service
	mu   sync.Mutex
	kept map[string]exchanged
}

// exchanged is a kept token, the digest of the secret that traded it,
// and when it is traded again.
type exchanged struct {
	digest [sha256.Size]byte
	token  string
	renew  time.Time
}

// NewExchanges returns the exchanges of s, keeping none yet.
func NewExchanges(s *Service) *Exchanges {
	return &Exchanges{s: s, kept: map[string]exchanged{}}
}

// Authenticate is the principal the API key id with secret acts as on a
// route of an account, as Authenticate decides for its token, and the
// token's claims: the token kept for the key and secret, or else one
// exchanged now, recording ip as its session's address. A kept token that
// no longer authenticates is dropped and the key exchanged once more.
func (e *Exchanges) Authenticate(ctx context.Context, id, secret, ip string) (kernel.Principal, token.Claims, error) {
	if key, err := kernel.ParseULID(id); err != nil || e.s.d.Operators[key] != [sha256.Size]byte{} ||
		strings.HasPrefix(secret, apikey.OrganizationPrefix) {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(secret))
	if raw, ok := e.get(id, digest); ok {
		p, c, err := e.s.Authenticate(ctx, raw, ReachAccount)
		if !errors.Is(err, ErrUnauthenticated) {
			return p, c, err
		}
		e.forget(id)
	}
	g, err := e.s.Exchange(ctx, id, secret, ip)
	if err != nil {
		return kernel.Principal{}, token.Claims{}, err
	}
	e.put(id, exchanged{digest: digest, token: g.Token, renew: g.Claims.Expiry.Add(-ExchangeMargin)})
	return e.s.Authenticate(ctx, g.Token, ReachAccount)
}

// get is the token kept for id when digest is its secret's and it is not
// yet due for renewal.
func (e *Exchanges) get(id string, digest [sha256.Size]byte) (string, bool) {
	e.mu.Lock()
	k, ok := e.kept[id]
	e.mu.Unlock()
	if !ok || subtle.ConstantTimeCompare(k.digest[:], digest[:]) != 1 ||
		!e.s.d.Now().Before(k.renew) {
		return "", false
	}
	return k.token, true
}

func (e *Exchanges) put(id string, k exchanged) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.kept) >= maxExchanges {
		e.kept = map[string]exchanged{}
	}
	e.kept[id] = k
}

func (e *Exchanges) forget(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.kept, id)
}
