package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/webhooktest"
)

var errDown = errors.New("down")

type (
	db          struct{ begin, commit error }
	transaction struct {
		pgx.Tx
		commit error
	}
)

func (d *db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return transaction{commit: d.commit}, d.begin
}
func (t transaction) Commit(context.Context) error { return t.commit }
func (transaction) Rollback(context.Context) error { return nil }

type store struct {
	in                                Integration
	d                                 Delivery
	calls                             *[]string
	lookup, insert, read, state, drop error
	duplicate                         bool
	keys                              []string
	drops                             []int
	refs                              []Ref
	pending                           error
}

func (s *store) Integration(context.Context, kernel.ULID, time.Time) (Integration, error) {
	*s.calls = append(*s.calls, "lookup")
	return s.in, s.lookup
}

func (s *store) Insert(ctx context.Context, d Delivery) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	*s.calls = append(*s.calls, "insert")
	s.d = d
	return !s.duplicate, s.insert
}

func (s *store) Read(context.Context, kernel.AccountID, kernel.ULID) (Delivery, error) {
	return s.d, s.read
}

func (s *store) State(_ context.Context, _ kernel.AccountID, _ kernel.ULID, replays int64, state, last string) error {
	if s.d.State == Received && s.d.Replays == replays {
		s.d.State = state
		s.d.LastError = last
	}
	return s.state
}

func (s *store) Drop(context.Context, *kernel.AccountID, *time.Time, int) ([]string, int, error) {
	n := 0
	if len(s.drops) > 0 {
		n = s.drops[0]
		s.drops = s.drops[1:]
	}
	return s.keys, n, s.drop
}

func (s *store) Pending(_ context.Context, after kernel.ULID, limit int) ([]Ref, error) {
	var out []Ref
	for _, ref := range s.refs {
		if ref.ID.String() > after.String() {
			out = append(out, ref)
		}
		if len(out) == limit {
			break
		}
	}
	return out, s.pending
}

type jobs struct {
	payload                  []byte
	enqueued, complete, held int
	enqueue, finish, hold    error
	dead                     map[string]bool
	deadError                error
	checked                  int
	statuses                 map[string]queue.KeyStatus
	statusError              error
	heldPrefix               string
}

func (j *jobs) EnqueueTx(ctx context.Context, k queue.Kind, p any, _ ...queue.Option) (int64, error) {
	if !tx.In(ctx) || k != Process && k != Reconcile {
		return 0, queue.ErrNoTransaction
	}
	j.enqueued++
	j.payload, _ = json.Marshal(p)
	return 1, j.enqueue
}

func (j *jobs) DeadByKeyTx(ctx context.Context, _ queue.Kind, key string, _ kernel.ULID) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	j.checked++
	return j.dead[key], j.deadError
}

func (j *jobs) CompleteTx(ctx context.Context, _ queue.Job) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	j.complete++
	return j.finish
}

func (j *jobs) HoldTx(ctx context.Context, _ queue.Job) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	j.held++
	return j.hold
}

type objects struct {
	bodies                    map[string][]byte
	put, commit, open, delete error
	read                      error
	live                      error
	locked                    int
	committed, deleted        []string
}

func (o *objects) Put(_ context.Context, _ objectstore.Name, key string, r io.Reader, _ int64) (objectstore.Object, error) {
	o.bodies[key], _ = io.ReadAll(r)
	return objectstore.Object{}, o.put
}

func (o *objects) Commit(_ context.Context, _ objectstore.Name, key string) error {
	o.committed = append(o.committed, key)
	return o.commit
}

func (o *objects) Open(_ context.Context, _ objectstore.Name, key string) (io.ReadCloser, error) {
	if o.read != nil {
		return io.NopCloser(errorReader{o.read}), o.open
	}
	return io.NopCloser(bytes.NewReader(o.bodies[key])), o.open
}

func (o *objects) Delete(_ context.Context, _ objectstore.Name, key string) error {
	o.deleted = append(o.deleted, key)
	return o.delete
}

type fixture struct {
	svc      *Service
	store    *store
	jobs     *jobs
	objects  *objects
	db       *db
	calls    []string
	log      bytes.Buffer
	rejected []string
	parsed   int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{jobs: &jobs{}, objects: &objects{bodies: map[string][]byte{}}, db: &db{}}
	f.store = &store{in: Integration{Account: kernel.NewAccountID(), ID: kernel.NewULID(), Provider: "GITHUB_ENTERPRISE", Current: []byte("current-secret")}, calls: &f.calls}
	f.svc = New(Deps{
		DB: f.db, Store: f.store, Jobs: f.jobs, Objects: f.objects,
		Providers: map[string]port.Webhooks{"GITHUB_ENTERPRISE": webhooktest.Provider{Calls: &f.calls, Parsed: &f.parsed}},
		Log:       slog.New(slog.NewJSONHandler(&f.log, nil)), Rejected: func(_ context.Context, p, r string) { f.rejected = append(f.rejected, p+":"+r) },
	})
	return f
}

