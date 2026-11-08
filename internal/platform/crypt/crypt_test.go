package crypt_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

func provider(t *testing.T, fill byte) kek.Provider {
	t.Helper()
	p, err := kek.OpenLocal(kek.LocalConfig{
		Key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func load(t *testing.T, db *pgxpool.Pool, p kek.Provider) *crypt.Keyring {
	t.Helper()
	k, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var secrets = crypt.Column{
	Table: "webhooks",
	Name:  "secret_sealed",
	Key:   []string{"account_id", "id"},
}

// TestAccKeyringFirstStart checks the keyring's first start: replicas
// starting together on an empty keyring end with one active key, which
// each of them holds.
//
// ref: decision 0013
func TestAccKeyringFirstStart(t *testing.T) {
	db := pool(t)
	p := provider(t, 1)
	const replicas = 8
	rings := make([]*crypt.Keyring, replicas)
	errs := make([]error, replicas)
	var wg sync.WaitGroup
	for i := range replicas {
		wg.Go(func() { rings[i], errs[i] = crypt.Load(t.Context(), db, p) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d: %v", i, err)
		}
	}
	var rows, active int
	if err := db.QueryRow(t.Context(), `
		SELECT count(*), count(*) FILTER (WHERE state = 'active') FROM encryption_keys`,
	).Scan(&rows, &active); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || active != 1 {
		t.Fatalf("%d keys, %d active; want 1 and 1", rows, active)
	}
	sealed, err := rings[0].SealColumn(
		t.Context(),
		secrets,
		[]string{"01JC0ACCOUNT", "01JC0HOOK"},
		[]byte("hush"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range rings {
		if k.Active() != 1 {
			t.Errorf("replica %d: active key %d", i, k.Active())
		}
		got, err := k.OpenColumn(
			t.Context(),
			secrets,
			[]string{"01JC0ACCOUNT", "01JC0HOOK"},
			sealed,
		)
		if err != nil || string(got.Reveal()) != "hush" {
			t.Errorf("replica %d opens %q, %v", i, got.Reveal(), err)
		}
	}
	var ref string
	if err := db.QueryRow(t.Context(), `SELECT kek_ref FROM encryption_keys`).
		Scan(&ref); err != nil ||
		ref != "local" {
		t.Errorf("kek_ref %q, %v", ref, err)
	}
}

// TestAccKeyringWrongKEK checks that a replica configured with another KEK
// stops, naming the key it could not unwrap.
func TestAccKeyringWrongKEK(t *testing.T) {
	db := pool(t)
	load(t, db, provider(t, 1))
	_, err := crypt.Load(t.Context(), db, provider(t, 2))
	if !errors.Is(err, kek.ErrUnwrap) || !strings.Contains(err.Error(), "data key 1") {
		t.Fatalf("got %v", err)
	}
}

// TestAccKeyringStates checks that a retiring key still opens and derives
// what it made, that the active key seals, and that a retired key does
// neither.
func TestAccKeyringStates(t *testing.T) {
	db := pool(t)
	p := provider(t, 1)
	old := load(t, db, p)
	pk := []string{"01JC0ACCOUNT", "01JC0HOOK"}
	sealedOld, err := old.SealColumn(t.Context(), secrets, pk, []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	derivedOld, id, err := old.DeriveKey("objectstore-url")
	if err != nil || id != 1 {
		t.Fatalf("derive: %d, %v", id, err)
	}
	wrapped, err := p.Wrap(t.Context(), 2, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `
		UPDATE encryption_keys SET state = 'retiring' WHERE id = 1;
		`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `
		INSERT INTO encryption_keys (id, wrapped_key, kek_ref, state) VALUES (2, $1, 'local', 'active')`,
		wrapped); err != nil {
		t.Fatal(err)
	}
	k := load(t, db, p)
	if k.Active() != 2 {
		t.Fatalf("active key %d", k.Active())
	}
	if got, err := k.OpenColumn(
		t.Context(),
		secrets,
		pk,
		sealedOld,
	); err != nil ||
		string(got.Reveal()) != "v1" {
		t.Errorf("a value under the retiring key: %q, %v", got.Reveal(), err)
	}
	sealedNew, err := k.SealColumn(t.Context(), secrets, pk, []byte("v2"))
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := seal.ColumnKeyID(sealedNew); id != 2 {
		t.Errorf("sealed under key %d", id)
	}
	again, err := k.DerivedKey("objectstore-url", 1)
	if err != nil || !bytes.Equal(again, derivedOld) {
		t.Errorf("the retiring key's derived key: %v", err)
	}
	derivedNew, id, err := k.DeriveKey("objectstore-url")
	if err != nil || id != 2 || bytes.Equal(derivedNew, derivedOld) {
		t.Errorf("the active key's derived key: %d, %v", id, err)
	}
	other, _, err := k.DeriveKey("state-upload-url")
	if err != nil || bytes.Equal(other, derivedNew) {
		t.Errorf("two purposes derive one key: %v", err)
	}
	if _, err := db.Exec(t.Context(), `
		UPDATE encryption_keys SET state = 'retired', retired_at = now() WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	k = load(t, db, p)
	if _, err := k.OpenColumn(
		t.Context(),
		secrets,
		pk,
		sealedOld,
	); !errors.Is(
		err,
		crypt.ErrUnknownKey,
	) {
		t.Errorf("a value under a retired key: %v", err)
	}
	if _, err := k.DerivedKey("objectstore-url", 1); !errors.Is(err, crypt.ErrUnknownKey) {
		t.Errorf("a retired key's derived key: %v", err)
	}
}

// TestAccEncryptionKeysSchema checks the table's own rules: one active
// key, and retired_at exactly on retired keys.
func TestAccEncryptionKeysSchema(t *testing.T) {
	db := pool(t)
	load(t, db, provider(t, 1))
	for name, stmt := range map[string]string{
		"a second active key": `INSERT INTO encryption_keys (id, wrapped_key, kek_ref, state) VALUES (2, '\x00', 'local', 'active')`,
		"retired, no time":    `UPDATE encryption_keys SET state = 'retired'`,
		"active, retired_at":  `UPDATE encryption_keys SET retired_at = now()`,
		"an unknown state":    `UPDATE encryption_keys SET state = 'lost'`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(t.Context(), stmt); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestAccKeyringUnavailable checks that a keyring that cannot reach
// PostgreSQL says so.
func TestAccKeyringUnavailable(t *testing.T) {
	db := pool(t)
	db.Close()
	if _, err := crypt.Load(t.Context(), db, provider(t, 1)); !errors.Is(err, crypt.ErrKeyring) {
		t.Errorf("got %v", err)
	}
}

// TestAccKeyringCreateFails checks that a first start whose key never
// lands, dropped or refused, fails rather than running without a key.
func TestAccKeyringCreateFails(t *testing.T) {
	for name, body := range map[string]string{
		"dropped": "RETURN NULL;",
		"refused": "RAISE EXCEPTION 'read-only keyring';",
	} {
		t.Run(name, func(t *testing.T) {
			db := pool(t)
			if _, err := db.Exec(t.Context(), `
				CREATE FUNCTION no_keys() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+body+` END $$;
				CREATE TRIGGER no_keys BEFORE INSERT ON encryption_keys
					FOR EACH ROW EXECUTE FUNCTION no_keys();`); err != nil {
				t.Fatal(err)
			}
			if _, err := crypt.Load(
				t.Context(),
				db,
				provider(t, 1),
			); !errors.Is(
				err,
				crypt.ErrKeyring,
			) {
				t.Errorf("got %v", err)
			}
		})
	}
}

// TestAccSealedValues checks the column helpers and the sealed object
// through a loaded keyring.
func TestAccSealedValues(t *testing.T) {
	k := load(t, pool(t), provider(t, 1))
	ctx := t.Context()
	pk := []string{"01JC0ACCOUNT", "01JC0HOOK"}
	sealed, err := k.SealColumn(ctx, secrets, pk, []byte("hush"))
	if err != nil {
		t.Fatal(err)
	}
	other := crypt.Column{Table: "webhooks", Name: "previous_secret_sealed", Key: secrets.Key}
	for name, open := range map[string]func() (crypt.Value, error){
		"another row": func() (crypt.Value, error) {
			return k.OpenColumn(ctx, secrets, []string{"01JC0ACCOUNT", "01JC0OTHER"}, sealed)
		},
		"another column": func() (crypt.Value, error) { return k.OpenColumn(ctx, other, pk, sealed) },
		"raw AAD":        func() (crypt.Value, error) { return k.Open(ctx, []byte("webhooks.secret_sealed:01JC0ACCOUNT"), sealed) },
		"not sealed":     func() (crypt.Value, error) { return k.Open(ctx, nil, []byte{1}) },
	} {
		if _, err := open(); err == nil {
			t.Errorf("%s opened", name)
		}
	}
	if got, err := k.Open(
		ctx,
		[]byte("webhooks.secret_sealed:01JC0ACCOUNT/01JC0HOOK"),
		sealed,
	); err != nil ||
		string(got.Reveal()) != "hush" {
		t.Errorf("the column's AAD is not <table>.<column>:<pk>: %v", err)
	}
	if _, err := k.SealColumn(ctx, secrets, pk[:1], nil); !errors.Is(err, crypt.ErrPrimaryKey) {
		t.Errorf("one part of a two-part key: %v", err)
	}
	if _, err := k.OpenColumn(ctx, secrets, nil, sealed); !errors.Is(err, crypt.ErrPrimaryKey) {
		t.Errorf("no key parts: %v", err)
	}

	aad := []byte("blob_objects:states/01JC0STACK/1.tfstate")
	var buf bytes.Buffer
	w, sealedKey, err := k.SealObject(ctx, &buf, aad)
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte("state "), 30000)
	if _, err := w.Write(object); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := k.OpenObject(ctx, bytes.NewReader(buf.Bytes()), aad, sealedKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, object) {
		t.Errorf("object round trip: %d bytes, %v", len(got), err)
	}
	if _, err := k.OpenObject(
		ctx,
		bytes.NewReader(buf.Bytes()),
		[]byte("blob_objects:states/other"),
		sealedKey,
	); !errors.Is(
		err,
		seal.ErrAuth,
	) {
		t.Errorf("a content key moved to another object: %v", err)
	}
	if _, err := k.OpenObject(
		ctx,
		bytes.NewReader(nil),
		aad,
		sealedKey,
	); !errors.Is(
		err,
		seal.ErrFormat,
	) {
		t.Errorf("an empty object: %v", err)
	}
}

func TestUnitValueRedacted(t *testing.T) {
	v := crypt.ValueOf([]byte("hunter2"))
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("opened", "secret", v)
	doc, err := json.Marshal(map[string]any{"secret": v})
	if err != nil {
		t.Fatal(err)
	}
	text, err := v.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v":            fmt.Sprintf("%v", v),
		"%s":            fmt.Sprintf("%s", v),
		"%+v":           fmt.Sprintf("%+v", v),
		"%#v":           fmt.Sprintf("%#v", v),
		"%x":            fmt.Sprintf("%x", v),
		"%q":            fmt.Sprintf("%q", v),
		"in a struct":   fmt.Sprintf("%+v", struct{ S crypt.Value }{v}),
		"String":        v.String(),
		"GoString":      v.GoString(),
		"slog":          log.String(),
		"encoding/json": string(doc),
		"MarshalText":   string(text),
		"in an error":   fmt.Errorf("open: %v", v).Error(),
	} {
		if strings.Contains(out, "hunter2") || strings.Contains(out, "68756e74657232") ||
			!strings.Contains(out, "[redacted]") {
			t.Errorf("%s renders %q", name, out)
		}
	}
	if string(v.Reveal()) != "hunter2" {
		t.Errorf("Reveal = %q", v.Reveal())
	}
}

func TestUnitRegistry(t *testing.T) {
	r := crypt.Registry()
	for _, c := range r {
		if !strings.HasSuffix(c.Name, "_sealed") || len(c.Key) == 0 {
			t.Errorf("%s.%s: a sealed column is <name>_sealed with a primary key", c.Table, c.Name)
		}
	}
	r = append(r, crypt.Column{Table: "x"})
	if len(crypt.Registry()) == len(r) {
		t.Error("Registry returns its own slice")
	}
}

func TestUnitConfig(t *testing.T) {
	if err := crypt.Defaults().Validate(); err != nil {
		t.Errorf("defaults: %v", err)
	}
	if err := (crypt.Config{RotationInterval: -time.Hour}).Validate(); !errors.Is(
		err,
		kek.ErrConfig,
	) ||
		!strings.Contains(err.Error(), "secrets.rotation_interval") {
		t.Errorf("a negative interval: %v", err)
	}
	if err := (crypt.Config{KEK: kek.Config{Provider: "vault"}}).Validate(); !errors.Is(
		err,
		kek.ErrConfig,
	) {
		t.Errorf("unknown provider: %v", err)
	}
}

// approvedOnly are the calls Go refuses under fips140=only: GCM with a
// caller's nonce, MD5 and SHA-1, and ciphers no certificate covers.
//
// ref: decision 0013
var approvedOnly = map[string]string{
	"cipher.NewGCM":              "GCM with a caller's nonce; use cipher.NewGCMWithRandomNonce",
	"cipher.NewGCMWithNonceSize": "GCM with a caller's nonce",
	"cipher.NewGCMWithTagSize":   "GCM with a caller's nonce",
	"crypto/md5":                 "MD5",
	"crypto/sha1":                "SHA-1",
	"crypto/des":                 "DES",
	"crypto/rc4":                 "RC4",
	"golang.org/x/crypto":        "a module outside the certified one",
}

// TestUnitApprovedConstructions reads the sources of seal, crypt and kek
// and fails on any construction FIPS 140-only mode refuses, so one fails
// review here rather than an operator's start.
func TestUnitApprovedConstructions(t *testing.T) {
	for _, dir := range []string{"../seal", ".", "kek"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				for banned, why := range approvedOnly {
					if p == banned || strings.HasPrefix(p, banned+"/") {
						t.Errorf("%s imports %s: %s", path, p, why)
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok {
						if why, banned := approvedOnly[x.Name+"."+sel.Sel.Name]; banned {
							t.Errorf("%s calls %s.%s: %s", path, x.Name, sel.Sel.Name, why)
						}
					}
				}
				return true
			})
		}
	}
}
