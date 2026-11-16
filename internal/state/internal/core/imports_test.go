package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func (m *mem) LockForRun(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	if err := m.failing("LockForRun"); err != nil {
		return stacksapi.Settings{}, err
	}
	set, ok := m.settings[id]
	if !ok || set.Account != acct {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	m.lockedRun = append(m.lockedRun, id.String()+inTx(ctx))
	return set, nil
}

// stackRuns is a runsapi.StackRuns answering list for every stack, with
// whether each call came in a transaction, failing with err.
type stackRuns struct {
	list []runsapi.UnendedRun
	inTx []bool
	err  error
}

func (r *stackRuns) Unended(ctx context.Context, _ kernel.AccountID, _ kernel.ULID) ([]runsapi.UnendedRun, error) {
	r.inTx = append(r.inTx, inTx(ctx) != "")
	return r.list, r.err
}

// The state files an import takes and refuses.
const (
	plainState     = `{"version":4,"terraform_version":"1.15.0","serial":3,"lineage":"l-1","outputs":{}}`
	encryptedState = `{"meta":{"key_provider.pbkdf2.k":"e30="},"encrypted_data":"AAEC","encryption_version":"v0"}`
)

// imports is a service over a fresh store, bucket and runs, at a fixed
// time, with p's account's stack st, slugged "web", keeping its state
// under p's lock with only a proposed run under way.
type imports struct {
	m    *mem
	b    *bucket
	runs *stackRuns
	s    *Service
	p    kernel.Principal
	st   stacksapi.Stack
	now  time.Time
}

func newImports() imports {
	m, b := newMem(), newBucket()
	acct := kernel.NewAccountID()
	i := imports{
		m: m, b: b,
		runs: &stackRuns{list: []runsapi.UnendedRun{{
			ID: kernel.NewULID(), Type: kernel.RunTypeProposed, State: kernel.RunStatePlanning,
		}}},
		p: kernel.Principal{
			Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: acct, Login: "alice",
		},
		st:  stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "web"},
		now: time.Unix(1_800_000_000, 0).UTC(),
	}
	i.s = New(Deps{
		DB: db{}, Store: m, Stacks: m, Objects: b, Runs: i.runs,
		Now: func() time.Time { return i.now },
	})
	m.stacks = append(m.stacks, i.st)
	m.settings[i.st.ID] = stacksapi.Settings{
		Stack: i.st, ManagesState: true,
		Lock: &stacksapi.Lock{Holder: i.p.Caller(), By: "alice"},
	}
	return i
}

// upload stores body as the live state file p uploaded, answering its
// object ID.
func (i imports) upload(p kernel.Principal, body string) string {
	id := kernel.NewULID()
	k := string(objectstore.Uploads) + "/" + UploadKey(p.AccountID, id, p.Caller())
	i.b.objects[k], i.b.states[k] = []byte(body), "live"
	return id.String()
}

// uploaded is the state of the upload of p's object, "" for none.
func (i imports) uploaded(p kernel.Principal, object string) string {
	id, _ := kernel.ParseULID(object)
	return i.b.states[string(objectstore.Uploads)+"/"+UploadKey(p.AccountID, id, p.Caller())]
}

// create runs the import of object at st's creation, in a transaction
// from 192.0.2.1.
func (i imports) create(t *testing.T, object string) error {
	t.Helper()
	return inTransaction(t, func(ctx context.Context) error {
		return i.s.Created(accessapi.WithClientAddr(ctx, "192.0.2.1"), i.p, i.st, object)
	})
}

// current is the current version of st's default workspace.
func (i imports) current() (Version, bool) {
	v, ok := i.m.current[Target{Account: i.st.Account, Stack: i.st.ID}]
	return v, ok
}

// invalidAt reports whether err is a *kernel.Invalid of the import whose
// one field is pointer, refused for reason.
func invalidAt(err error, pointer, reason string) bool {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	return ok && inv.Detail == invalidDetail && len(inv.Errors) == 1 &&
		inv.Errors[0].Pointer == pointer && inv.Errors[0].Reason == reason
}

