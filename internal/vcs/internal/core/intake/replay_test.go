package intake

import (
	"context"
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

type adminStore struct {
	s                              *store
	rows                           search.Result[Delivery]
	read, lock, list, reset, audit error
	onLock                         func()
	changed                        bool
	entries                        []kernel.AuditEntry
	resets                         int
}

func (a *adminStore) Owned(_ context.Context, acct kernel.AccountID, integration, id kernel.ULID, before time.Time, lock bool) (Delivery, error) {
	if a.s.d.Account != acct || a.s.d.Integration != integration || a.s.d.ID != id || a.s.d.ReceivedAt.Before(before) {
		return Delivery{}, ErrNotFound
	}
	if lock {
		if a.onLock != nil {
			a.onLock()
		}
		if a.changed {
			a.s.d.Replays++
		}
		return a.s.d, a.lock
	}
	return a.s.d, a.read
}

func (a *adminStore) List(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ time.Time, _ search.Input) (search.Result[Delivery], error) {
	if !tx.In(ctx) {
		return search.Result[Delivery]{}, queue.ErrNoTransaction
	}
	out := a.rows
	out.Edges = append([]search.Edge[Delivery](nil), a.rows.Edges...)
	return out, a.list
}

func (a *adminStore) Reset(ctx context.Context, d Delivery, deadline time.Time) (Delivery, error) {
	if !tx.In(ctx) {
		return d, queue.ErrNoTransaction
	}
	a.resets++
	d.Replays++
	d.State = Received
	d.LastError = ""
	d.RunsCreated = 0
	d.Deadline = deadline
	return d, a.reset
}

func (a *adminStore) Audit(ctx context.Context, e kernel.AuditEntry) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	a.entries = append(a.entries, e)
	return a.audit
}

func bodyMAC(body, secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write(body)
	return []byte(hex.EncodeToString(m.Sum(nil)))
}

func TestUnitStoredSignature(t *testing.T) {
	t.Parallel()
	body, secret := []byte("unprinted-payload"), []byte(strings.Repeat("s", 32))
	for _, tc := range []struct {
		name      string
		signature []byte
		secrets   [][]byte
		body      []byte
		want      bool
	}{
		{"plain", bodyMAC(body, secret), [][]byte{secret}, body, true},
		{"header", append([]byte("sha256="), bodyMAC(body, secret)...), [][]byte{secret}, body, true},
		{"previous", bodyMAC(body, secret), [][]byte{[]byte(strings.Repeat("x", 32)), secret}, body, true},
		{"empty-candidate", bodyMAC(body, secret), [][]byte{nil, secret}, body, true},
		{"wrong-body", bodyMAC(body, secret), [][]byte{secret}, []byte("changed"), false},
		{"wrong-secret", bodyMAC(body, secret), [][]byte{[]byte(strings.Repeat("x", 32))}, body, false},
		{"absent", nil, [][]byte{secret}, body, false},
		{"sha1", []byte("sha1=" + strings.Repeat("0", 40)), [][]byte{secret}, body, false},
		{"badhex", []byte(strings.Repeat("z", 64)), [][]byte{secret}, body, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := verifyStored(tc.body, tc.signature, tc.secrets); got != tc.want {
				t.Fatalf("signature check=%v, want%v", got, tc.want)
			}
		})
	}
	if fips140.Enforced() {
		if verifyStored(body, []byte(strings.Repeat("0", 64)), [][]byte{[]byte("hook")}) {
			t.Fatal("FIPS accepted short key")
		}
	} else if !verifyStored(body, bodyMAC(body, []byte("hook")), [][]byte{[]byte("hook")}) {
		t.Fatal("ordinary-mode short-key semantics changed")
	}
}

func TestUnitReplayKeys(t *testing.T) {
	t.Parallel()
	id, stack := kernel.NewULID(), kernel.NewULID()
	if ProcessKey(id, 0) != id.String() || EvaluationKey(id, 0, stack) != id.String()+":"+stack.String() {
		t.Fatal("legacy keys changed")
	}
	if !strings.HasPrefix(ProcessKey(id, 1), ReplayPrefix(id, 1)) || !strings.HasPrefix(EvaluationKey(id, 1, stack), ReplayPrefix(id, 1)) ||
		strings.HasPrefix(ProcessKey(id, 10), ReplayPrefix(id, 1)) {
		t.Fatal("generation keys overlap")
	}
}

