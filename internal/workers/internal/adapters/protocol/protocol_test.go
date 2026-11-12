package protocol_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	gen "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/workers/internal/adapters/protocol"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/credential"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

const base = "/worker/v1"

var errBoom = errors.New("boom")

// store holds one pool and its workers.
type store struct {
	worker.Store

	ca      []byte
	pool    pool.Pool
	workers map[kernel.ULID]worker.Worker
}

func (s *store) PoolByID(_ context.Context, id kernel.ULID) (pool.Pool, error) {
	if id != s.pool.ID {
		return pool.Pool{}, pool.ErrNotFound
	}
	return s.pool, nil
}

func (s *store) CA(context.Context) ([]byte, error) { return s.ca, nil }

func (*store) Nonce(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	string,
	time.Time,
) (bool, error) {
	return true, nil
}

func (s *store) Count(
	context.Context,
	kernel.OrganizationID,
	kernel.AccountID,
) (featuresapi.Usage, error) {
	return featuresapi.Usage{Organization: int64(len(s.workers))}, nil
}

func (s *store) Worker(_ context.Context, id kernel.ULID, _ bool) (worker.Worker, int64, error) {
	w, ok := s.workers[id]
	if !ok {
		return w, 0, worker.ErrNotFound
	}
	return w, s.pool.Serial, nil
}

func (s *store) CreateWorker(_ context.Context, w worker.Worker) error {
	s.workers[w.ID] = w
	return nil
}

func (s *store) Resume(_ context.Context, w worker.Worker) error {
	old := s.workers[w.ID]
	old.Serial, old.Version = w.Serial, w.Version
	s.workers[w.ID] = old
	return nil
}

func (s *store) Seen(
	ctx context.Context,
	id kernel.ULID,
	busy bool,
	_ time.Time,
) (worker.Worker, int64, error) {
	w, serial, err := s.Worker(ctx, id, false)
	w.Busy = busy
	return w, serial, err
}

func (s *store) Remove(_ context.Context, id kernel.ULID) error {
	if _, ok := s.workers[id]; !ok {
		return worker.ErrNotFound
	}
	delete(s.workers, id)
	return nil
}

type fakeTx struct{ pgx.Tx }

func (fakeTx) Commit(context.Context) error   { return nil }
func (fakeTx) Rollback(context.Context) error { return nil }

type db struct{}

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }

// tokens mints a session token named for its worker, valid for an hour,
// and verifies one of those that has not expired; fail fails both.
type tokens struct {
	fail    error
	expires map[string]time.Time
}

func (t *tokens) WorkerToken(_ context.Context, w kernel.ULID) (accessapi.Token, error) {
	raw := "session." + w.String()
	t.expires[raw] = time.Now().Add(time.Hour)
	return accessapi.Token{Raw: raw, Expires: t.expires[raw]}, t.fail
}

func (t *tokens) VerifyWorker(_ context.Context, raw string) (accessapi.Bearer, error) {
	if t.fail != nil {
		return accessapi.Bearer{}, t.fail
	}
	id, ok := strings.CutPrefix(raw, "session.")
	u, err := kernel.ParseULID(id)
	if !ok || err != nil {
		return accessapi.Bearer{}, accessapi.ErrInvalidToken
	}
	return accessapi.Bearer{ID: u, Expires: t.expires[raw]}, nil
}

// limits allows max workers in all.
type limits struct{ max int64 }

func (l limits) CheckLimit(
	ctx context.Context,
	_ kernel.OrganizationID,
	_ kernel.AccountID,
	limit string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	u, err := count(ctx)
	if err != nil {
		return err
	}
	if u.Organization >= l.max {
		return &featuresapi.LimitError{
			Limit: limit,
			Scope: featuresapi.ScopeOrganization,
			Value: l.max,
		}
	}
	return nil
}

type fixture struct {
	h      http.Handler
	store  *store
	tokens *tokens
	key    *ecdsa.PrivateKey
	cert   *x509.Certificate
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ca, err := credential.NewCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	p := pool.Pool{
		Account: kernel.NewAccountID(), Organization: kernel.NewOrganizationID(),
		ID: kernel.NewULID(), Serial: 1,
	}
	cert, err := ca.Sign(csr, p.ID, p.Serial, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		store:  &store{ca: ca.Certificate.Raw, pool: p, workers: map[kernel.ULID]worker.Worker{}},
		tokens: &tokens{expires: map[string]time.Time{}}, key: key, cert: cert,
	}
	svc := worker.New(worker.Deps{
		Store: f.store, DB: db{}, Tokens: f.tokens, Limits: limits{max: 1}, Version: "devel",
	})
	strict := gen.NewStrictHandlerWithOptions(
		protocol.New(svc, slog.New(slog.DiscardHandler)),
		nil,
		gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	h := gen.HandlerWithOptions(strict, gen.ChiServerOptions{
		BaseURL: base, ErrorHandlerFunc: problem.RequestError,
	})
	f.h = f.authenticate(h)
	return f
}

// authenticate stands in for the worker API's authenticator: it puts the
// Bearer of a session token the fake verifies on the request's context,
// and passes any other request on bare, as the route table never does.
func (f *fixture) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if b, err := f.tokens.VerifyWorker(r.Context(), raw); ok && err == nil {
			r = r.WithContext(accessapi.WithBearer(r.Context(), b))
		}
		next.ServeHTTP(w, r)
	})
}

