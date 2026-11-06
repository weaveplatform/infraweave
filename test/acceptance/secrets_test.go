package acceptance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func (s *servedSteps) registerSecrets(sc *godog.ScenarioContext) {
	sc.Step(`^the object "([^"]+)" in the states store holds "([^"]*)"$`, s.putObject)
	sc.Step(`^the object "([^"]+)" in the states store reads "([^"]*)"$`, s.readsObject)
	sc.Step(`^the operator runs infraweave secrets rotate$`, s.rotate)
	sc.Step(`^the operator runs infraweave secrets rewrap to Vault$`, s.rewrapToVault)
	sc.Step(`^data key (\d+) is eventually deleted$`, s.keyDeleted)
	sc.Step(`^every sealed value is under data key (\d+)$`, s.everyValueUnder)
	sc.Step(`^data key (\d+) is wrapped by Vault$`, s.wrappedByVault)
	sc.Step(`^servers A and B restart with the Vault KEK$`, s.restartOnVault)
}

// keyring is the keyring of the scenario's database, loaded as a server
// loads it, under the KEK the servers run with, with the pool it reads
// through, which the caller closes.
func (s *servedSteps) keyring(
	ctx context.Context,
) (keys *crypt.Keyring, pool *pgxpool.Pool, err error) {
	db, err := s.w.database(ctx)
	if err != nil {
		return nil, nil, err
	}
	var p kek.Provider
	if s.vault != nil {
		p, err = vault.New(*s.vault, vault.Options{})
	} else {
		p, err = kek.OpenLocal(kek.LocalConfig{Key: KEK})
	}
	if err != nil {
		return nil, nil, fmt.Errorf("keyring: %w", err)
	}
	if pool, err = pgxpool.New(ctx, db.DSN); err != nil {
		return nil, nil, fmt.Errorf("keyring: %w", err)
	}
	if keys, err = crypt.Load(ctx, pool, p); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("keyring: %w", err)
	}
	return keys, pool, nil
}

// objects is serve's blob store over the scenario's object storage, the fs
// root its servers share, sealing and opening under keys.
func (s *servedSteps) objects(pool *pgxpool.Pool, keys *crypt.Keyring) (*blobs.Objects, error) {
	c := objectstore.Defaults()
	c.Driver, c.FS.Root, c.FS.URL = objectstore.DriverFS, s.w.objects(), "http://infraweave.test"
	store, err := objectstore.New(
		c,
		func(string) (objectstore.Bucket, error) { return fs.New(c.FS, keys) },
	)
	if err != nil {
		return nil, fmt.Errorf("object store: %w", err)
	}
	return blobs.New(pool, queue.New(pool), objectstore.WriterOnly(store), keys, c), nil
}

