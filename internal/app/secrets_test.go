package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/private"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// onDB is local's configuration on db's database.
func onDB(t *testing.T, db *sql.DB) config.Config {
	t.Helper()
	c := local(t)
	c.Database.URL = db.Primary().Config().ConnString()
	return c
}

// newKEK is the configuration of a new local KEK, in a file of its own.
func newKEK(t *testing.T) kek.Config {
	t.Helper()
	file := filepath.Join(t.TempDir(), "kek")
	if err := GenerateKEK(file); err != nil {
		t.Fatal(err)
	}
	return kek.Config{Provider: kek.Local, Local: kek.LocalConfig{KeyFile: file}}
}

func TestUnitGenerateKEK(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kek")
	if err := GenerateKEK(file); err != nil {
		t.Fatal(err)
	}
	if err := private.Check(file); err != nil {
		t.Error(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kek.ParseKey(string(b)); err != nil {
		t.Errorf("ParseKey: %v", err)
	}
	if err := GenerateKEK(file); !errors.Is(err, os.ErrExist) {
		t.Errorf("over a KEK that exists: %v", err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(b) {
		t.Error("the KEK that exists changed")
	}
	if err := GenerateKEK(filepath.Join(file, "below-a-file")); err == nil {
		t.Error("a file below a file")
	}
}

// TestAccSecretsTasks checks secrets.rotate and secrets.reseal: rotate
// leaves a key younger than secrets.rotation_interval, and every key when
// the interval is 0; an old key is rotated with secrets.reseal enqueued in
// the same transaction; a rotation under way is skipped; and the reseal
// leaves only the new key.
func TestAccSecretsTasks(t *testing.T) {
	db := sqltest.Open(t)
	testKeyring(t, db.Primary())
	p, _, err := openKEK(t.Context(), local(t).Secrets.KEK)
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(db.Primary())
	c := local(t)
	run := func(c config.Config) error {
		rotate, _ := secretsTasks(c, db.Primary(), q, p, discard())
		return rotate.Handler(t.Context(), queue.Job{})
	}
	keys := func() int { return rows(t, db, `SELECT count(*) FROM encryption_keys`) }
	reseals := func() int {
		return rows(t, db, `SELECT count(*) FROM jobs WHERE kind = 'secrets.reseal'`)
	}

	c.Secrets.RotationInterval = 0
	if err := run(c); err != nil || keys() != 1 {
		t.Fatalf("interval 0: %v, %d keys", err, keys())
	}
	c.Secrets.RotationInterval = time.Hour
	if err := run(c); err != nil || keys() != 1 {
		t.Fatalf("a young key: %v, %d keys", err, keys())
	}
	if _, err := db.Primary().Exec(t.Context(),
		`UPDATE encryption_keys SET created_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if err := run(c); err != nil || keys() != 2 || reseals() != 1 {
		t.Fatalf("an old key: %v, %d keys, %d reseals", err, keys(), reseals())
	}
	if err := run(c); err != nil || keys() != 2 {
		t.Fatalf("a rotation under way: %v, %d keys", err, keys())
	}
	if err := SecretsRotate(
		t.Context(),
		onDB(t, db),
		&strings.Builder{},
	); !errors.Is(
		err,
		crypt.ErrRotating,
	) {
		t.Errorf("SecretsRotate under way: %v", err)
	}
	_, reseal := secretsTasks(c, db.Primary(), q, p, discard())
	if err := reseal(t.Context(), queue.Job{}); err != nil {
		t.Fatal(err)
	}
	if n := rows(
		t,
		db,
		`SELECT count(*) FROM encryption_keys WHERE id = 2 AND state = 'active'`,
	); n != 1 ||
		keys() != 1 {
		t.Errorf("after the reseal: %d keys", keys())
	}

	closed := sqltest.Open(t)
	closed.Close()
	rotate, reseal := secretsTasks(c, closed.Primary(), q, p, discard())
	if err := rotate.Handler(t.Context(), queue.Job{}); err == nil {
		t.Error("rotate on a closed pool")
	}
	if err := reseal(t.Context(), queue.Job{}); err == nil {
		t.Error("reseal on a closed pool")
	}
}

// TestAccSecretsRotateInAll checks infraweave secrets rotate against a
// running all: the rotation commits with its secrets.reseal, all reloads
// its keyring, its scheduler reseals and deletes the old key, and it
// still serves with the new one.
func TestAccSecretsRotateInAll(t *testing.T) {
	observer := sqltest.Open(t)
	out, _, stop := runMode(t, observer, ModeAll)
	defer stop()
	var w strings.Builder
	if err := SecretsRotate(t.Context(), onDB(t, observer), &w); err != nil ||
		!strings.Contains(w.String(), "rotated the data key") {
		t.Fatalf("SecretsRotate: %v %q", err, w.String())
	}
	until(t, "the old key deleted", func() bool {
		return rows(t, observer, `SELECT count(*) FROM encryption_keys WHERE id = 1`) == 0
	})
	until(t, "all to reload", func() bool {
		return strings.Contains(out.String(), "keyring reloaded") &&
			strings.Contains(out.String(), "active_key=2")
	})
}

// TestAccSecretsRewrap checks infraweave secrets rewrap: every key moves
// to the new KEK, which then loads the keyring; a rewrap the keys are
// already under finds nothing to do; and one from a KEK that unwraps
// nothing stops, naming the key.
func TestAccSecretsRewrap(t *testing.T) {
	db := sqltest.Open(t)
	testKeyring(t, db.Primary())
	c := onDB(t, db)
	to := newKEK(t)
	var w strings.Builder
	if err := SecretsRewrap(t.Context(), c, to, &w); err != nil ||
		w.String() != "rewrapped 1 data keys to local\n" {
		t.Fatalf("SecretsRewrap: %v %q", err, w.String())
	}
	p, _, err := openKEK(t.Context(), to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypt.Load(t.Context(), db.Primary(), p); err != nil {
		t.Errorf("Load under the new KEK: %v", err)
	}
	w.Reset()
	if err := SecretsRewrap(t.Context(), c, to, &w); err != nil ||
		w.String() != "rewrapped 0 data keys to local\n" {
		t.Errorf("again: %v %q", err, w.String())
	}
	if err := SecretsRewrap(t.Context(), c, newKEK(t), &w); !errors.Is(err, crypt.ErrKeyring) {
		t.Errorf("from a KEK that unwraps nothing: %v", err)
	}
}

// TestAccSecretsFailures checks that rotate and rewrap stop before the
// database on a KEK they cannot open, and on a database they cannot reach.
func TestAccSecretsFailures(t *testing.T) {
	db := sqltest.Open(t)
	good := onDB(t, db)
	noKEK := good
	noKEK.Secrets.KEK.Provider = ""
	unreachable := good
	unreachable.Database.URL = "postgres://infraweave@127.0.0.1:1/infraweave?connect_timeout=1"
	ctx := context.Background()
	for name, f := range map[string]func() error{
		"rotate without a KEK": func() error { return SecretsRotate(ctx, noKEK, &strings.Builder{}) },
		"rotate unreachable":   func() error { return SecretsRotate(ctx, unreachable, &strings.Builder{}) },
		"rewrap without a KEK": func() error {
			return SecretsRewrap(ctx, noKEK, newKEK(t), &strings.Builder{})
		},
		"rewrap to no KEK": func() error {
			return SecretsRewrap(ctx, good, kek.Config{Provider: kek.Vault}, &strings.Builder{})
		},
		"rewrap unreachable": func() error {
			return SecretsRewrap(ctx, unreachable, newKEK(t), &strings.Builder{})
		},
	} {
		if err := f(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if rows(t, db, `SELECT count(*) FROM encryption_keys`) != 0 {
		t.Error("a failed command wrote keys")
	}
}
