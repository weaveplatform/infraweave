package session

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Channel is the login path a session came through.
type Channel string

// The channels of decision 0006. infraweave's CLI is INFRAWEAVECTL.
const (
	ChannelUnknown      Channel = "UNKNOWN"
	ChannelWeb          Channel = "WEB"
	ChannelTerraformCLI Channel = "TERRAFORM_CLI"
	ChannelCLI          Channel = "INFRAWEAVECTL"
)

// Session is the row behind a credential: revoking it revokes every token
// that names it as its jti.
type Session struct {
	ID             kernel.ULID
	OrganizationID kernel.OrganizationID
	// AccountID is the one account the session works in; zero for an
	// organization, break-glass or operator session, and for a member no
	// account admits.
	AccountID kernel.AccountID
	Channel   Channel
	ActorKind kernel.PrincipalKind
	ActorID   kernel.ULID
	MemberID  kernel.MemberID
	Login     string
	Name      string
	Email     string
	Admin     bool
	Teams     []string
	Scopes    []string
	IPAddress string
	CreatedAt time.Time
	ExpiresAt time.Time
	// IdleExpiresAt ends a browser session that goes unused; zero for
	// sessions that have no idle limit.
	IdleExpiresAt time.Time
	RevokedAt     time.Time
}

// Live reports whether the session authenticates at now: not revoked, and
// before its absolute and idle ends.
func (s Session) Live(now time.Time) bool {
	return s.RevokedAt.IsZero() && now.Before(s.ExpiresAt) &&
		(s.IdleExpiresAt.IsZero() || now.Before(s.IdleExpiresAt))
}

// Principal is who a request with the session's credential acts as.
func (s Session) Principal() kernel.Principal {
	return kernel.Principal{
		Kind:           s.ActorKind,
		ActorID:        s.ActorID,
		OrganizationID: s.OrganizationID,
		AccountID:      s.AccountID,
		MemberID:       s.MemberID,
		SessionID:      s.ID,
		Login:          s.Login,
		Name:           s.Name,
		Teams:          s.Teams,
		Admin:          s.Admin,
		Channel:        string(s.Channel),
		Scopes:         s.Scopes,
	}
}

// keyPrefix starts a session's message on infraweave_invalidate.
const keyPrefix = "session:"

// Key is the message a revocation sends on infraweave_invalidate, so every
// replica drops the session from its cache.
func Key(id kernel.ULID) string { return keyPrefix + id.String() }

// ParseKey reads the session a message names, and false for any other
// message.
func ParseKey(payload string) (kernel.ULID, bool) {
	raw, ok := strings.CutPrefix(payload, keyPrefix)
	if !ok {
		return kernel.ULID{}, false
	}
	id, err := kernel.ParseULID(raw)
	return id, err == nil
}

// CacheTTL is how long a replica trusts a session it read without reading
// it again, which bounds a revocation whose message is lost (decision
// 0006 § "Sessions").
const CacheTTL = 60 * time.Second

// sweepAt is the size at which a load drops the expired entries first, so
// sessions no request names again do not pile up.
const sweepAt = 1 << 14

// Cache is a replica's sessions, read through load and kept for CacheTTL,
// or until Drop or Flush.
type Cache struct {
	load func(context.Context, kernel.ULID) (Session, error)
	now  func() time.Time

	mu      sync.Mutex
	entries map[kernel.ULID]entry
}

type entry struct {
	s  Session
	at time.Time
}

// NewCache returns a cache that reads through load on the clock now.
func NewCache(
	load func(context.Context, kernel.ULID) (Session, error),
	now func() time.Time,
) *Cache {
	return &Cache{load: load, now: now, entries: map[kernel.ULID]entry{}}
}

// Get returns the session id, from the cache while it is younger than
// CacheTTL. A failed load is not cached.
func (c *Cache) Get(ctx context.Context, id kernel.ULID) (Session, error) {
	now := c.now()
	c.mu.Lock()
	e, ok := c.entries[id]
	c.mu.Unlock()
	if ok && now.Sub(e.at) < CacheTTL {
		return e.s, nil
	}
	s, err := c.load(ctx, id)
	if err != nil {
		return Session{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= sweepAt {
		for k, e := range c.entries {
			if now.Sub(e.at) >= CacheTTL {
				delete(c.entries, k)
			}
		}
	}
	c.entries[id] = entry{s: s, at: now}
	return s, nil
}

// Drop forgets the session id, so the next Get reads it again.
func (c *Cache) Drop(id kernel.ULID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// Flush forgets every session, as a lost notification connection requires.
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}