func TestUnitDeliveryReplay(t *testing.T) {
	for _, principal := range []kernel.PrincipalKind{kernel.PrincipalAPIKey, kernel.PrincipalRun} {
		t.Run(string(principal), func(t *testing.T) {
			f := newFixture(t)
			now := time.Now()
			body, secret := []byte("unprinted-body"), []byte(strings.Repeat("s", 32))
			f.store.in.Current = secret
			f.store.d = Delivery{
				Account:     f.store.in.Account,
				ID:          kernel.NewULID(),
				Integration: f.store.in.ID,
				ReceivedAt:  now.Add(-3 * 24 * time.Hour),
				Body:        body,
				Signature:   bodyMAC(body, secret),
				State:       Failed,
				RunsCreated: 4,
				LastError:   ProcessingFailed,
			}
			admin := &adminStore{s: f.store}
			f.svc.d.Admin = admin
			p := kernel.Principal{AccountID: f.store.d.Account, ActorID: kernel.NewULID(), Kind: principal}
			out, err := f.svc.Replay(t.Context(), p, f.store.d.Integration, f.store.d.ID, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if out.Replays != 1 || out.State != Received || out.RunsCreated != 0 || out.LastError != "" || out.ReceivedAt != f.store.d.ReceivedAt || !out.Deadline.After(now) ||
				f.jobs.enqueued != 1 ||
				len(admin.entries) != 1 {
				t.Fatal("replay reset/receipt/audit not atomic", out, admin.entries)
			}
			if admin.entries[0].Actor.Username == "" || admin.entries[0].Action != "vcs_integration.replay_delivery" || admin.entries[0].Body != nil ||
				admin.entries[0].RemoteIP != "127.0.0.1" ||
				f.jobs.heldPrefix != out.ID.String() {
				t.Fatal("unsafe/incomplete audit or lock scope")
			}
			if principal == kernel.PrincipalRun && admin.entries[0].Actor.Run != p.ActorID {
				t.Fatal("run actor lost")
			}
			var payload job
			if err := json.Unmarshal(f.jobs.payload, &payload); err != nil || payload.Replays != 1 {
				t.Fatal("job generation missing", err)
			}
		})
	}
}

func TestUnitReplayRefusalsAndFailures(t *testing.T) {
	for _, fault := range []string{"pending", "running", "owner", "lock", "changed", "signature", "lookup", "reset", "audit", "enqueue", "queue", "blob-expired", "blob-storage", "body-missing", "body-storage", "body-read", "body-large", "begin", "commit", "previous-valid", "previous-expired"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			now := time.Now()
			body, secret := []byte("payload"), []byte(strings.Repeat("s", 32))
			f.store.in.Current = secret
			d := Delivery{
				Account:     f.store.in.Account,
				Integration: f.store.in.ID,
				ID:          kernel.NewULID(),
				ReceivedAt:  now,
				Body:        body,
				Signature:   bodyMAC(body, secret),
				State:       Failed,
			}
			f.store.d = d
			admin := &adminStore{s: f.store}
			f.svc.d.Admin = admin
			switch fault {
			case "pending":
				f.jobs.statuses = map[string]queue.KeyStatus{d.ID.String(): {Pending: 1}}
			case "running":
				f.jobs.statuses = map[string]queue.KeyStatus{d.ID.String(): {Running: 1}}
			case "owner":
				admin.read = ErrNotFound
			case "lock":
				admin.lock = errDown
			case "changed":
				admin.changed = true
			case "signature":
				f.store.in.Current = []byte(strings.Repeat("x", 32))
			case "lookup":
				f.store.lookup = errDown
			case "reset":
				admin.reset = errDown
			case "audit":
				admin.audit = errDown
			case "enqueue":
				f.jobs.enqueue = errDown
			case "queue":
				f.jobs.statusError = errDown
			case "blob-expired", "blob-storage", "body-missing", "body-storage", "body-read", "body-large":
				f.store.d.BodyKey = "body"
				f.store.d.Body = nil
				f.objects.bodies = map[string][]byte{"body": body}
				switch fault {
				case "blob-expired":
					f.objects.live = objectstore.ErrNotFound
				case "blob-storage":
					f.objects.live = errDown
				case "body-missing":
					f.objects.open = objectstore.ErrNotFound
				case "body-storage":
					f.objects.open = errDown
				case "body-read":
					f.objects.read = errDown
				case "body-large":
					f.objects.bodies["body"] = make([]byte, MaxBody+1)
				}
			case "begin":
				f.db.begin = errDown
			case "commit":
				f.db.commit = errDown
			case "previous-valid", "previous-expired":
				f.store.in.Current = []byte(strings.Repeat("x", 32))
				f.store.in.Previous = secret
				until := now.Add(time.Hour)
				if fault == "previous-expired" {
					until = now.Add(-time.Hour)
				}
				f.store.in.PreviousUntil = &until
			}
			_, err := f.svc.Replay(t.Context(), kernel.Principal{AccountID: d.Account}, d.Integration, d.ID, "")
			if fault == "previous-valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "payload") {
				t.Fatal("failure ignored or body leaked", err)
			}
			if fault == "body-storage" || fault == "blob-storage" || fault == "body-read" {
				var conflict *ReplayConflict
				if errors.As(err, &conflict) || !errors.Is(err, errDown) {
					t.Fatal("infrastructure error became expired body", err)
				}
			}
		})
	}
}

