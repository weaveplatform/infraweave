package crypt

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
)

// Config holds the secrets settings, keyed below secrets.
type Config struct {
	KEK kek.Config `mapstructure:"kek"`
}

// Defaults are the settings an empty configuration means: no KEK
// provider, which the process modes that seal refuse.
func Defaults() Config { return Config{KEK: kek.Defaults()} }

// Validate rejects a KEK setting infraweave could not start with.
func (c Config) Validate() error { return c.KEK.Validate() } //nolint:wrapcheck // names its key

var (
	// ErrUnknownKey reports a value sealed under a data key the keyring
	// does not hold: retired, or never this installation's.
	ErrUnknownKey = errors.New("sealed under a data key the keyring does not hold")
	// ErrKeyring reports a keyring that could not be loaded.
	ErrKeyring = errors.New("keyring")
)

// Querier is the part of a pool or transaction the keyring reads and
// writes encryption_keys through.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Keyring holds the unwrapped active and retiring data keys. Sealing and
// opening are local AES operations, so the KEK provider is called only
// when the keyring loads.
type Keyring struct {
	mu     sync.RWMutex
	active uint32
	keys   map[uint32][]byte
}

// Load unwraps every active and retiring data key in encryption_keys with
// p. On an empty keyring it creates key 1, and a replica that loses that
// race reads the winner's. A key that fails to unwrap stops the load with
// an error naming its ID, because it means the wrong KEK is configured.
func Load(ctx context.Context, db Querier, p kek.Provider) (*Keyring, error) {
	for range 2 {
		k, err := load(ctx, db, p)
		if err != nil || k.active != 0 {
			return k, err
		}
		if err := create(ctx, db, p); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: no active data key after creating one", ErrKeyring)
}

func load(ctx context.Context, db Querier, p kek.Provider) (*Keyring, error) {
	rows, err := db.Query(ctx, `
		SELECT id, wrapped_key, state FROM encryption_keys
		WHERE state IN ('active', 'retiring')`)
	if err != nil {
		return nil, fmt.Errorf("%w: read encryption_keys: %w", ErrKeyring, sql.Unavailable(err))
	}
	type row struct {
		id      uint32
		wrapped []byte
		state   string
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var id int64
		err := r.Scan(&id, &x.wrapped, &x.state)
		x.id = uint32(id) //nolint:gosec // encryption_keys.id is a positive integer
		return x, err     //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("%w: read encryption_keys: %w", ErrKeyring, sql.Unavailable(err))
	}
	k := &Keyring{keys: map[uint32][]byte{}}
	for _, r := range found {
		key, err := p.Unwrap(ctx, r.id, r.wrapped)
		if err != nil {
			return nil, fmt.Errorf("%w: data key %d: %w", ErrKeyring, r.id, err)
		}
		k.keys[r.id] = key
		if r.state == "active" {
			k.active = r.id
		}
	}
	return k, nil
}

// create inserts a new active data key with the next ID. A concurrent
// creator takes the same ID, or the one active slot, so exactly one
// insert lands and the other does nothing.
func create(ctx context.Context, db Querier, p kek.Provider) error {
	var next int64
	if err := db.QueryRow(ctx,
		`SELECT coalesce(max(id), 0) + 1 FROM encryption_keys`).Scan(&next); err != nil {
		return fmt.Errorf("%w: next key ID: %w", ErrKeyring, sql.Unavailable(err))
	}
	id := uint32(next) //nolint:gosec // one key a year never nears 2^32
	key := make([]byte, seal.KeySize)
	_, _ = rand.Read(key)
	wrapped, err := p.Wrap(ctx, id, key)
	if err != nil {
		return fmt.Errorf("%w: wrap data key %d: %w", ErrKeyring, id, err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO encryption_keys (id, wrapped_key, kek_ref, state)
		VALUES ($1, $2, $3, 'active')
		ON CONFLICT DO NOTHING`, next, wrapped, p.Ref()); err != nil {
		return fmt.Errorf("%w: create data key %d: %w", ErrKeyring, id, sql.Unavailable(err))
	}
	return nil
}

// Active is the ID of the data key that seals new values.
func (k *Keyring) Active() uint32 {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.active
}

func (k *Keyring) activeKey() (uint32, []byte) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.active, k.keys[k.active]
}

func (k *Keyring) key(id uint32) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w: key %d", ErrUnknownKey, id)
	}
	return key, nil
}

// Seal seals plaintext under the active data key with associated data
// aad, in seal's column format.
func (k *Keyring) Seal(_ context.Context, aad, plaintext []byte) ([]byte, error) {
	id, key := k.activeKey()
	return seal.SealColumn(key, id, aad, plaintext) //nolint:wrapcheck // seal's errors are the API
}

// Open opens a value Seal sealed with the same aad under an active or
// retiring data key.
func (k *Keyring) Open(_ context.Context, aad, sealed []byte) (Value, error) {
	id, err := seal.ColumnKeyID(sealed)
	if err != nil {
		return Value{}, err //nolint:wrapcheck // as above
	}
	key, err := k.key(id)
	if err != nil {
		return Value{}, err
	}
	plain, err := seal.OpenColumn(key, aad, sealed)
	if err != nil {
		return Value{}, err //nolint:wrapcheck // as above
	}
	return Value{plain}, nil
}

// SealColumn seals plaintext for column c of the row whose primary key
// parts are pk, so it opens in no other row or column.
func (k *Keyring) SealColumn(
	ctx context.Context,
	c Column,
	pk []string,
	plaintext []byte,
) ([]byte, error) {
	aad, err := c.AAD(pk)
	if err != nil {
		return nil, err
	}
	return k.Seal(ctx, aad, plaintext)
}

// OpenColumn opens a value SealColumn sealed for the same column and row.
func (k *Keyring) OpenColumn(
	ctx context.Context,
	c Column,
	pk []string,
	sealed []byte,
) (Value, error) {
	aad, err := c.AAD(pk)
	if err != nil {
		return Value{}, err
	}
	return k.Open(ctx, aad, sealed)
}

// DeriveKey returns a 256-bit key for purpose, HKDF-SHA256 of the active
// data key with info infraweave/<purpose>, and that data key's ID, which
// whatever the key makes carries for DerivedKey.
func (k *Keyring) DeriveKey(purpose string) ([]byte, uint32, error) {
	id, key := k.activeKey()
	d, err := derive(key, purpose)
	return d, id, err
}

// DerivedKey returns purpose's key derived from data key id, active or
// retiring, so a value made before a rotation still verifies.
func (k *Keyring) DerivedKey(purpose string, id uint32) ([]byte, error) {
	key, err := k.key(id)
	if err != nil {
		return nil, err
	}
	return derive(key, purpose)
}

func derive(key []byte, purpose string) ([]byte, error) {
	d, err := hkdf.Key(sha256.New, key, nil, "infraweave/"+purpose, seal.KeySize)
	if err != nil {
		return nil, fmt.Errorf("derive %s: %w", purpose, err)
	}
	return d, nil
}

// SealObject returns a writer that seals an object to w under a fresh
// content key, and that key sealed under the keyring with aad, which the
// caller stores beside the object.
func (k *Keyring) SealObject(
	ctx context.Context,
	w io.Writer,
	aad []byte,
) (*seal.Writer, []byte, error) {
	content := make([]byte, seal.KeySize)
	_, _ = rand.Read(content)
	sealedKey, err := k.Seal(ctx, aad, content)
	if err != nil {
		return nil, nil, err
	}
	sw, err := seal.NewWriter(w, content)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // as above
	}
	return sw, sealedKey, nil
}

// OpenObject opens the content key SealObject returned and returns a
// reader of the object r holds.
func (k *Keyring) OpenObject(
	ctx context.Context,
	r io.Reader,
	aad, sealedKey []byte,
) (*seal.Reader, error) {
	content, err := k.Open(ctx, aad, sealedKey)
	if err != nil {
		return nil, err
	}
	sr, err := seal.NewReader(r, content.Reveal())
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return sr, nil
}

// Column is a sealed column: a bytea column named <name>_sealed.
type Column struct {
	Table string
	Name  string
	// Key names the primary key columns, in the order their values join.
	Key []string
}

// ErrPrimaryKey reports primary key values that do not match a Column's
// key columns.
var ErrPrimaryKey = errors.New("primary key does not match the column's key")

// AAD is the associated data of the column's value in the row whose
// primary key parts are pk: <table>.<column>:<parts joined by />. A
// sealed object's content key, which SealObject seals, takes it too.
func (c Column) AAD(pk []string) ([]byte, error) {
	if len(pk) != len(c.Key) {
		return nil, fmt.Errorf(
			"%w: %s.%s takes %d parts, got %d",
			ErrPrimaryKey,
			c.Table,
			c.Name,
			len(c.Key),
			len(pk),
		)
	}
	return []byte(c.Table + "." + c.Name + ":" + strings.Join(pk, "/")), nil
}

// registry is every sealed column of every stream. A store that seals a
// column adds it here, and resealing after a rotation walks it;
// TestAccSealedColumnRegistry in internal/app keeps it equal to the
// _sealed columns of the migrated schema.
var registry = []Column{
	{Table: "blob_objects", Name: "content_key_sealed", Key: []string{"store", "key"}},
}

// Registry lists the sealed columns.
func Registry() []Column { return slices.Clone(registry) }
