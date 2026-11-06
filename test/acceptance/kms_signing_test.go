package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// kmsSteps drive decision 0006's kms signing backend on the @vault
// container's Transit engine.
type kmsSteps struct {
	s *servedSteps
	// ref is the scenario's kms_keys reference, <mount>/<key>/<version>.
	ref string
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
		`^an infraweave server signing with an aes256-gcm96 Transit key is started$`,
		k.wrongType,
	)
	sc.Step(`^it exits before it listens, naming the Transit key$`, k.refusedNaming)
}

// onTransit makes a Transit key of typ for the scenario, and sets every
// server it starts to sign with its version 1, reaching Vault through
// secrets.kek.vault with a token file while the KEK stays local.
func (k *kmsSteps) onTransit(ctx context.Context, typ string) error {
	w := k.s.w
	v, err := w.s.deps.Vault(ctx)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("signing-%d", w.s.deps.keys.Add(1))
	if err := v.post(ctx, "transit/keys/"+name, `{"type":"`+typ+`"}`); err != nil {
		return fmt.Errorf("@vault: %w", err)
	}
	token := filepath.Join(w.dir, "vault-token")
	if err := os.WriteFile(token, []byte(v.Token), 0o600); err != nil {
		return fmt.Errorf("vault token: %w", err)
	}
	k.ref = "transit/" + name + "/1"
	w.kek = map[string]any{"provider": "local", "vault": map[string]any{
		"address": v.Addr,
		"auth":    map[string]any{"method": "token", "token_file": token},
	}}
	w.auth = map[string]any{"signing": map[string]any{
		"backend":  "kms",
		"kms_keys": []map[string]any{{"provider": "vault", "ref": k.ref}},
	}}
	return nil
}

func (k *kmsSteps) twoOnTransit(ctx context.Context) error {
	if err := k.onTransit(ctx, "ecdsa-p256"); err != nil {
		return err
	}
	return k.s.twoReplicas(ctx)
}

// row checks that the one signing key is the Transit key's: kms_key_ref
// names it and private_key_sealed is empty.
func (k *kmsSteps) row(ctx context.Context) error {
	db, err := k.s.w.querier(ctx)
	if err != nil {
		return err
	}
	got, err := db.Query(ctx, `
SELECT count(*), coalesce(max(kms_key_ref), ''), coalesce(bool_or(private_key_sealed IS NOT NULL), false)
FROM signing_keys`)
	if err != nil {
		return fmt.Errorf("signing_keys: %w", err)
	}
	if want := "1|vault:" + k.ref + "|f"; strings.TrimSpace(got) != want {
		return fmt.Errorf("%w: signing_keys holds %q (count, kms_key_ref, sealed), want %q",
			errStep, strings.TrimSpace(got), want)
	}
	return nil
}

func (k *kmsSteps) wrongType(ctx context.Context) error {
	if err := k.onTransit(ctx, "aes256-gcm96"); err != nil {
		return err
	}
	_, k.err = k.s.w.startServer(ctx, nil)
	return nil
}

// refusedNaming checks that the server exited 1 before its "listening"
// line, saying the key is not a P-256 key and naming its reference.
func (k *kmsSteps) refusedNaming() error {
	exit, ok := errors.AsType[*exec.ExitError](k.err)
	if !errors.Is(k.err, ErrExited) || !ok || exit.ExitCode() != 1 {
		return fmt.Errorf("%w: infraweave did not exit 1 before listening: %w", errStep, k.err)
	}
	for _, want := range []string{"vault:" + k.ref, "not ECDSA P-256"} {
		if !strings.Contains(k.err.Error(), want) {
			return fmt.Errorf("%w: it exited without saying %q: %w", errStep, want, k.err)
		}
	}
	return nil
}