func TestUnitDeliveryFailureProjection(t *testing.T) {
	f := newFixture(t)
	acct, id := f.store.in.Account, f.store.in.ID
	current, old, sticky := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	admin := &adminStore{s: f.store, rows: search.Result[Delivery]{Edges: []search.Edge[Delivery]{
		{Node: Delivery{ID: current, Replays: 2, State: Dispatched}},
		{Node: Delivery{ID: old, Replays: 2, State: Dispatched}},
		{Node: Delivery{ID: sticky, Replays: 2, State: Dispatched, LastError: ProcessingFailed}},
	}}}
	f.svc.d.Admin = admin
	f.jobs.statuses = map[string]queue.KeyStatus{ReplayPrefix(current, 2): {Dead: 1, Running: 1}, ReplayPrefix(old, 1): {Dead: 1}}
	out, err := f.svc.List(t.Context(), acct, id, search.Input{})
	if err != nil || len(out.Edges) != 3 || out.Edges[0].Node.State != Failed || out.Edges[0].Node.LastError != ProcessingFailed || out.Edges[1].Node.State != Dispatched ||
		out.Edges[2].Node.State != Failed ||
		admin.rows.Edges[0].Node.State != Dispatched {
		t.Fatal("generation failure projection incorrect", out, err)
	}
	f.jobs.statusError = errDown
	if _, err := f.svc.List(t.Context(), acct, id, search.Input{}); !errors.Is(err, errDown) {
		t.Fatal("summary failure ignored", err)
	}
	f.jobs.statusError = nil
	admin.list = errDown
	if _, err := f.svc.List(t.Context(), acct, id, search.Input{}); !errors.Is(err, errDown) {
		t.Fatal("search failure ignored", err)
	}
	f.svc.d.Admin = nil
	if _, err := f.svc.List(t.Context(), acct, id, search.Input{}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal("missing store accepted", err)
	}
	if _, err := f.svc.Replay(t.Context(), kernel.Principal{}, id, current, ""); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal("missing replay store accepted", err)
	}
}

