package token

import (
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Purpose is what a signing key signs (decision 0006).
type Purpose string

// APIToken is the purpose of every API-facing JWT: API, organization, CLI,
// in-run, worker and run tokens. Its keys are never published.
const APIToken Purpose = "api-token"

// State is where a signing key is in its rotation.
type State string

// The states of decision 0006's rotation, in the order a key passes
// through them. A next key verifies but does not sign; an active key
// signs; a retiring key verifies what it signed until those tokens
// expire; a retired key does neither.
const (
	StateNext     State = "next"
	StateActive   State = "active"
	StateRetiring State = "retiring"
	StateRetired  State = "retired"
)

// Decision 0006's rotation schedule.
const (
	// KeyLifetime is how long a key signs before its successor takes over.
	KeyLifetime = 90 * 24 * time.Hour
	// NextLead is how long before KeyLifetime the successor is created.
	NextLead = 7 * 24 * time.Hour
	// MinPublished is how long a next key verifies before it may sign, so
	// every verifier has loaded it by the time a token names it.
	MinPublished = 48 * time.Hour
	// RetireGrace is how long a retiring key outlives its last token.
	RetireGrace = 24 * time.Hour
	// SignerRefresh is how long a replica signs with the key it loaded
	// before it reads which key is active again, so a key's last token is
	// signed at most this long after its successor took over.
	SignerRefresh = time.Minute
	// RotateEvery is how often authn.keys.rotate runs.
	RotateEvery = 24 * time.Hour
)

// Key is a signing key's public half and its place in the rotation.
type Key struct {
	ID      kernel.ULID
	Purpose Purpose
	// Public is the key's JWK; its KeyID is ID's text.
	Public    jose.JSONWebKey
	State     State
	CreatedAt time.Time
	// ActivatedAt is when the key began signing; zero while it is next.
	ActivatedAt time.Time
	// RetireAfter is when the last token the key signed expires; set once
	// it is retiring.
	RetireAfter time.Time
}

// Step is one change a rotation makes: a new key when ID is zero,
// otherwise a key's move to State.
type Step struct {
	ID    kernel.ULID
	State State
	// At is when the step happens: the new key's creation, and its
	// activation if it starts active; the activation of a promoted key;
	// the retirement of a retired one.
	At time.Time
	// RetireAfter is set when the key becomes retiring.
	RetireAfter time.Time
}

// Policy is how rotation runs for one installation.
type Policy struct {
	// Manual is auth.signing.rotation: manual, under which authn.keys.rotate
	// does not run and no key is replaced; the first key is still created.
	Manual bool
	// TokenLife is the longest lifetime of a token the purpose's keys sign.
	TokenLife time.Duration
}

// Plan is the rotation due at now over keys, a purpose's keys that are not
// retired, in the order the store must apply it: a retiring key past its
// grace is retired; with no active key, a next key is promoted or the
// first key is created active; a next key published for MinPublished takes
// over from an active key KeyLifetime old, which becomes retiring until
// its last token expires; and an active key within NextLead of
// KeyLifetime gets a next key.
func (p Policy) Plan(keys []Key, now time.Time) []Step {
	var (
		steps        []Step
		active, next *Key
	)
	for i := range keys {
		switch k := &keys[i]; k.State {
		case StateActive:
			active = k
		case StateNext:
			next = k
		case StateRetiring:
			if !now.Before(k.RetireAfter.Add(RetireGrace)) {
				steps = append(steps, Step{ID: k.ID, State: StateRetired, At: now})
			}
		case StateRetired:
		}
	}
	switch {
	case active == nil && next != nil:
		return append(steps, Step{ID: next.ID, State: StateActive, At: now})
	case active == nil:
		return append(steps, Step{State: StateActive, At: now})
	case p.Manual:
		return steps
	case next != nil:
		if now.Sub(active.ActivatedAt) >= KeyLifetime && now.Sub(next.CreatedAt) >= MinPublished {
			steps = append(steps,
				Step{
					ID:          active.ID,
					State:       StateRetiring,
					At:          now,
					RetireAfter: now.Add(SignerRefresh + p.TokenLife),
				},
				Step{ID: next.ID, State: StateActive, At: now})
		}
		return steps
	case now.Sub(active.ActivatedAt) >= KeyLifetime-NextLead:
		return append(steps, Step{State: StateNext, At: now})
	}
	return steps
}