// registration is a JWS of a registration of the fixture's pool, naming
// the worker named when it is set.
func (f *fixture) registration(t *testing.T, named string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: f.key},
		(&jose.SignerOptions{}).WithHeader("x5c",
			[]string{base64.StdEncoding.EncodeToString(f.cert.Raw)}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"pool": f.store.pool.ID.String(), "iat": time.Now().Unix(),
		"jti": kernel.NewULID().String(), "version": "1.0.0", "os": "linux", "arch": "amd64",
		"worker": named,
	})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// call sends body to path with token as its bearer, when set, and
// answers the status and the decoded body.
func (f *fixture) call(t *testing.T, path, token string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, base+path,
		strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	out := map[string]any{}
	if raw, _ := io.ReadAll(w.Body); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s answered %q", path, raw)
		}
	}
	return w.Code, out
}

// TestUnitProtocol checks that a launcher registers, polls and
// deregisters, that a registration beyond private-workers answers 403
// limit-reached and a resumed one does not, and that a refused
// registration answers 401.
func TestUnitProtocol(t *testing.T) {
	f := newFixture(t)
	code, reg := f.call(t, "/register", "", map[string]any{"registration": f.registration(t, "")})
	if code != http.StatusOK || reg["sessionToken"] == nil || reg["sessionExpiresAt"] == nil {
		t.Fatalf("register = %d %v", code, reg)
	}
	id, _ := reg["worker"].(string)
	token, _ := reg["sessionToken"].(string)

	code, body := f.call(t, "/register", "", map[string]any{"registration": f.registration(t, "")})
	if code != http.StatusForbidden || body["type"] != "urn:infraweave:problem:limit-reached" ||
		body["limit"] != featuresapi.PrivateWorkers {
		t.Errorf("beyond the limit = %d %v", code, body)
	}
	if code, body := f.call(t, "/register", "",
		map[string]any{"registration": f.registration(t, id)}); code != http.StatusOK ||
		body["worker"] != id {
		t.Errorf("resume = %d %v", code, body)
	}
	if code, body := f.call(t, "/register", "",
		map[string]any{"registration": "garbage"}); code != http.StatusUnauthorized {
		t.Errorf("garbage = %d %v", code, body)
	}

	u, err := kernel.ParseULID(id)
	if err != nil {
		t.Fatal(err)
	}
	w := f.store.workers[u]
	w.Cycle, w.Drained = true, true
	f.store.workers[u] = w
	code, body = f.call(t, "/poll", token, map[string]any{"busy": true, "drained": true})
	if signals, _ := body["signals"].([]any); code != http.StatusOK || len(signals) != 1 ||
		signals[0] != "cycle" || body["drained"] != true || body["sessionToken"] != nil {
		t.Errorf("cycled poll = %d %v", code, body)
	}
	f.tokens.expires[token] = time.Now().Add(time.Minute)
	code, body = f.call(t, "/poll", token, map[string]any{"busy": false, "wait": 5})
	if code != http.StatusOK || body["sessionToken"] == nil || body["sessionExpiresAt"] == nil {
		t.Errorf("renewing poll = %d %v", code, body)
	}

	if code, _ := f.call(t, "/deregister", token, map[string]any{}); code != http.StatusNoContent {
		t.Errorf("deregister = %d", code)
	}
	if code, body := f.call(
		t,
		"/deregister",
		token,
		map[string]any{},
	); code != http.StatusUnauthorized {
		t.Errorf("deregister twice = %d %v", code, body)
	}
}

// TestUnitProtocolHold checks that a poll with nothing to answer is held
// for its wait and answers no signal.
func TestUnitProtocolHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		w := worker.Worker{ID: kernel.NewULID(), Pool: f.store.pool.ID, Serial: 1}
		f.store.workers[w.ID] = w
		tok, err := f.tokens.WorkerToken(t.Context(), w.ID)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		code, body := f.call(t, "/poll", tok.Raw, map[string]any{"busy": false, "wait": 5})
		if code != http.StatusOK || len(body) != 0 || time.Since(start) != 5*time.Second {
			t.Errorf("held poll = %d %v after %v", code, body, time.Since(start))
		}
	})
}

// TestUnitProtocolRefused checks that poll and deregister refuse a
// request with no worker session token, or one naming an unknown worker,
// and that a registration whose token cannot be minted fails.
func TestUnitProtocolRefused(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/poll", "/deregister"} {
		for name, token := range map[string]string{
			"no token":       "",
			"an API token":   "api.token",
			"unknown worker": "session." + kernel.NewULID().String(),
		} {
			code, body := f.call(t, path, token, map[string]any{"busy": false})
			if code != http.StatusUnauthorized ||
				body["type"] != "urn:infraweave:problem:unauthenticated" {
				t.Errorf("%s with %s = %d %v", path, name, code, body)
			}
		}
	}
	f.tokens.fail = errBoom
	if code, _ := f.call(t, "/register", "", map[string]any{
		"registration": f.registration(t, ""),
	}); code != http.StatusInternalServerError {
		t.Errorf("mint down = %d", code)
	}
}