func TestUnitReplayRechecksSecretWindowAfterLock(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	secret := []byte(strings.Repeat("s", 32))
	until := now.Add(time.Second)
	f.svc.d.Now = func() time.Time { return now }
	f.store.in.Current = []byte(strings.Repeat("x", 32))
	f.store.in.Previous = secret
	f.store.in.PreviousUntil = &until
	f.store.d = Delivery{
		Account:     f.store.in.Account,
		ID:          kernel.NewULID(),
		Integration: f.store.in.ID,
		ReceivedAt:  now,
		Body:        []byte("body"),
		Signature:   bodyMAC([]byte("body"), secret),
		State:       Failed,
	}
	admin := &adminStore{s: f.store, onLock: func() { now = now.Add(2 * time.Second) }}
	f.svc.d.Admin = admin
	_, err := f.svc.Replay(t.Context(), kernel.Principal{AccountID: f.store.d.Account}, f.store.d.Integration, f.store.d.ID, "")
	var conflict *ReplayConflict
	if !errors.As(err, &conflict) || admin.resets != 0 {
		t.Fatal("previous window was evaluated before the row lock", err)
	}
}

func TestUnitReplayFreshAdmission(t *testing.T) {
	for _, name := range []string{"previous window after lookup", "body expires during lock", "body expires during lookup", "row retention after lookup", "fresh reset deadline"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			now := time.Now()
			secret := []byte(strings.Repeat("s", 32))
			body := []byte("retained-body")
			f.svc.d.Now = func() time.Time { return now }
			f.store.in.Current = secret
			f.store.d = Delivery{
				Account:     f.store.in.Account,
				ID:          kernel.NewULID(),
				Integration: f.store.in.ID,
				ReceivedAt:  now,
				Body:        body,
				Signature:   bodyMAC(body, secret),
				State:       Failed,
			}
			admin := &adminStore{s: f.store}
			f.svc.d.Admin = admin
			advance := func() { now = now.Add(2 * time.Second) }
			until := now.Add(time.Second)
			switch name {
			case "previous window after lookup":
				f.store.in.Current = []byte(strings.Repeat("x", 32))
				f.store.in.Previous = secret
				f.store.in.PreviousUntil = &until
				f.store.onLookup = advance
			case "body expires during lock", "body expires during lookup":
				f.store.d.BodyKey = "external"
				f.store.d.Body = nil
				f.objects.bodies["external"] = body
				f.objects.expires = &until
				if name == "body expires during lock" {
					f.objects.onLock = advance
				} else {
					f.store.onLookup = advance
				}
			case "row retention after lookup":
				f.store.d.ReceivedAt = now.Add(-f.svc.d.Retention).Add(time.Second)
				f.store.onLookup = advance
			case "fresh reset deadline":
				f.store.onLookup = advance
			}
			out, err := f.svc.Replay(t.Context(), kernel.Principal{AccountID: f.store.d.Account}, f.store.d.Integration, f.store.d.ID, "")
			if name == "fresh reset deadline" {
				if err != nil || !out.Deadline.Equal(now.Add(f.svc.d.Timeout)) {
					t.Fatal("reset deadline used pre-read clock", out.Deadline, now, err)
				}
				return
			}
			var conflict *ReplayConflict
			if !errors.As(err, &conflict) || admin.resets != 0 || f.jobs.enqueued != 0 || len(admin.entries) != 0 {
				t.Fatal("expired admission changed replay state/job/audit", err, admin.resets, f.jobs.enqueued, len(admin.entries))
			}
		})
	}
}

func TestUnitReplayExternalBodyKeepsExpiry(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	secret := []byte(strings.Repeat("s", 32))
	body := []byte("private retained bytes")
	f.store.in.Current = secret
	f.store.d = Delivery{
		Account:     f.store.in.Account,
		ID:          kernel.NewULID(),
		Integration: f.store.in.ID,
		ReceivedAt:  now,
		BodyKey:     "inbox/body",
		Signature:   bodyMAC(body, secret),
		State:       Failed,
	}
	until := now.Add(time.Hour)
	wantExpiry := until
	f.objects.expires = &until
	f.objects.bodies["inbox/body"] = body
	admin := &adminStore{s: f.store}
	f.svc.d.Admin = admin
	out, err := f.svc.Replay(t.Context(), kernel.Principal{AccountID: f.store.d.Account}, f.store.d.Integration, f.store.d.ID, "")
	if err != nil || out.Replays != 1 || f.objects.locked != 2 || !f.objects.expires.Equal(wantExpiry) {
		t.Fatal("external replay did not validate both expiry gates", out, err, f.objects.locked)
	}
}
