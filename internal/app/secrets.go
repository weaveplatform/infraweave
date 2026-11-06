package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// The secrets kinds of decision 0013. secrets.rotate is periodic and
// rotates the data key once it is secrets.rotation_interval old;
// secrets.reseal is enqueued with each rotation.
var (
	RotateKeys = queue.Kind{
		Name:   "secrets.rotate",
		Queue:  queue.Maintenance,
		Policy: queue.Ignore,
	}
	ResealValues = queue.Kind{
		Name:   "secrets.reseal",
		Queue:  queue.Maintenance,
		Policy: queue.Ignore,
	}
)

// RotateEvery is how often secrets.rotate compares the active key's age
// with secrets.rotation_interval.
const RotateEvery = 24 * time.Hour

// keysDB is a pool the secrets operations run on; *pgxpool.Pool is one.
type keysDB interface {
	crypt.DB
	tx.Beginner
}

// rotateKeys rotates the data key, wrapped by p, once the active key is
// olderThan old, and enqueues secrets.reseal in the same transaction, so a
// rotation never commits without the job that retires the old key.
func rotateKeys(
	ctx context.Context,
	db keysDB,
	q *queue.Queue,
	p kek.Provider,
	olderThan time.Duration,
) (bool, error) {
	var rotated bool
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if rotated, err = crypt.Rotate(ctx, tx.Q(ctx, db), p, olderThan); err != nil || !rotated {
			return err //nolint:wrapcheck // names the keyring
		}
		_, err = q.EnqueueTx(ctx, ResealValues, nil)
		return err //nolint:wrapcheck // names the kind
	})
	return rotated, err //nolint:wrapcheck // as above
}

// secretsTasks are the periodic secrets.rotate, which rotates nothing
// while secrets.rotation_interval is 0 or a rotation is under way, and the
// handler of the secrets.reseal it enqueues.
func secretsTasks(
	c config.Config,
	db keysDB,
	q *queue.Queue,
	p kek.Provider,
	log *slog.Logger,
) (rotate periodic, reseal queue.Handler) {
	rotate = periodic{RotateKeys, RotateEvery, func(ctx context.Context, _ queue.Job) error {
		if c.Secrets.RotationInterval == 0 {
			return nil
		}
		rotated, err := rotateKeys(ctx, db, q, p, c.Secrets.RotationInterval)
		if errors.Is(err, crypt.ErrRotating) {
			log.Debug("data key rotation skipped", "reason", err)
			return nil
		}
		if rotated {
			log.Info("data key rotated; secrets.reseal enqueued")
		}
		return err
	}}
	reseal = func(ctx context.Context, _ queue.Job) error {
		n, err := crypt.Reseal(ctx, db, p)
		if err == nil {
			log.Info("sealed values resealed under the active data key", "values", n)
		}
		return err //nolint:wrapcheck // Reseal names the keyring
	}
	return rotate, reseal
}

// SecretsRotate is infraweave secrets rotate: it rotates the data key now,
// whatever its age, enqueues the secrets.reseal a scheduler runs, and
// writes the outcome to w. A rotation still under way is ErrRotating.
func SecretsRotate(ctx context.Context, c config.Config, w io.Writer) error {
	provider, closeKEK, err := openKEK(ctx, c.Secrets.KEK)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer closeKEK()
	db, _, err := openWithSession(ctx, c.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := rotateKeys(ctx, db.Primary(), queue.New(db.Primary()), provider, 0); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(
		w,
		"rotated the data key; a scheduler reseals under it and deletes the old key",
	)
	return nil
}

// SecretsRewrap is infraweave secrets rewrap: it rewraps every data key
// from the KEK the configuration names to the one to names, and writes how
// many it rewrapped to w. The replicas then restart with to's settings.
func SecretsRewrap(ctx context.Context, c config.Config, to kek.Config, w io.Writer) error {
	from, closeFrom, err := openKEK(ctx, c.Secrets.KEK)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer closeFrom()
	target, closeTo, err := openKEK(ctx, to)
	if err != nil {
		return fmt.Errorf("--to: %w", err)
	}
	defer closeTo()
	db, _, err := openWithSession(ctx, c.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	n, err := crypt.Rewrap(ctx, db.Primary(), from, target)
	if err != nil {
		return err //nolint:wrapcheck // names the keyring and the key
	}
	_, _ = fmt.Fprintf(w, "rewrapped %d data keys to %s\n", n, target.Ref())
	return nil
}

// GenerateKEK is infraweave secrets generate-kek: it writes a new local
// KEK, base64 of 32 random bytes, to file, which it creates readable only
// by its owner. It refuses a file that exists, so a KEK in use is never
// overwritten.
func GenerateKEK(file string) error {
	key := make([]byte, 32)
	_, _ = rand.Read(key) // crypto/rand.Read never fails
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("generate KEK: %w", err)
	}
	_, err = fmt.Fprintln(f, base64.StdEncoding.EncodeToString(key))
	if err = errors.Join(err, f.Close()); err != nil {
		return fmt.Errorf("generate KEK: %w", err)
	}
	return nil
}
