package pg_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

func keyring(t *testing.T, db *pgxpool.Pool) *crypt.Keyring {
	t.Helper()
	p, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	k, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// failingSealer seals and opens nothing.
type failingSealer struct{}

var errSeal = errors.New("keyring down")

func (failingSealer) SealColumn(context.Context, crypt.Column, []string, []byte) ([]byte, error) {
	return nil, errSeal
}

func (failingSealer) OpenColumn(
	context.Context,
	crypt.Column,
	[]string,
	[]byte,
) (crypt.Value, error) {
	return crypt.Value{}, errSeal
}

// closingSealer seals by closing the pool, so the write that follows
// fails.
type closingSealer struct {
	failingSealer

	db *pgxpool.Pool
}

func (c closingSealer) SealColumn(context.Context, crypt.Column, []string, []byte) ([]byte, error) {
	c.db.Close()
	return []byte{1}, nil
}

// TestAccHandover checks that Handed finds a run by its ID alone, of
// whichever account has it, and that a run's artifact key is made once,
// sealed to its row, and opened on every later read, another run's
// differing.
func TestAccHandover(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, keyring(t, db))
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	id := insertRun(t, db, acct, stack, false)
	other := insertRun(t, db, kernel.NewAccountID(), kernel.NewULID(), false)

	run, err := s.Handed(ctx, id)
	if err != nil || run.Account != acct || run.ID != id || run.Stack != stack {
		t.Fatalf("Handed = %+v, %v", run, err)
	}
	if _, err := s.Handed(ctx, kernel.NewULID()); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Errorf("Handed of no run = %v", err)
	}

	key, err := s.ArtifactKey(ctx, run)
	if err != nil || len(key.Reveal()) != 32 {
		t.Fatalf("ArtifactKey = %d bytes, %v", len(key.Reveal()), err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx, `SELECT artifact_key_sealed FROM runs WHERE id = $1`,
		id).Scan(&sealed); err != nil || bytes.Contains(sealed, key.Reveal()) {
		t.Errorf("the key is stored as %x, %v", sealed, err)
	}
	if again, err := s.ArtifactKey(ctx, run); err != nil ||
		!bytes.Equal(again.Reveal(), key.Reveal()) {
		t.Errorf("ArtifactKey again = %v", err)
	}
	otherRun, err := s.Handed(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if theirs, err := s.ArtifactKey(ctx, otherRun); err != nil ||
		bytes.Equal(theirs.Reveal(), key.Reveal()) {
		t.Errorf("another run's key = %v, the same key", err)
	}
	gone := run
	gone.ID = kernel.NewULID()
	if _, err := s.ArtifactKey(ctx, gone); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Errorf("ArtifactKey of no run = %v", err)
	}
	if cols := pg.SealedColumns(); len(cols) != 2 || cols[0].Table != "runs" ||
		cols[0].Name != "artifact_key_sealed" || cols[1].Table != "runs" ||
		cols[1].Name != "env_overrides_sealed" {
		t.Errorf("SealedColumns = %+v", cols)
	}
}

// TestAccOverridesBroken checks that overrides the keyring cannot seal
// are not stored, that ones it cannot open or decode fail the read, that
// a run that is gone has none to read, and that the read and the write
// fail on a closed pool.
func TestAccOverridesBroken(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	ring := keyring(t, db)
	s, broken := pg.New(db, ring), pg.New(db, failingSealer{})
	run, err := s.Handed(ctx, insertRun(t, db, kernel.NewAccountID(), kernel.NewULID(), false))
	if err != nil {
		t.Fatal(err)
	}
	ovs := []lifecycle.Override{{Name: "A", Value: "1"}}
	if err := broken.SetOverrides(ctx, run, ovs); !errors.Is(err, errSeal) {
		t.Errorf("SetOverrides that cannot seal = %v", err)
	}
	if got, err := s.Overrides(ctx, run); err != nil || got != nil {
		t.Errorf("overrides that could not be sealed are stored: %+v, %v", got, err)
	}
	if err := s.SetOverrides(ctx, run, ovs); err != nil {
		t.Fatal(err)
	}
	if _, err := broken.Overrides(ctx, run); !errors.Is(err, errSeal) {
		t.Errorf("Overrides that cannot open = %v", err)
	}
	sealed, err := ring.SealColumn(ctx, crypt.Column{
		Table: "runs", Name: "env_overrides_sealed", Key: []string{"account_id", "id"},
	}, []string{run.Account.UUID(), run.ID.UUID()}, []byte("not JSON"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE runs SET env_overrides_sealed = $2 WHERE id = $1`,
		run.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Overrides(ctx, run); err == nil {
		t.Error("Overrides that do not decode succeeded")
	}
	gone := run
	gone.ID = kernel.NewULID()
	if _, err := s.Overrides(ctx, gone); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Errorf("Overrides of no run = %v", err)
	}

	if err := pg.New(db, closingSealer{db: db}).SetOverrides(ctx, run, ovs); err == nil {
		t.Error("SetOverrides whose write fails succeeded")
	}
	if _, err := s.Overrides(ctx, run); err == nil {
		t.Error("Overrides on a closed pool succeeded")
	}
}

// TestAccHandoverBroken checks that a key the keyring cannot seal is not
// stored, that one it cannot open fails the read, and that each read
// and the key's write fail on a closed pool.
func TestAccHandoverBroken(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	id := insertRun(t, db, kernel.NewAccountID(), kernel.NewULID(), false)
	broken := pg.New(db, failingSealer{})
	run, err := broken.Handed(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broken.ArtifactKey(ctx, run); !errors.Is(err, errSeal) {
		t.Errorf("ArtifactKey that cannot seal = %v", err)
	}
	var stored bool
	if err := db.QueryRow(ctx, `SELECT artifact_key_sealed IS NOT NULL FROM runs WHERE id = $1`,
		id).Scan(&stored); err != nil || stored {
		t.Errorf("a key that cannot be sealed is stored: %v, %v", stored, err)
	}
	if _, err := pg.New(db, keyring(t, db)).ArtifactKey(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := broken.ArtifactKey(ctx, run); !errors.Is(err, errSeal) {
		t.Errorf("ArtifactKey that cannot open = %v", err)
	}

	fresh, err := broken.Handed(ctx, insertRun(t, db, kernel.NewAccountID(), kernel.NewULID(),
		false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.New(db, closingSealer{db: db}).ArtifactKey(ctx, fresh); err == nil {
		t.Error("ArtifactKey whose write fails succeeded")
	}
	if _, err := broken.Handed(ctx, id); err == nil {
		t.Error("Handed on a closed pool succeeded")
	}
	if _, err := broken.ArtifactKey(ctx, fresh); err == nil {
		t.Error("ArtifactKey on a closed pool succeeded")
	}
}