// TestUnitImportAtCreation checks that a creation that names an upload
// opens the default workspace and makes the file its first version,
// resealed, marked imported by the caller at the service's time, and
// deletes the upload and records the import from the caller's address;
// that a client-encrypted file goes in with no serial or lineage; and
// that each refusal points at stackObjectID with its reason, importing
// nothing: an object ID that is no ULID, another principal's upload, one
// imported already, one over 100 MiB, and a file that is no version 4
// state or encrypted envelope.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestUnitImportAtCreation(t *testing.T) {
	i := newImports()
	obj := i.upload(i.p, plainState)
	if err := i.create(t, obj); err != nil {
		t.Fatalf("Created with an upload = %v", err)
	}
	v, ok := i.current()
	sum := sha256.Sum256([]byte(plainState))
	switch {
	case !ok || !v.Imported || v.ImportedUsername == nil || *v.ImportedUsername != "alice" ||
		v.ImportedAt == nil || !v.ImportedAt.Equal(i.now) || v.Run != nil:
		t.Errorf("the imported version = %+v", v)
	case v.Serial == nil || *v.Serial != 3 || v.Lineage == nil || *v.Lineage != "l-1" ||
		v.ClientEncrypted || v.Size != int64(len(plainState)) || string(v.SHA256) != string(sum[:]):
		t.Errorf("the imported version's metadata = %+v", v)
	case v.Key != versionKey(Target{Account: i.st.Account, Stack: i.st.ID}, v.ID) ||
		i.b.states["states/"+v.Key] != "live" ||
		string(i.b.objects["states/"+v.Key]) != plainState:
		t.Errorf("the version's object %s is %q", v.Key, i.b.states["states/"+v.Key])
	case i.uploaded(i.p, obj) != "deleting":
		t.Errorf("the upload is %q after its import", i.uploaded(i.p, obj))
	}
	if len(i.m.audits) != 1 {
		t.Fatalf("audits = %+v", i.m.audits)
	}
	e := i.m.audits[0]
	var meta map[string]any
	_ = json.Unmarshal(e.AffectedResource.Metadata, &meta)
	if e.Action != ActionImport || e.EventType != "UPDATE" || e.Actor.Username != "alice" ||
		e.AccountID != i.st.Account.ULID || e.AffectedResource.Type != ResourceStack ||
		e.AffectedResource.ID != i.st.ID.String() || e.RemoteIP != "192.0.2.1" ||
		meta["versionId"] != v.ID.String() || meta["workspace"] != DefaultName ||
		meta["clientEncrypted"] != false {
		t.Errorf("the audit entry = %+v with metadata %v", e, meta)
	}

	i = newImports()
	if err := i.create(t, i.upload(i.p, encryptedState)); err != nil {
		t.Fatalf("Created with an encrypted upload = %v", err)
	}
	if v, ok := i.current(); !ok || !v.ClientEncrypted || v.Serial != nil || v.Lineage != nil ||
		!v.Imported {
		t.Errorf("the imported encrypted version = %+v", v)
	}

	other := i.p
	other.ActorID, other.Kind = kernel.NewULID(), kernel.PrincipalAPIKey
	theirs := i.upload(other, plainState)
	imported := i.upload(i.p, plainState)
	if err := i.create(t, imported); err != nil {
		t.Fatal(err)
	}
	big := i.upload(i.p, plainState)
	id, _ := kernel.ParseULID(big)
	i.b.sizes = map[string]int64{UploadKey(i.st.Account, id, i.p.Caller()): MaxUpload + 1}
	for _, tc := range []struct {
		name, object, reason string
	}{
		{"an object ID that is no ULID", "not-an-id", kernel.ReasonNotFound},
		{"an object ID with no upload", kernel.NewULID().String(), kernel.ReasonNotFound},
		{"another principal's upload", theirs, kernel.ReasonNotFound},
		{"an upload imported already", imported, kernel.ReasonNotFound},
		{"an upload over 100 MiB", big, kernel.ReasonMaximum},
		{"a state of version 3", i.upload(i.p, `{"version":3,"serial":1,"lineage":"l"}`), kernel.ReasonRule},
		{"a version that is a string", i.upload(i.p, `{"version":"4","serial":1,"lineage":"l"}`), kernel.ReasonRule},
		{"a plain state without lineage", i.upload(i.p, `{"version":4,"serial":1}`), kernel.ReasonRule},
		{"an envelope without its version", i.upload(i.p, `{"encrypted_data":"AA"}`), kernel.ReasonRule},
		{"no JSON object", i.upload(i.p, `[1, 2]`), kernel.ReasonRule},
		{"no JSON", i.upload(i.p, `terraform`), kernel.ReasonRule},
	} {
		before := len(i.m.versions)
		if err := i.create(t, tc.object); !invalidAt(err, "/stackObjectID", tc.reason) ||
			len(i.m.versions) != before {
			t.Errorf("%s = %v, adding %d versions", tc.name, err, len(i.m.versions)-before)
		}
	}
	if i.uploaded(other, theirs) != "live" || len(i.m.audits) != 2 {
		t.Errorf("refusals left another's upload %q and recorded %d audit entries",
			i.uploaded(other, theirs), len(i.m.audits)-2)
	}
}

