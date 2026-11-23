package pg_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// These fault ports control issuance/claim boundaries; real API acceptance
// separately exercises actual signed tokens through two application replicas.
type serialTokens struct {
	fail   bool
	serial atomic.Int64
}

func (t *serialTokens) WorkerToken(_ context.Context, id kernel.ULID, serial int64) (accessapi.Token, error) {
	t.serial.Store(serial)
	if t.fail {
		return accessapi.Token{}, errDown
	}
	return accessapi.Token{Raw: fmt.Sprintf("worker/%s/%d", id, serial), Expires: time.Now().Add(time.Hour), WorkerSerial: serial}, nil
}

func (*serialTokens) RunToken(context.Context, kernel.ULID) (accessapi.Token, error) {
	return accessapi.Token{}, errDown
}

func (*serialTokens) RunTokenKeys(context.Context) (accessapi.RunTokenKeys, error) {
	return accessapi.RunTokenKeys{}, errDown
}

type claimCounter struct {
	workersapi.Scheduler
	calls atomic.Int32
}

func (s *claimCounter) Claim(context.Context, workersapi.Claim) (workersapi.Assignment, bool, error) {
	s.calls.Add(1)
	return workersapi.Assignment{}, false, nil
}

type lockBoundaryStore struct {
	*pg.Store
	locks   atomic.Int32
	reached chan struct{}
	release chan struct{}
}

func (s *lockBoundaryStore) Worker(ctx context.Context, id kernel.ULID, lock bool) (worker.Worker, int64, error) {
	if lock && s.locks.Add(1) == 2 {
		close(s.reached)
		select {
		case <-s.release:
		case <-ctx.Done():
			return worker.Worker{}, 0, ctx.Err()
		}
	}
	return s.Store.Worker(ctx, id, lock)
}

func sameLiveness(a, b worker.Worker) bool {
	return a.Serial == b.Serial && a.Busy == b.Busy && a.Offline == b.Offline && a.LastSeenAt.Equal(b.LastSeenAt) &&
		(a.AvailableAt == nil && b.AvailableAt == nil || a.AvailableAt != nil && b.AvailableAt != nil && a.AvailableAt.Equal(*b.AvailableAt))
}

func TestAccWorkerResetFencesClaimAfterUnlockedRead(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	w := f.newWorker(t, p, time.Now().Add(-time.Minute).Truncate(time.Microsecond))
	boundary := &lockBoundaryStore{Store: f.store, reached: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(boundary.release) }) }
	t.Cleanup(release)
	svc := worker.New(worker.Deps{Store: boundary, DB: f.db, Tokens: &serialTokens{}})
	sch := &claimCounter{}
	svc.BindScheduler(sch)
	result := make(chan error, 1)
	go func() {
		_, err := svc.Poll(t.Context(), accessapi.Bearer{ID: w.ID, WorkerSerial: w.Serial, Expires: time.Now().Add(time.Hour)}, worker.PollInput{})
		result <- err
	}()
	<-boundary.reached
	reset, err := f.svc.Reset(t.Context(), f.admin, p.ID.String(), request(t), false)
	if err != nil {
		t.Fatal(err)
	}
	resumed := w
	resumed.Serial = reset.Serial
	resumed.LastSeenAt = time.Now()
	if err := f.store.Resume(t.Context(), resumed); err != nil {
		t.Fatal(err)
	}
	before, _, err := f.store.Worker(t.Context(), w.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-result; !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatalf("claim crossed reset: %v", err)
	}
	after, _, err := f.store.Worker(t.Context(), w.ID, false)
	if err != nil || sch.calls.Load() != 0 || !sameLiveness(before, after) {
		t.Fatal("revoked claim changed replacement worker")
	}
	if err := svc.Deregister(t.Context(), accessapi.Bearer{ID: w.ID, WorkerSerial: w.Serial}); !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatal("old token deregistered replacement")
	}
}

func TestAccWorkerSeenWaitsForResetAndDoesNotMutateStaleGeneration(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	w := f.newWorker(t, p, time.Now().Add(-time.Minute).Truncate(time.Microsecond))
	locked := make(chan struct{})
	release := make(chan struct{})
	resetDone := make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	csr := request(t)
	go func() {
		resetDone <- tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
			if _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), csr, false); err != nil {
				return err
			}
			close(locked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	<-locked
	seen := make(chan error, 1)
	go func() { _, _, err := f.store.Seen(t.Context(), w.ID, w.Serial, true, time.Now()); seen <- err }()
	waitForLock(t, f.db)
	unlock()
	if err := <-resetDone; err != nil {
		t.Fatal(err)
	}
	if err := <-seen; !errors.Is(err, worker.ErrNotFound) {
		t.Fatalf("stale Seen crossed reset: %v", err)
	}
	got, _, err := f.store.Worker(t.Context(), w.ID, false)
	if err != nil || !sameLiveness(w, got) {
		t.Fatal("stale generation changed real liveness columns")
	}
}