// putObject writes and commits a sealed object as serve does; no
// operation writes one yet.
func (s *servedSteps) putObject(ctx context.Context, key, body string) error {
	keys, pool, err := s.keyring(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	o, err := s.objects(pool, keys)
	if err != nil {
		return err
	}
	if _, err := o.Put(
		ctx,
		objectstore.States,
		key,
		strings.NewReader(body),
		int64(len(body)),
	); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	if err := tx.Do(ctx, pool, tx.Options{}, func(ctx context.Context) error {
		return o.Commit(ctx, objectstore.States, key)
	}); err != nil {
		return fmt.Errorf("commit %s: %w", key, err)
	}
	return nil
}

// readsObject opens the object as serve's reader does, with a keyring
// loaded now, so it holds whatever keys the database holds now.
func (s *servedSteps) readsObject(ctx context.Context, key, want string) error {
	keys, pool, err := s.keyring(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	o, err := s.objects(pool, keys)
	if err != nil {
		return err
	}
	r, err := o.Open(ctx, objectstore.States, key)
	if err != nil {
		return fmt.Errorf("%w: open %s: %w", errStep, key, err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", errStep, key, err)
	}
	if string(got) != want {
		return fmt.Errorf("%w: %s reads %q, want %q", errStep, key, got, want)
	}
	return nil
}

// operator runs bin secrets with args and the servers' configuration, as
// an operator's one-shot job does, and returns its output.
func (s *servedSteps) operator(ctx context.Context, args ...string) (string, error) {
	db, err := s.w.database(ctx)
	if err != nil {
		return "", err
	}
	bin, err := binary(ctx)
	if err != nil {
		return "", err
	}
	config, err := yaml.Marshal(map[string]any{
		"database": map[string]any{"url": db.DSN},
		"secrets":  secretsWith(s.w.kek),
	})
	if err != nil {
		return "", fmt.Errorf("operator config: %w", err)
	}
	path := filepath.Join(s.w.dir, "operator.yaml")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return "", fmt.Errorf("operator config: %w", err)
	}
	cmd := exec.CommandContext(
		ctx,
		bin,
		append(append([]string{"secrets"}, args...), "--config", path)...)
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+s.w.s.coverDir, "INFRAWEAVE_SECRETS_KEK="+KEK)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: infraweave secrets %s: %w: %s",
			errStep, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func (s *servedSteps) rotate(ctx context.Context) error {
	_, err := s.operator(ctx, "rotate")
	return err
}

// rewrapToVault rewraps the data keys to a Transit key of the scenario's
// own, with a token file, and runs every later command on Vault.
func (s *servedSteps) rewrapToVault(ctx context.Context) error {
	v, err := s.w.s.deps.Vault(ctx)
	if err != nil {
		return err
	}
	name, err := s.w.s.deps.VaultKey(ctx)
	if err != nil {
		return err
	}
	token := filepath.Join(s.w.dir, "vault-token")
	if err := os.WriteFile(token, []byte(v.Token), 0o600); err != nil {
		return fmt.Errorf("vault token: %w", err)
	}
	c := kek.Defaults().Vault
	c.Address, c.KeyName = v.Addr, name
	c.Auth.Method, c.Auth.TokenFile = kek.VaultToken, token
	settings := map[string]any{"provider": kek.Vault, "vault": map[string]any{
		"address":  c.Address,
		"key_name": c.KeyName,
		"auth":     map[string]any{"method": c.Auth.Method, "token_file": c.Auth.TokenFile},
	}}
	to, err := yaml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("vault settings: %w", err)
	}
	path := filepath.Join(s.w.dir, "to-vault.yaml")
	if err := os.WriteFile(path, to, 0o600); err != nil {
		return fmt.Errorf("vault settings: %w", err)
	}
	if _, err := s.operator(ctx, "rewrap", "--to", path); err != nil {
		return err
	}
	s.w.kek, s.vault = settings, &c
	return nil
}

// restartOnVault stops both servers and starts two on the Vault KEK, as an
// operator rolls the replicas after a rewrap.
func (s *servedSteps) restartOnVault(ctx context.Context) error {
	if s.w.kek == nil {
		return fmt.Errorf("%w: no rewrap to Vault ran", errStep)
	}
	var err error
	for _, srv := range s.w.servers {
		err = errors.Join(err, srv.Stop(ctx))
	}
	if err != nil {
		return err
	}
	s.w.servers, s.w.apis, s.w.api, s.w.admin = nil, nil, nil, nil
	return s.twoReplicas(ctx)
}

func (s *servedSteps) keyDeleted(ctx context.Context, id int) error {
	return Eventually(ctx, fmt.Sprintf("data key %d to be deleted", id),
		func(ctx context.Context) (bool, error) {
			out, err := s.query(
				ctx,
				fmt.Sprintf(`SELECT count(*) FROM encryption_keys WHERE id = %d`, id),
			)
			return strings.TrimSpace(out) == "0", err
		})
}

func (s *servedSteps) wrappedByVault(ctx context.Context, id int) error {
	out, err := s.query(ctx, fmt.Sprintf(`SELECT kek_ref FROM encryption_keys WHERE id = %d`, id))
	if err != nil {
		return err
	}
	if want := "vault:" + s.vault.Address + "/transit/" + s.vault.KeyName; strings.TrimSpace(
		out,
	) != want {
		return fmt.Errorf("%w: data key %d's kek_ref is %q, want %q", errStep, id, out, want)
	}
	return nil
}

// everyValueUnder checks that every sealed value of crypt.Registry names
// data key id, and that the scenario sealed at least one.
func (s *servedSteps) everyValueUnder(ctx context.Context, id int) error {
	_, pool, err := s.keyring(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	n := 0
	for _, c := range crypt.Registry() {
		rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE %s IS NOT NULL`,
			c.Name, c.Table, c.Name))
		if err != nil {
			return fmt.Errorf("%s.%s: %w", c.Table, c.Name, err)
		}
		values, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
		if err != nil {
			return fmt.Errorf("%s.%s: %w", c.Table, c.Name, err)
		}
		for _, v := range values {
			got, err := seal.ColumnKeyID(v)
			if err != nil {
				return fmt.Errorf("%w: a value of %s.%s: %w", errStep, c.Table, c.Name, err)
			}
			if got != uint32(id) { //nolint:gosec // a step's small positive integer
				return fmt.Errorf("%w: a value of %s.%s is under data key %d, want %d",
					errStep, c.Table, c.Name, got, id)
			}
			n++
		}
	}
	if n == 0 {
		return fmt.Errorf("%w: the scenario sealed no value", errStep)
	}
	return nil
}

// sealBody seals body as the servers seal a stored response under key.
func (s *servedSteps) sealBody(ctx context.Context, key string, body []byte) ([]byte, error) {
	keys, pool, err := s.keyring(ctx)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	sealed, err := keys.SealColumn(ctx, idempotency.Body, []string{loopback, key}, body)
	if err != nil {
		return nil, fmt.Errorf("seal a stored body: %w", err)
	}
	return sealed, nil
}
