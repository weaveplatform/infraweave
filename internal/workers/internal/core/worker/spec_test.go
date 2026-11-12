package worker_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"math/big"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// specFixture is a fixture whose pool carries its certificate, with a
// scheduler that hands over run at state version 4 with doc as its
// secrets.
func specFixture(t *testing.T) (*fixture, *scheduler, kernel.ULID, []byte) {
	t.Helper()
	f := newFixture(t, worker.Deps{})
	f.pool.Certificate = f.cert.Raw
	f.store.pools[f.pool.ID] = f.pool
	run := kernel.NewULID()
	doc := []byte(`{"apiToken":"in-run"}`)
	sch := &scheduler{handover: workersapi.Handover{
		Account: f.pool.Account, Pool: f.pool.ID,
		Spec:    workersapi.Spec{Run: workersapi.SpecRun{ID: run, StateVersion: 4}},
		Secrets: crypt.NewValue(doc),
	}}
	f.svc.BindScheduler(sch)
	return f, sch, run, doc
}

// TestUnitWorkerRunSpec checks that a run token's bearer gets its run's
// specification, whose secrets only the pool's private key opens, and
// only for that run at that state version; that the account's default
// runner image fills in only for a run and stack that name none; and
// that another run's token is refused before the run is handed over.
func TestUnitWorkerRunSpec(t *testing.T) {
	f, sch, run, doc := specFixture(t)
	got, err := f.svc.Spec(t.Context(), accessapi.Bearer{ID: run}, run)
	if err != nil || got.Run.ID != run || got.RunnerImage != nil {
		t.Fatalf("Spec = %+v, %v", got, err)
	}
	priv, err := f.key.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := seal.OpenEnvelope(priv, run.String(), 4, got.Sealed); err != nil ||
		string(plain) != string(doc) {
		t.Errorf("opened %q, %v", plain, err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := other.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func() ([]byte, error){
		"another key": func() ([]byte, error) {
			return seal.OpenEnvelope(otherPriv, run.String(), 4, got.Sealed)
		},
		"another run": func() ([]byte, error) {
			return seal.OpenEnvelope(priv, kernel.NewULID().String(), 4, got.Sealed)
		},
		"another state version": func() ([]byte, error) {
			return seal.OpenEnvelope(priv, run.String(), 5, got.Sealed)
		},
	} {
		if _, err := open(); err == nil {
			t.Errorf("%s opened the secrets", name)
		}
	}
	account, own := "ghcr.io/acme/runner", "ghcr.io/acme/stack"
	f.store.images = images.Images{Private: &account}
	if got, err := f.svc.Spec(t.Context(), accessapi.Bearer{ID: run}, run); err != nil ||
		got.RunnerImage == nil || *got.RunnerImage != account {
		t.Errorf("with the account's image = %v, %v", got.RunnerImage, err)
	}
	sch.handover.Spec.RunnerImage = &own
	if got, err := f.svc.Spec(t.Context(), accessapi.Bearer{ID: run}, run); err != nil ||
		got.RunnerImage == nil || *got.RunnerImage != own {
		t.Errorf("with the stack's image = %v, %v", got.RunnerImage, err)
	}
	handed := len(sch.handed)
	if _, err := f.svc.Spec(t.Context(), accessapi.Bearer{ID: kernel.NewULID()},
		run); !errors.Is(err, worker.ErrUnauthenticated) || len(sch.handed) != handed {
		t.Errorf("another run's token = %v, handed %d times", err, len(sch.handed)-handed)
	}
}

// selfSigned is the DER of a certificate for key's public key.
func selfSigned(t *testing.T, key crypto.Signer) []byte {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// TestUnitWorkerRunSpecFails checks that a run is not on a worker while
// no scheduler is bound or its pool is gone, that the scheduler's and the
// store's failures are returned, and that a pool certificate holding no
// P-256 key fails rather than seals to another key.
func TestUnitWorkerRunSpecFails(t *testing.T) {
	f, sch, run, _ := specFixture(t)
	bearer := accessapi.Bearer{ID: run}
	if _, err := newFixture(t, worker.Deps{}).svc.Spec(t.Context(), bearer,
		run); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("no scheduler = %v", err)
	}
	sch.handErr = workersapi.ErrNotOnWorker
	if _, err := f.svc.Spec(t.Context(), bearer, run); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("a handover refused = %v", err)
	}
	sch.handErr = nil
	for _, method := range []string{"Get", "RunnerImages"} {
		f.store.fail = method
		if _, err := f.svc.Spec(t.Context(), bearer, run); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", method, err)
		}
	}
	f.store.fail = ""
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p224, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		der  []byte
		want error
	}{
		"not a certificate": {[]byte{0x30}, nil},
		"an RSA key":        {selfSigned(t, rsaKey), seal.ErrPoolKey},
		"a P-384 key":       {selfSigned(t, p384), seal.ErrPoolKey},
		"a P-224 key":       {selfSigned(t, p224), nil},
	} {
		p := f.pool
		p.Certificate = tc.der
		f.store.pools[p.ID] = p
		_, err := f.svc.Spec(t.Context(), bearer, run)
		if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s = %v", name, err)
		}
	}
	delete(f.store.pools, f.pool.ID)
	if _, err := f.svc.Spec(t.Context(), bearer, run); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("pool gone = %v", err)
	}
}