func TestAccWorkerRenewalFailureRollsBackSeenAndRetainsGeneration(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	w := f.newWorker(t, p, time.Now().Add(-time.Minute).Truncate(time.Microsecond))
	tokens := &serialTokens{fail: true}
	svc := worker.New(worker.Deps{Store: f.store, DB: f.db, Tokens: tokens})
	b := accessapi.Bearer{ID: w.ID, WorkerSerial: w.Serial, Expires: time.Now().Add(time.Minute)}
	if _, err := svc.Poll(t.Context(), b, worker.PollInput{Busy: true}); !errors.Is(err, errDown) {
		t.Fatal(err)
	}
	got, _, err := f.store.Worker(t.Context(), w.ID, false)
	if err != nil || !sameLiveness(w, got) {
		t.Fatal("failed renewal committed liveness")
	}
	tokens.fail = false
	for range 2 {
		out, err := svc.Poll(t.Context(), b, worker.PollInput{Busy: true})
		if err != nil || out.Token == nil || out.Token.WorkerSerial != w.Serial || tokens.serial.Load() != w.Serial {
			t.Fatalf("renewal/response-loss retry: %+v %v", out, err)
		}
	}
}

func TestAccWorkerAdmissionLocksPoolBeforeWorkerAndSerializesReset(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	w := f.newWorker(t, p, time.Now().Truncate(time.Microsecond))
	blocker, err := f.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT id FROM workers WHERE id=$1 FOR UPDATE`, w.ID); err != nil {
		t.Fatal(err)
	}
	admitted := make(chan struct{})
	release := make(chan struct{})
	admissionDone := make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	go func() {
		admissionDone <- tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
			if _, _, err := f.store.Worker(ctx, w.ID, true); err != nil {
				return err
			}
			close(admitted)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	waitForLock(t, f.db)
	resetDone := make(chan error, 1)
	csr := request(t)
	go func() { _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), csr, false); resetDone <- err }()
	for {
		var waiting int
		if err := f.db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case err := <-resetDone:
			t.Fatalf("reset passed the admission pool fence: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-admitted:
	case err := <-admissionDone:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-resetDone:
		t.Fatalf("reset passed an uncommitted admission: %v", err)
	default:
	}
	unlock()
	if err := <-admissionDone; err != nil {
		t.Fatal(err)
	}
	if err := <-resetDone; err != nil {
		t.Fatal(err)
	}
}

func TestAccWorkerRegistrationTokenFailureRollsBackNonceAndResume(t *testing.T) {
	f := newFixture(t)
	key, csr := registrationKey(t)
	p, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: "Builders"}, csr)
	if err != nil {
		t.Fatal(err)
	}
	tokens := &serialTokens{fail: true}
	svc := worker.New(worker.Deps{Store: f.store, DB: f.db, Tokens: tokens, Version: "devel"})
	proof, jti := registrationProof(t, p, key, "")
	if _, err := svc.Register(t.Context(), proof); !errors.Is(err, errDown) {
		t.Fatal(err)
	}
	var workers, nonces int
	if err := f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM workers WHERE pool_id=$1),(SELECT count(*) FROM worker_nonces WHERE pool_id=$1 AND jti=$2)`, p.ID, jti).
		Scan(&workers, &nonces); err != nil || workers != 0 ||
		nonces != 0 {
		t.Fatal("failed token issuance consumed registration or quota")
	}
	tokens.fail = false
	reg, err := svc.Register(t.Context(), proof)
	if err != nil || reg.Token.WorkerSerial != p.Serial {
		t.Fatalf("rolled-back proof retry: %v", err)
	}
	if _, err := svc.Register(t.Context(), proof); !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatal("successful registration proof was replayable")
	}
	before, _, err := f.store.Worker(t.Context(), reg.Worker, false)
	if err != nil {
		t.Fatal(err)
	}
	keyB, csrB := registrationKey(t)
	reset, err := f.svc.Reset(t.Context(), f.admin, p.ID.String(), csrB, false)
	if err != nil {
		t.Fatal(err)
	}
	resumption, resumeJTI := registrationProof(t, reset, keyB, reg.Worker.String())
	tokens.fail = true
	if _, err := svc.Register(t.Context(), resumption); !errors.Is(err, errDown) {
		t.Fatal(err)
	}
	after, _, err := f.store.Worker(t.Context(), reg.Worker, false)
	if err != nil || !sameLiveness(before, after) {
		t.Fatal("failed replacement registration rewrote worker generation/liveness")
	}
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM worker_nonces WHERE pool_id=$1 AND jti=$2`, p.ID, resumeJTI).Scan(&nonces); err != nil || nonces != 0 {
		t.Fatal("failed resume consumed its nonce")
	}
	tokens.fail = false
	resumed, err := svc.Register(t.Context(), resumption)
	if err != nil || resumed.Worker != reg.Worker || resumed.Token.WorkerSerial != reset.Serial {
		t.Fatalf("resumption changed private identity or generation: %v", err)
	}
}

type pausedSerialTokens struct {
	serialTokens
	entered chan struct{}
	release chan struct{}
}

func (t *pausedSerialTokens) WorkerToken(ctx context.Context, id kernel.ULID, serial int64) (accessapi.Token, error) {
	close(t.entered)
	select {
	case <-t.release:
	case <-ctx.Done():
		return accessapi.Token{}, ctx.Err()
	}
	return t.serialTokens.WorkerToken(ctx, id, serial)
}

func TestAccWorkerRegistrationAndResetShareOneGenerationFence(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	key, csr := registrationKey(t)
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, csr)
	if err != nil {
		t.Fatal(err)
	}
	tokens := &pausedSerialTokens{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(tokens.release) }) }
	t.Cleanup(release)
	svc := worker.New(worker.Deps{Store: f.store, DB: f.db, Tokens: tokens, Version: "devel"})
	proof, _ := registrationProof(t, p, key, "")
	type registrationResult struct {
		reg worker.Registration
		err error
	}
	registered := make(chan registrationResult, 1)
	go func() { reg, err := svc.Register(ctx, proof); registered <- registrationResult{reg, err} }()
	select {
	case <-tokens.entered:
	case out := <-registered:
		t.Fatal(out.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	reset := make(chan error, 1)
	csrB := request(t)
	go func() { _, err := f.svc.Reset(ctx, f.admin, p.ID.String(), csrB, false); reset <- err }()
	waitForLock(t, f.db)
	select {
	case err := <-reset:
		t.Fatalf("reset crossed registration/token fence: %v", err)
	default:
	}
	release()
	out := <-registered
	if out.err != nil || out.reg.Token.WorkerSerial != p.Serial {
		t.Fatal(out.err)
	}
	if err := <-reset; err != nil {
		t.Fatal(err)
	}
	_, err = svc.Poll(ctx, accessapi.Bearer{ID: out.reg.Worker, WorkerSerial: p.Serial, Expires: time.Now().Add(time.Hour)}, worker.PollInput{})
	if !errors.Is(err, worker.ErrUnauthenticated) {
		t.Fatal("completed reset did not revoke in-flight issued generation")
	}
}

func registrationKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "pool"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, base64.StdEncoding.EncodeToString(csr)
}

func registrationProof(t *testing.T, p pool.Pool, key *ecdsa.PrivateKey, named string) (string, string) {
	t.Helper()
	jti := kernel.NewULID().String()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithHeader(jose.HeaderKey("x5c"), []string{base64.StdEncoding.EncodeToString(p.Certificate)}))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(
		map[string]any{
			"pool":         p.ID.String(),
			"worker":       named,
			"iat":          time.Now().Unix(),
			"jti":          jti,
			"version":      "1.0.0",
			"os":           "linux",
			"arch":         "amd64",
			"capabilities": []string{"plan-sanitization-v1"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proof.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw, jti
}

// Independent public workers share a generation fence; reset waits for the
// admitted transaction without imposing a global exclusive claim lock.
func TestAccPublicWorkersShareGenerationFence(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	p, err := f.svc.CreatePublic(ctx, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]worker.Worker, 2)
	for i := range slots {
		now := time.Now().UTC().Truncate(time.Microsecond)
		slots[i] = worker.Worker{
			Public: true, ID: kernel.NewULID(), Pool: p.ID, Serial: p.Serial,
			Version: "1.0.0", OS: "linux", Arch: "amd64", LastSeenAt: now, AvailableAt: &now, CreatedAt: now,
		}
		if err := f.store.CreateWorker(ctx, slots[i]); err != nil {
			t.Fatal(err)
		}
	}
	admitted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	first := make(chan error, 1)
	go func() {
		first <- tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
			if _, _, err := f.store.Worker(ctx, slots[0].ID, true); err != nil {
				return err
			}
			close(admitted)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-admitted:
	case err := <-first:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The second worker must be admitted while the first still holds its fence.
	secondCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	err = tx.Do(secondCtx, f.db, tx.Options{}, func(ctx context.Context) error {
		_, _, err := f.store.Worker(ctx, slots[1].ID, true)
		return err
	})
	stop()
	if err != nil {
		t.Fatalf("independent public worker serialized on pool fence: %v", err)
	}
	reset := make(chan error, 1)
	csr := request(t)
	go func() { _, err := f.svc.ResetPublic(ctx, kernel.ExpectVersions(1), csr, false); reset <- err }()
	waitForLock(t, f.db)
	select {
	case err := <-reset:
		t.Fatalf("reset passed admitted public worker: %v", err)
	default:
	}
	unlock()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-reset; err != nil {
		t.Fatal(err)
	}
	before := slots[0]
	if _, _, err := f.store.Seen(ctx, before.ID, before.Serial, true, time.Now()); !errors.Is(err, worker.ErrNotFound) {
		t.Fatalf("stale public generation changed liveness: %v", err)
	}
	after, _, err := f.store.Worker(ctx, before.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !sameLiveness(before, after) {
		t.Fatalf("stale public poll changed liveness: before=%+v after=%+v", before, after)
	}
}