func (f *fixture) headers(body []byte) http.Header {
	return http.Header{
		"X-Hub-Signature-256": {"sha256=" + string(webhooktest.Sign(body, f.store.in.Current))}, "X-Github-Event": {"push"},
		"X-Github-Delivery": {"delivery-7"}, "Authorization": {"Bearer current-secret"}, "X-Gitlab-Token": {"current-secret"}, "User-Agent": {"fixture"},
	}
}

func (f *fixture) receive(t *testing.T, b []byte) (int, error) {
	t.Helper()
	return f.svc.Receive(t.Context(), "github_enterprise", f.store.in.ID.String(), f.headers(b), b)
}

func (f *fixture) job() queue.Job {
	return queue.Job{ID: 1, Account: f.store.in.Account.ULID, Payload: f.jobs.payload, Attempt: 1, MaxAttempts: 5}
}

func TestUnitReceiveOrder(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"type":"push"}`)
	code, err := f.receive(t, body)
	if err != nil || code != 202 || f.jobs.enqueued != 1 {
		t.Fatalf("receive=%d,%v jobs=%d", code, err, f.jobs.enqueued)
	}
	if want := []string{"lookup", "verify", "event", "filter", "id", "repository", "insert"}; !reflect.DeepEqual(f.calls, want) {
		t.Errorf("order=%v", f.calls)
	}
	d := f.store.d
	if d.Headers.Get("X-GitHub-Event") != "push" || d.Headers.Get("X-GitHub-Delivery") != "delivery-7" {
		t.Fatal("retained headers lost canonical lookup")
	}
	if d.Headers.Get("Authorization") != "" || d.Headers.Get("X-Gitlab-Token") != "" || !bytes.Equal(d.Signature, webhooktest.Sign(d.Body, f.store.in.Current)) ||
		d.Deadline.Sub(d.ReceivedAt) != 10*time.Minute {
		t.Errorf("unsafe delivery metadata or deadline")
	}
	if strings.Contains(f.log.String(), "current-secret") {
		t.Fatal("secret in log")
	}
	if fmt.Sprintf("%v %#v", f.store.in, f.store.in) != "webhook integration [redacted] webhook integration [redacted]" {
		t.Fatal("unsafe formatting")
	}
	f.svc.d.Log.Info("redacted values", "integration", f.store.in, "delivery", f.store.d)
	if strings.Contains(f.log.String(), "current-secret") || strings.Contains(f.log.String(), "padding") {
		t.Fatal("structured values leaked")
	}
	f.store.duplicate = true
	if code, err := f.receive(t, body); code != 200 || err != nil || f.jobs.enqueued != 1 {
		t.Errorf("duplicate=%d,%v", code, err)
	}
}

func TestUnitReceiveRefusals(t *testing.T) {
	for _, tc := range []string{"bad-id", "missing", "provider", "unsupported", "signature", "ping", "begin", "insert", "enqueue", "commit"} {
		t.Run(tc, func(t *testing.T) {
			f := newFixture(t)
			b := []byte(`{}`)
			h := f.headers(b)
			provider, ref := "github_enterprise", f.store.in.ID.String()
			switch tc {
			case "bad-id":
				ref = "no"
			case "missing":
				f.store.lookup = ErrNotFound
			case "provider":
				provider = "gitlab"
			case "unsupported":
				f.svc.d.Providers = nil
			case "signature":
				h.Del("X-Hub-Signature-256")
			case "ping":
				h.Set("X-GitHub-Event", "ping")
			case "begin":
				f.db.begin = errDown
			case "insert":
				f.store.insert = errDown
			case "enqueue":
				f.jobs.enqueue = errDown
			case "commit":
				f.db.commit = errDown
			}
			code, err := f.svc.Receive(t.Context(), provider, ref, h, b)
			if tc == "ping" {
				if code != 200 || err != nil {
					t.Fatal(code, err)
				}
			} else if err == nil {
				t.Fatal("refusal accepted")
			}
			if tc == "signature" || tc == "unsupported" {
				if !errors.Is(err, ErrVerification) || len(f.rejected) != 1 || strings.Contains(f.log.String(), "current-secret") {
					t.Fatal("unsafe verification refusal")
				}
			}
			if tc == "bad-id" || tc == "missing" || tc == "provider" || tc == "unsupported" || tc == "signature" || tc == "ping" {
				if !f.store.d.ID.IsZero() || f.jobs.enqueued != 0 || len(f.objects.bodies) != 0 {
					t.Fatal("refusal/filter stored work")
				}
			}
		})
	}
}

func TestUnitBodyStorage(t *testing.T) {
	for _, size := range []int{0, InlineBody, InlineBody + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newFixture(t)
			body := bytes.Repeat([]byte("x"), size)
			if _, err := f.receive(t, body); err != nil {
				t.Fatal(err)
			}
			d := f.store.d
			if size <= InlineBody {
				if d.BodyKey != "" || !bytes.Equal(d.Body, body) {
					t.Fatal("inline body changed")
				}
			} else {
				if d.Body != nil || !bytes.Equal(f.objects.bodies[d.BodyKey], body) || !reflect.DeepEqual(f.objects.committed, []string{d.BodyKey}) {
					t.Fatal("blob body changed")
				}
				winner := d.BodyKey
				f.store.duplicate = true
				if code, err := f.receive(t, body); code != 200 || err != nil || len(f.objects.deleted) != 1 || f.objects.deleted[0] == winner {
					t.Fatalf("duplicate touched winner: %d,%v", code, err)
				}
			}
		})
	}
	for _, fault := range []string{"put", "commit", "delete"} {
		f := newFixture(t)
		switch fault {
		case "put":
			f.objects.put = errDown
		case "commit":
			f.objects.commit = errDown
		case "delete":
			f.objects.delete = errDown
			f.store.duplicate = true
		}
		if _, err := f.receive(t, make([]byte, InlineBody+1)); err == nil {
			t.Errorf("%s accepted", fault)
		}
	}
	f := newFixture(t)
	b := []byte(`{}`)
	h := f.headers(b)
	h.Del("X-GitHub-Delivery")
	if _, err := f.svc.Receive(t.Context(), "github_enterprise", f.store.in.ID.String(), h, b); err != nil || len(f.store.d.DeliveryID) != 64 {
		t.Fatal("no body digest fallback", err)
	}
}

func TestUnitSecretWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.in.Previous = []byte("previous-secret")
		f.store.in.PreviousUntil = new(time.Now().Add(time.Hour))
		b := []byte(`{}`)
		h := f.headers(b)
		h.Set("X-Hub-Signature-256", "sha256="+string(webhooktest.Sign(b, f.store.in.Previous)))
		if _, err := f.svc.Receive(t.Context(), "github_enterprise", f.store.in.ID.String(), h, b); err != nil {
			t.Fatal(err)
		}
		<-time.After(time.Hour)
		if _, err := f.svc.Receive(t.Context(), "github_enterprise", f.store.in.ID.String(), h, b); !errors.Is(err, ErrVerification) {
			t.Fatal("expired previous secret accepted", err)
		}
	})
}

func TestUnitProcessDelivery(t *testing.T) {
	for _, fault := range []string{"ok", "large", "missing", "read", "done", "expired", "parse", "last", "hold", "body", "too-large", "unsupported", "state", "complete", "payload"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.receive(t, []byte(`{"type":"push"}`)); err != nil {
				t.Fatal(err)
			}
			j := f.job()
			switch fault {
			case "large":
				f.store.d.BodyKey = "body"
				f.objects.bodies["body"] = f.store.d.Body
				f.store.d.Body = nil
			case "missing":
				f.store.read = ErrNotFound
			case "read":
				f.store.read = errDown
			case "done":
				f.store.d.State = Dispatched
			case "expired":
				f.store.d.Deadline = time.Time{}
			case "parse", "last", "hold":
				f.svc.d.Providers["GITHUB_ENTERPRISE"] = webhooktest.Provider{ParseError: errors.New("current-secret payload")}
				if fault == "last" {
					j.Attempt = j.MaxAttempts
				}
				if fault == "hold" {
					f.jobs.hold = queue.ErrLeaseLost
				}
			case "body":
				f.store.d.BodyKey = "body"
				f.objects.open = errDown
			case "too-large":
				f.store.d.BodyKey = "body"
				f.objects.bodies["body"] = make([]byte, MaxBody+1)
			case "unsupported":
				f.svc.d.Providers = nil
			case "state":
				f.store.state = errDown
			case "complete":
				f.jobs.finish = errDown
			case "payload":
				j.Payload = []byte(`{`)
			}
			err := f.svc.Processed(t.Context(), j)
			wantError := fault == "read" || fault == "parse" || fault == "last" || fault == "hold" || fault == "body" || fault == "too-large" || fault == "unsupported" ||
				fault == "state" ||
				fault == "complete" ||
				fault == "payload"
			if (err != nil) != wantError {
				t.Errorf("process error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "current-secret") || strings.Contains(f.store.d.LastError, "current-secret") {
				t.Fatal("parser error leaked")
			}
			if fault == "last" && f.store.d.State != Failed {
				t.Fatal("last attempt not failed")
			}
			if fault == "parse" && (f.store.d.State != Received || f.store.d.LastError == "" || f.jobs.held != 1) {
				t.Fatal("attempt metadata missing")
			}
			if fault == "expired" && (f.store.d.State != Expired || f.parsed != 0) {
				t.Fatal("expired job parsed payload")
			}
			if (fault == "ok" || fault == "large") && (f.store.d.State != Dispatched || f.jobs.complete != 1) {
				t.Fatal("successful job not completed")
			}
		})
	}
}

func TestUnitRetention(t *testing.T) {
	f := newFixture(t)
	f.store.keys = []string{"blob"}
	f.store.drops = []int{PruneBatch, 1}
	if err := f.svc.Pruned(t.Context(), queue.Job{}); err != nil || len(f.objects.deleted) != 2 {
		t.Fatal("prune", err)
	}
	if err := f.svc.Purge(t.Context(), f.store.in.Account); err != nil {
		t.Fatal(err)
	}
	f.store.drop = errDown
	if err := f.svc.Pruned(t.Context(), queue.Job{}); err == nil {
		t.Fatal("drop failure ignored")
	}
	f.store.drop = nil
	f.objects.delete = errDown
	if err := f.svc.Purge(t.Context(), f.store.in.Account); err == nil {
		t.Fatal("blob deletion failure ignored")
	}
}

func TestUnitSafeHeaders(t *testing.T) {
	h := http.Header{"Authorization": {"secret"}, "X-Gitlab-Token": {"secret"}}
	for _, name := range []string{"Content-Type", "User-Agent", "X-GitHub-Event", "X-GitHub-Delivery", "X-Hub-Signature-256", "X-Gitlab-Event", "X-Gitlab-Event-UUID", "X-Event-Key", "X-Request-UUID", "X-Request-Id", "X-Hub-Signature"} {
		h.Set(name, "safe")
	}
	for _, p := range []string{"GITHUB_ENTERPRISE", "GITLAB", "BITBUCKET_CLOUD", "BITBUCKET_DATACENTER", "AZURE_DEVOPS"} {
		got := SafeHeaders(p, h)
		if got.Get("Authorization") != "" || got.Get("X-Gitlab-Token") != "" || got.Get("User-Agent") != "safe" {
			t.Fatal("unsafe headers")
		}
		got.Set("User-Agent", "changed")
		if h.Get("User-Agent") != "safe" {
			t.Fatal("headers alias request")
		}
		for name := range got {
			if got.Get(name) == "" {
				t.Fatalf("noncanonical retained header %q", name)
			}
		}
	}
}

func TestUnitDeliveryDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.receive(t, []byte(`{"type":"push"}`)); err != nil {
			t.Fatal(err)
		}
		<-time.After(10 * time.Minute)
		if err := f.svc.Processed(t.Context(), f.job()); err != nil || f.parsed != 0 || f.store.d.State != Expired {
			t.Fatal("deadline did not expire without parsing", err)
		}
	})
}

func (s *store) Hold(_ context.Context, ref Ref) (bool, error) {
	return s.d.Replays == ref.Replays && s.d.State == ref.State, s.state
}
func (s *store) Failure(_ context.Context, ref Ref) error {
	if s.d.Replays == ref.Replays {
		if s.d.State == Received {
			s.d.State = Failed
		}
		s.d.LastError = ProcessingFailed
	}
	return s.state
}
func (j *jobs) StatusByKeyPrefixes(_ context.Context, _ kernel.ULID, _ []queue.Kind, prefixes []string) (map[string]queue.KeyStatus, error) {
	out := map[string]queue.KeyStatus{}
	if j.statusError != nil {
		return nil, j.statusError
	}
	for _, p := range prefixes {
		if j.dead[p] {
			out[p] = queue.KeyStatus{Dead: 1}
		}
		if j.statuses != nil {
			out[p] = j.statuses[p]
		}
	}
	return out, j.deadError
}
func (j *jobs) HoldByKeyPrefixTx(ctx context.Context, _ kernel.ULID, _ []queue.Kind, prefix string) (queue.KeyStatus, error) {
	if !tx.In(ctx) {
		return queue.KeyStatus{}, queue.ErrNoTransaction
	}
	j.checked++
	j.heldPrefix = prefix
	if j.statusError != nil {
		return queue.KeyStatus{}, j.statusError
	}
	out := queue.KeyStatus{}
	if j.dead[prefix] {
		out.Dead = 1
	}
	if j.statuses != nil {
		out = j.statuses[prefix]
	}
	return out, j.deadError
}

func (o *objects) LockLiveAt(ctx context.Context, _ objectstore.Name, _ string, _ time.Time) (objectstore.Object, error) {
	if !tx.In(ctx) {
		return objectstore.Object{}, queue.ErrNoTransaction
	}
	o.locked++
	return objectstore.Object{}, o.live
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
