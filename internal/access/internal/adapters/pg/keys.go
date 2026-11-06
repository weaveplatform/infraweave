package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// ErrStep reports a rotation step that moves a key to a state no rotation
// moves an existing key to: next, which only a new key starts in.
var ErrStep = errors.New("invalid rotation step")

// privateKey is the sealed private half of a local signing key, bound to
// the key's id as its uuid text.
var privateKey = crypt.Column{
	Table: "signing_keys",
	Name:  "private_key_sealed",
	Key:   []string{"id"},
}

// Sealer seals and opens a column's values; crypt.Keyring is one.
type Sealer interface {
	SealColumn(ctx context.Context, c crypt.Column, pk []string, plaintext []byte) ([]byte, error)
	OpenColumn(ctx context.Context, c crypt.Column, pk []string, sealed []byte) (crypt.Value, error)
}

// SigningKeys is token.Store on PostgreSQL, with the local backend: it
// generates each key in process and seals its private half.
type SigningKeys struct {
	db   DB
	seal Sealer
}

var _ token.Store = (*SigningKeys)(nil)

// NewSigningKeys returns the store on db, sealing under seal.
func NewSigningKeys(db DB, seal Sealer) *SigningKeys { return &SigningKeys{db: db, seal: seal} }

// Keys returns purpose's keys that are not retired, oldest first.
func (s *SigningKeys) Keys(ctx context.Context, purpose token.Purpose) ([]token.Key, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `
SELECT id, public_jwk, state, created_at, activated_at, retire_after
FROM signing_keys
WHERE purpose = $1 AND state <> 'retired'
ORDER BY created_at, id`, purpose)
	if err != nil {
		return nil, fmt.Errorf("access: read signing keys: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	var keys []token.Key
	for rows.Next() {
		var (
			k                   = token.Key{Purpose: purpose}
			jwk                 []byte
			activated, retireAt *time.Time
		)
		if err := rows.Scan(
			&k.ID,
			&jwk,
			&k.State,
			&k.CreatedAt,
			&activated,
			&retireAt,
		); err != nil {
			return nil, fmt.Errorf("access: read signing keys: %w", sql.Unavailable(err))
		}
		if err := json.Unmarshal(jwk, &k.Public); err != nil {
			return nil, fmt.Errorf("access: signing key %s: public_jwk: %w", k.ID, err)
		}
		if activated != nil {
			k.ActivatedAt = *activated
		}
		if retireAt != nil {
			k.RetireAfter = *retireAt
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("access: read signing keys: %w", sql.Unavailable(err))
	}
	return keys, nil
}

// Signer opens the private half of the key id.
func (s *SigningKeys) Signer(ctx context.Context, id kernel.ULID) (signer.Signer, error) {
	var sealed []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT private_key_sealed FROM signing_keys WHERE id = $1`, id).Scan(&sealed)
	if err := read(err, "signing key", id); err != nil {
		return nil, err
	}
	der, err := s.seal.OpenColumn(ctx, privateKey, []string{id.UUID()}, sealed)
	if err != nil {
		return nil, fmt.Errorf("access: open signing key %s: %w", id, err)
	}
	return signer.ParseLocal(id.String(), der.Reveal()) //nolint:wrapcheck // names the key
}

// Rotate runs plan over purpose's keys and applies its steps in one
// transaction. SHARE ROW EXCLUSIVE conflicts with itself and with every
// write but not with reads, so concurrent rotations, the first key's
// creation among them, queue behind each other while verifiers keep
// reading; the second sees what the first committed.
func (s *SigningKeys) Rotate(
	ctx context.Context,
	purpose token.Purpose,
	plan func([]token.Key) []token.Step,
) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, s.db).Exec(ctx,
			`LOCK TABLE signing_keys IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return sql.Unavailable(err) //nolint:wrapcheck // wrapped below
		}
		keys, err := s.Keys(ctx, purpose)
		if err != nil {
			return err
		}
		for _, step := range plan(keys) {
			if err := s.apply(ctx, purpose, step); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("access: rotate %s keys: %w", purpose, err)
	}
	return nil
}

// apply makes one step: a new key, or a key's move to its next state with
// the time that state records.
func (s *SigningKeys) apply(ctx context.Context, purpose token.Purpose, step token.Step) error {
	if step.ID.IsZero() {
		return s.create(ctx, purpose, step)
	}
	column := map[token.State]string{
		token.StateActive:   "activated_at",
		token.StateRetiring: "retire_after",
		token.StateRetired:  "retired_at",
	}[step.State]
	if column == "" {
		return fmt.Errorf("%w: key %s to %s", ErrStep, step.ID, step.State)
	}
	at := step.At
	if step.State == token.StateRetiring {
		at = step.RetireAfter
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx,
		`UPDATE signing_keys SET state = $2, `+column+` = $3 WHERE id = $1`,
		step.ID, step.State, at); err != nil {
		return fmt.Errorf("move key %s to %s: %w", step.ID, step.State, sql.Unavailable(err))
	}
	return nil
}

// create generates a key, seals its private half and inserts it.
func (s *SigningKeys) create(ctx context.Context, purpose token.Purpose, step token.Step) error {
	id := kernel.NewULID()
	l, err := signer.GenerateLocal(id.String())
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	der, err := l.PrivateKey()
	if err != nil {
		return err //nolint:wrapcheck // names the key
	}
	sealed, err := s.seal.SealColumn(ctx, privateKey, []string{id.UUID()}, der)
	if err != nil {
		return fmt.Errorf("seal key %s: %w", id, err)
	}
	jwk, err := json.Marshal(l.Public())
	if err != nil {
		return fmt.Errorf("key %s: public_jwk: %w", id, err)
	}
	var activated *time.Time
	if step.State == token.StateActive {
		activated = &step.At
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `
INSERT INTO signing_keys
    (id, purpose, algorithm, public_jwk, private_key_sealed, state, created_at, activated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, purpose, signer.ES256, jwk, sealed, step.State, step.At, activated); err != nil {
		return fmt.Errorf("insert key %s: %w", id, sql.Unavailable(err))
	}
	return nil
}
