package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// credentialAdmissionBarrier pauses only after production Exchange has read
// and verified the key. All credential reads/writes still use PostgreSQL.
type credentialAdmissionBarrier struct {
	port.APIKeys
	verified chan struct{}
	proceed  <-chan struct{}
}

func (b credentialAdmissionBarrier) TouchAPIKey(ctx context.Context, id kernel.ULID) error {
	if err := b.APIKeys.TouchAPIKey(ctx, id); err != nil {
		return err
	}
	close(b.verified)
	select {
	case <-b.proceed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAccCredentialMutationRefusesAnAlreadyVerifiedExchange(t *testing.T) {
	x := seed(t, pool(t), "credential-admission-fence")
	secret := apikey.NewSecret()
	hash, valid := apikey.Hash(secret)
	if !valid {
		t.Fatal("generated fixture secret is invalid")
	}
	if _, err := x.db.Exec(t.Context(), `UPDATE api_keys SET secret_hash=$2 WHERE id=$1`, x.key, hash); err != nil {
		t.Fatal(err)
	}
	prior := x.keySession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(t.Context(), prior); err != nil {
		t.Fatal(err)
	}
	verified, proceed := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(proceed) })
	t.Cleanup(release)
	tokens := token.New(pg.NewSigningKeys(x.db, keyring(t, x.db), nil), "https://iw.example.com",
		token.Policy{TokenLife: time.Hour}, time.Now)
	service := authn.New(authn.Deps{
		Keys:     credentialAdmissionBarrier{APIKeys: x.s, verified: verified, proceed: proceed},
		Sessions: x.s, Tokens: tokens, Audience: audience, TTL: time.Hour, Now: time.Now,
		Entry: x.s,
	})
	type result struct {
		grant authn.Grant
		err   error
	}
	done := make(chan result, 1)
	go func() {
		grant, err := service.Exchange(t.Context(), x.key.String(), secret, "192.0.2.9")
		done <- result{grant: grant, err: err}
	}()
	select {
	case <-verified:
	case got := <-done:
		t.Fatalf("exchange ended before its controlled credential boundary: %v", got.err)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	_, revoked, err := x.s.DisableAPIKey(t.Context(), x.acct, x.key, "ADMIN")
	if err != nil || len(revoked) != 1 || revoked[0] != prior.ID {
		t.Fatalf("actual disable did not revoke its visible session: %v, %v", revoked, err)
	}
	release()
	got := <-done
	if !errors.Is(got.err, authn.ErrUnauthenticated) {
		if got.err == nil {
			if _, _, err := service.Authenticate(t.Context(), got.grant.Token, authn.ReachAccount); err == nil {
				t.Fatal("disabled key's in-flight exchange issued a signed token that still authenticates")
			}
		}
		t.Fatalf("credential changed after verification but exchange was not refused: %v", got.err)
	}
}