// TestUnitImportable checks which state files an import takes, by what
// each says of itself, while a write takes every plain or encrypted
// state whatever its version.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestUnitImportable(t *testing.T) {
	for body, want := range map[string]bool{
		plainState:     true,
		encryptedState: true,
		`{"version":4,"serial":0,"lineage":"l","resources":[{"version":[4]}]}`: true,
		`{"version":4.0,"serial":1,"lineage":"l"}`:                             false,
		`{"version":{"major":4},"serial":1,"lineage":"l"}`:                     false,
		`{"version":["4"],"serial":1,"lineage":"l"}`:                           false,
		`{"version":5,"serial":1,"lineage":"l"}`:                               false,
		`{"encryption_version":"v0"}`:                                          false,
		`{}`:                                                                   false,
	} {
		m, err := scan(strings.NewReader(body))
		if err != nil || m.importable() != want {
			t.Errorf("%s: importable %t, %v; want %t", body, m.importable(), err, want)
		}
	}
}

// TestUnitImport checks an import into a stack after its creation: under
// the stack's row lock, read in a transaction with its runs, it makes the
// caller's upload the default workspace's new version even when the
// current version has the same bytes, with a proposed run under way, and
// refuses, in order, a stack the account lacks, one that keeps no
// managed state, one unlocked, one another caller locked and one with a
// tracked run, task or destroy run under way, each before the upload is
// read, and then an upload that is missing at /state.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestUnitImport(t *testing.T) {
	i := newImports()
	ctx := t.Context()
	if err := inTransaction(t, func(ctx context.Context) error {
		return i.s.Created(ctx, i.p, i.st, i.upload(i.p, plainState))
	}); err != nil {
		t.Fatal(err)
	}
	first, _ := i.current()
	obj := i.upload(i.p, plainState)
	if err := i.s.Import(ctx, i.p, "198.51.100.7", "web", obj); err != nil {
		t.Fatalf("Import = %v", err)
	}
	v, _ := i.current()
	if v.ID == first.ID || !v.Imported || string(v.SHA256) != string(first.SHA256) ||
		len(i.m.versions) != 2 || i.uploaded(i.p, obj) != "deleting" {
		t.Errorf("a second import of the same bytes = %+v after %+v", v, first)
	}
	if !slices.Equal(i.m.lockedRun, []string{i.st.ID.String() + " in tx"}) ||
		!slices.Equal(i.runs.inTx, []bool{true}) {
		t.Errorf("locked %v, read the runs in a transaction %v", i.m.lockedRun, i.runs.inTx)
	}
	if e := i.m.audits[len(i.m.audits)-1]; e.Action != ActionImport || e.RemoteIP != "198.51.100.7" {
		t.Errorf("the import's audit entry = %+v", e)
	}

	if err := i.s.Import(ctx, i.p, "", "api", obj); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("an import into a stack the account lacks = %v", err)
	}
	set := i.m.settings[i.st.ID]
	run := kernel.NewULID()
	for _, tc := range []struct {
		name   string
		change func(*stacksapi.Settings, *stackRuns)
		detail string
	}{
		{
			"no managed state", func(s *stacksapi.Settings, _ *stackRuns) { s.ManagesState = false },
			"not managed by infraweave",
		},
		{"no lock", func(s *stacksapi.Settings, _ *stackRuns) { s.Lock = nil }, "not locked"},
		{"another's lock", func(s *stacksapi.Settings, _ *stackRuns) {
			s.Lock = &stacksapi.Lock{Holder: "api_key:x", By: "bob"}
		}, "locked by bob"},
		{"a tracked run", func(_ *stacksapi.Settings, r *stackRuns) {
			r.list = append(r.list, runsapi.UnendedRun{ID: run, Type: kernel.RunTypeTracked})
		}, "TRACKED run " + run.String()},
		{"a task", func(_ *stacksapi.Settings, r *stackRuns) {
			r.list = []runsapi.UnendedRun{{ID: run, Type: kernel.RunTypeTask}}
		}, "TASK run"},
		{"a destroy run", func(_ *stacksapi.Settings, r *stackRuns) {
			r.list = []runsapi.UnendedRun{{ID: run, Type: kernel.RunTypeDestroy}}
		}, "DESTROY run"},
	} {
		s, r := set, &stackRuns{}
		tc.change(&s, r)
		i.m.settings[i.st.ID], i.runs.list = s, r.list
		// The object ID names no upload, so a refusal before the upload
		// is read is the only one this import can meet.
		err := i.s.Import(ctx, i.p, "", "web", kernel.NewULID().String())
		if b, ok := errors.AsType[*Blocked](err); !ok || !strings.Contains(b.Detail, tc.detail) ||
			!strings.Contains(b.Error(), tc.detail) {
			t.Errorf("%s = %v, want a *Blocked naming %q", tc.name, err, tc.detail)
		}
	}
	i.m.settings[i.st.ID], i.runs.list = set, nil
	if err := i.s.Import(ctx, i.p, "", "web", obj); !invalidAt(err, "/state",
		kernel.ReasonNotFound) {
		t.Errorf("an import of an upload imported already = %v", err)
	}
	if err := i.s.Import(ctx, i.p, "", "web", i.upload(i.p, `{"version":3}`)); !invalidAt(err,
		"/state", kernel.ReasonRule) {
		t.Errorf("an import of no version 4 state = %v", err)
	}
}

// TestUnitImportDown checks that each read and write an import makes
// fails it with its error, and that a workspace opened with no
// transaction imports nothing.
func TestUnitImportDown(t *testing.T) {
	i := newImports()
	if err := i.s.Created(t.Context(), i.p, i.st, i.upload(i.p, plainState)); !errors.Is(err,
		ErrNoTransaction) {
		t.Errorf("an import outside a transaction = %v", err)
	}
	for _, method := range []string{
		"BySlug", "LockForRun", "Unended", "OpenWorkspace", "LockWorkspace", "AddVersion", "Audit",
		"LockLive", "Open", "Put", "Commit", "Delete",
	} {
		i := newImports()
		i.m.fail, i.b.broken = method, method
		if method == "Unended" {
			i.runs.err = errDown
		}
		err := i.s.Import(t.Context(), i.p, "", "web", i.upload(i.p, plainState))
		if !errors.Is(err, errDown) {
			t.Errorf("an import whose %s fails = %v", method, err)
		}
	}
}
