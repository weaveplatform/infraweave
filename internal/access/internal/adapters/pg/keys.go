package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var (
	// ErrStep reports a rotation step that moves a key to a state no
	// rotation moves an existing key to: next, which only a new key starts
	// in.
	ErrStep = errors.New("invalid rotation step")
	// ErrKMSRef reports a key whose kms_key_ref no configured KMS key
	// opens: the operator removed it from auth.signing.kms_keys while it
	// still signs.
	ErrKMSRef = errors.New("signing key's KMS key is not in auth.signing.kms_keys")
)

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

// SigningKeys is token.Store on PostgreSQL. A key the local backend
// generates has its private half sealed in private_key_sealed; a key of
// the kms backend has its KMS key's Ref in kms_key_ref, and signs through
// the opened KMS key of that Ref.
type SigningKeys struct {
	db   DB
	seal Sealer
	kms  map[string]signer.KMS
}

var _ token.Store = (*SigningKeys)(nil)

// NewSigningKeys returns the store on db, sealing under seal and signing
// through kms, the kms backend's opened keys.
func NewSigningKeys(db DB, seal Sealer, kms []signer.KMS) *SigningKeys {
	s := &SigningKeys{db: db, seal: seal, kms: make(map[string]signer.KMS, len(kms))}
	for _, k := range kms {
		s.kms[k.Ref()] = k
	}
	return s
}

// Keys returns purpose's keys that are not retired, oldest first.
func (s *SigningKeys) Keys(ctx context.Context, purpose token.Purpose) ([]token.Key, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `
SELECT id, public_jwk, state, created_at, activated_at, retire_after, coalesce(kms_key_ref, '')
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
			&k.Ref,
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

// Signer opens the private half of the key id: its sealed local key, or
// the KMS key its kms_key_ref names, under its kid.
func (s *SigningKeys) Signer(ctx context.Context, id kernel.ULID) (signer.Signer, error) {
	var (
		sealed []byte
		ref    *string
	)
	err := tx.Q(ctx, s.db).QueryRow(ctx,
		`SELECT private_key_sealed, kms_key_ref FROM signing_keys WHERE id = $1`, id).
		Scan(&sealed, &ref)
	if err := read(err, "signing key", id); err != nil {
		return nil, err
	}
	if ref != nil {
		k, ok := s.kms[*ref]
		if !ok {
			return nil, fmt.Errorf("access: signing key %s: %w: %s", id, ErrKMSRef, *ref)
		}
		return signer.WithKeyID(k, id.String()), nil
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

// create inserts a key: the KMS key step.Ref names, or else one it
// generates, sealing its private half.
func (s *SigningKeys) create(ctx context.Context, purpose token.Purpose, step token.Step) error {
	id := kernel.NewULID()
	var (
		public jose.JSONWebKey
		sealed []byte
		ref    *string
	)
	if step.Ref != "" {
		k, ok := s.kms[step.Ref]
		if !ok {
			return fmt.Errorf("key %s: %w: %s", id, ErrKMSRef, step.Ref)
		}
		public, ref = signer.WithKeyID(k, id.String()).Public(), &step.Ref
	} else {
		l, err := signer.GenerateLocal(id.String())
		if err != nil {
			return err //nolint:wrapcheck // names itself
		}
		der, err := l.PrivateKey()
		if err != nil {
			return err //nolint:wrapcheck // names the key
		}
		if sealed, err = s.seal.SealColumn(ctx, privateKey, []string{id.UUID()}, der); err != nil {
			return fmt.Errorf("seal key %s: %w", id, err)
		}
		public = l.Public()
	}
	jwk, err := json.Marshal(public)
	if err != nil {
		return fmt.Errorf("key %s: public_jwk: %w", id, err)
	}
	var activated *time.Time
	if step.State == token.StateActive {
		activated = &step.At
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `
INSERT INTO signing_keys
    (id, purpose, algorithm, public_jwk, private_key_sealed, kms_key_ref, state, created_at, activated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, purpose, signer.ES256, jwk, sealed, ref, step.State, step.At, activated); err != nil {
		return fmt.Errorf("insert key %s: %w", id, sql.Unavailable(err))
	}
	return nil
}
