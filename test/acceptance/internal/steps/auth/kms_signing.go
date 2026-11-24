package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// kmsSteps drive the kms token-signing backend on the @vault container's
// Transit engine.
//
// ref: decision 0006
type kmsSteps struct {
	w *harness.World
	// ref and federation are the scenario's auth.signing.kms_keys and
	// auth.federation.kms_keys references, <mount>/<key>/<version>.
	ref, federation string
	// err is why the last start failed.
	err error
}

func (k *kmsSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^two infraweave servers, A and B, on one database, signing with an ecdsa-p256 Transit key$`,
		k.twoOnTransit,
	)
	sc.Step(`^the signing key's row names the Transit key and holds no private half$`, k.row)
	sc.Step(
		`^signing_keys holds no federation key, since no federation token has been signed$`,
		k.noFederationRow,
	)
	sc.Step(
		`^an infraweave server signing with an aes256-gcm96 Transit key is started$`,
		k.wrongType,
	)
	sc.Step(
		`^an infraweave server signing federation tokens with an (ecdsa-p256|rsa-2048) Transit key is started$`,
		k.wrongFederationType,
	)
	sc.Step(
		`^an infraweave server signing with an ecdsa-p256 Transit key and no federation key is started$`,
		k.noFederationKey,
	)
	sc.Step(`^it exits before it listens, naming the Transit key and ES256$`, func() error {
		return k.refusedNaming("vault:"+k.ref, "ES256")
	})
	sc.Step(`^it exits before it listens, naming the federation Transit key and RS256$`, func() error {
		return k.refusedNaming("vault:"+k.federation, "RS256")
	})
	sc.Step(`^it exits before it listens, naming auth\.federation\.kms_keys$`, func() error {
		return k.refusedNaming("auth.federation.kms_keys is required")
	})
}

// onTransit makes a Transit key of typ for the api-token purpose and, when
// federation is not empty, one of that type for the federation purpose,
// and sets every server it starts to sign with their versions 1, reaching
// Vault through secrets.kek.vault with a token file while the KEK stays
// local.
func (k *kmsSteps) onTransit(ctx context.Context, typ, federation string) error {
	w := k.w
	v, err := w.Suite.Deps.Vault(ctx)
	if err != nil {
		return err
	}
	serial := w.Suite.Deps.Serial()
	name, fedName := fmt.Sprintf("signing-%d", serial), fmt.Sprintf("federation-%d", serial)
	if err := v.Post(ctx, "transit/keys/"+name, `{"type":"`+typ+`"}`); err != nil {
		return fmt.Errorf("@vault: %w", err)
	}
	token := filepath.Join(w.Dir, "vault-token")
	if err := os.WriteFile(token, []byte(v.Token), 0o600); err != nil {
		return fmt.Errorf("vault token: %w", err)
	}
	k.ref = "transit/" + name + "/1"
	w.SetKEK(map[string]any{"provider": "local", "vault": map[string]any{
		"address": v.Addr,
		"auth":    map[string]any{"method": "token", "token_file": token},
	}})
	auth := map[string]any{"signing": map[string]any{
		"backend":  "kms",
		"kms_keys": []map[string]any{{"provider": "vault", "ref": k.ref}},
	}}
	if federation != "" {
		if err := v.Post(ctx, "transit/keys/"+fedName, `{"type":"`+federation+`"}`); err != nil {
			return fmt.Errorf("@vault: %w", err)
		}
		k.federation = "transit/" + fedName + "/1"
		auth["federation"] = map[string]any{
			"kms_keys": []map[string]any{{"provider": "vault", "ref": k.federation}},
		}
	}
	w.Settings["auth"] = auth
	return nil
}

func (k *kmsSteps) twoOnTransit(ctx context.Context) error {
	if err := k.onTransit(ctx, "ecdsa-p256", "rsa-3072"); err != nil {
		return err
	}
	return k.w.TwoReplicas(ctx)
}

// row checks that the one api-token signing key is the Transit key's:
// kms_key_ref names it and private_key_sealed is empty.
func (k *kmsSteps) row(ctx context.Context) error {
	return k.rowOf(ctx, "api-token", "1|vault:"+k.ref+"|ES256|f")
}

// noFederationRow checks that starting the servers, which opened the
// federation Transit key, created no federation signing key.
func (k *kmsSteps) noFederationRow(ctx context.Context) error {
	return k.rowOf(ctx, "federation", "0|||f")
}

// rowOf checks purpose's signing keys: their count, and the KMS
// reference, algorithm and whether a private half is sealed, of the last.
func (k *kmsSteps) rowOf(ctx context.Context, purpose, want string) error {
	db, err := k.w.Querier(ctx)
	if err != nil {
		return err
	}
	got, err := db.Query(ctx, `
SELECT count(*), coalesce(max(kms_key_ref), ''), coalesce(max(algorithm), ''),
    coalesce(bool_or(private_key_sealed IS NOT NULL), false)
FROM signing_keys WHERE purpose = '`+purpose+`'`)
	if err != nil {
		return fmt.Errorf("signing_keys: %w", err)
	}
	if strings.TrimSpace(got) != want {
		return fmt.Errorf(
			"%w: signing_keys holds %q (count, kms_key_ref, algorithm, sealed) of %s, want %q",
			harness.ErrStep, strings.TrimSpace(got), purpose, want)
	}
	return nil
}

func (k *kmsSteps) wrongType(ctx context.Context) error {
	return k.start(ctx, "aes256-gcm96", "rsa-3072")
}

func (k *kmsSteps) wrongFederationType(ctx context.Context, typ string) error {
	return k.start(ctx, "ecdsa-p256", typ)
}

func (k *kmsSteps) noFederationKey(ctx context.Context) error {
	return k.start(ctx, "ecdsa-p256", "")
}

// start starts a server on Transit keys of typ and federation, keeping why
// it failed.
func (k *kmsSteps) start(ctx context.Context, typ, federation string) error {
	if err := k.onTransit(ctx, typ, federation); err != nil {
		return err
	}
	_, k.err = k.w.StartServer(ctx, nil)
	return nil
}

// refusedNaming checks that the server exited 1 before its "listening"
// line, saying each of wants.
func (k *kmsSteps) refusedNaming(wants ...string) error {
	exit, ok := errors.AsType[*exec.ExitError](k.err)
	if !errors.Is(k.err, harness.ErrExited) || !ok || exit.ExitCode() != 1 {
		return fmt.Errorf(
			"%w: infraweave did not exit 1 before listening: %w",
			harness.ErrStep,
			k.err,
		)
	}
	for _, want := range wants {
		if !strings.Contains(k.err.Error(), want) {
			return fmt.Errorf("%w: it exited without saying %q: %w", harness.ErrStep, want, k.err)
		}
	}
	return nil
}
