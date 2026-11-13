package core

import (
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// bucket is an in-memory Objects: each object's bytes and state,
// pending, live or deleting. Put reads its whole source before it
// answers, as the store's does, and runs onPut once it has; broken names
// the one method that fails with errDown.
type bucket struct {
	objects map[string][]byte
	states  map[string]string
	onPut   func()
	broken  string
}

func newBucket() *bucket {
	return &bucket{objects: map[string][]byte{}, states: map[string]string{}}
}

func (b *bucket) Put(
	_ context.Context,
	store objectstore.Name,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	if b.broken == "Put" {
		return objectstore.Object{}, errDown
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return objectstore.Object{}, err
	}
	if int64(len(data)) != size {
		return objectstore.Object{}, objectstore.ErrSize
	}
	k := string(store) + "/" + key
	b.objects[k], b.states[k] = data, "pending"
	if b.onPut != nil {
		b.onPut()
	}
	return objectstore.Object{Size: size, SHA256: sha256.Sum256(data)}, nil
}

func (b *bucket) Commit(ctx context.Context, store objectstore.Name, key string) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if b.broken == "Commit" {
		return errDown
	}
	b.states[string(store)+"/"+key] = "live"
	return nil
}

func (b *bucket) Open(
	_ context.Context,
	store objectstore.Name,
	key string,
) (io.ReadCloser, error) {
	k := string(store) + "/" + key
	switch {
	case b.broken == "Open":
		return nil, errDown
	case b.states[k] != "live":
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b.objects[k])), nil
}

func (b *bucket) Delete(ctx context.Context, store objectstore.Name, key string) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if b.broken == "Delete" {
		return errDown
	}
	b.states[string(store)+"/"+key] = "deleting"
	return nil
}

// byState are the keys of b's objects in each state.
func (b *bucket) byState() map[string][]string {
	out := map[string][]string{}
	for k, st := range b.states {
		out[st] = append(out[st], k)
	}
	return out
}

// writes is a service over a fresh store and bucket, with the workspace
// of target existing and run holding its exclusive lock x.
type writes struct {
	m      *mem
	b      *bucket
	s      *Service
	target Target
	run    kernel.ULID
}

func newWrites() writes {
	m, b := newMem(), newBucket()
	w := writes{
		m: m, b: b, s: New(Deps{DB: db{}, Store: m, Stacks: m, Objects: b}),
		target: Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID(), Workspace: "blue"},
		run:    kernel.NewULID(),
	}
	m.workspaces[w.target] = true
	m.locks[w.target] = []Lock{lockOf("x", Exclusive, w.run)}
	return w
}

// write writes body under lock x, with md5 as its Content-MD5.
func (w writes) write(t *testing.T, body, md5 string) (bool, error) {
	t.Helper()
	return w.s.Write(t.Context(), Write{
		Target: w.target, Run: w.run, LockID: "x", Body: strings.NewReader(body),
		Size: int64(len(body)), ContentMD5: md5,
	})
}

// plain is a plain state of serial 1, and plainMD5 its Content-MD5,
// given as text so the test runs no MD5 under fips140=only.
const (
	plain    = `{"version":4,"serial":1,"lineage":"l"}`
	plainMD5 = "VXaZ0ITmi5uNhBzmzahNVw=="
)

// TestUnitStateWrite checks that a write under the run's exclusive lock
// adds a version whose object is live and whose metadata the body gives,
// a plain state's serial and lineage or a client-encrypted state's none,
// under a key of the account, the stack, the workspace's SHA-256 and the
// version; that the same body again adds none and deletes its object;
// and that a body that is no state, or fails its Content-MD5 outside
// fips140=only, has its object deleted and adds none.
func TestUnitStateWrite(t *testing.T) {
	w := newWrites()
	added, err := w.write(t, plain, plainMD5)
	if err != nil || !added {
		t.Fatalf("a plain state: %v, %v", added, err)
	}
	v := w.m.current[w.target]
	sum := sha256.Sum256([]byte(plain))
	wantKey := fmt.Sprintf("%s/%s/%x/%s", w.target.Account, w.target.Stack,
		sha256.Sum256([]byte("blue")), v.ID)
	if v.Key != wantKey || v.Stack != w.target.Stack || v.Workspace != "blue" ||
		v.Size != int64(len(plain)) || !bytes.Equal(v.SHA256, sum[:]) || v.Serial == nil ||
		*v.Serial != 1 || v.Lineage == nil || *v.Lineage != "l" || v.ClientEncrypted ||
		v.Run == nil || *v.Run != w.run {
		t.Errorf("version %+v, key %s, want %s", v, v.Key, wantKey)
	}
	if got := w.b.byState(); !reflect.DeepEqual(got, map[string][]string{
		"live": {"states/" + wantKey},
	}) {
		t.Errorf("objects %v", got)
	}

	if added, err := w.write(t, plain, ""); err != nil || added {
		t.Errorf("the same body again: %v, %v", added, err)
	}
	if w.m.current[w.target].ID != v.ID || len(w.b.byState()["deleting"]) != 1 {
		t.Errorf("the same body again made %+v current, objects %v", w.m.current[w.target],
			w.b.byState())
	}

	encrypted := `{"meta":{"key_provider.pbkdf2.k":"e30="},"encrypted_data":"eA==",` +
		`"encryption_version":"v0"}`
	if added, err := w.write(t, encrypted, ""); err != nil || !added {
		t.Fatalf("a client-encrypted state: %v, %v", added, err)
	}
	if e := w.m.current[w.target]; !e.ClientEncrypted || e.Serial != nil || e.Lineage != nil {
		t.Errorf("a client-encrypted version %+v", e)
	}

	// A state larger than the decoder's and the pipe's buffers streams
	// past the scan.
	big := `{"version":4,"serial":2,"lineage":"l","resources":[` +
		strings.Repeat(`{"a":[1,2.5,{"b":null,"c":"`+strings.Repeat("x", 100)+`"}]},`, 10000) +
		`true]}`
	if added, err := w.write(t, big, ""); err != nil || !added ||
		*w.m.current[w.target].Serial != 2 {
		t.Errorf("a large state: %v, %v", added, err)
	}

	for _, body := range []string{
		``, `[]`, `"s"`, `{"version":4,"serial":1}`, `{"version":4,"lineage":"l"}`,
		`{"serial":1,"lineage":"l"}`, `{"version":4,"serial":"1","lineage":"l"}`,
		`{"version":4,"serial":1.5,"lineage":"l"}`, `{"version":4,"serial":-1,"lineage":"l"}`,
		`{"version":4,"serial":1,"lineage":2}`, `{"version":4,"serial":1,"lineage":"l"} {}`,
		`{"version":4,"serial":1,"lineage":"l"`, `{"version":[4,`, `{"a":1,}`, `{"serial":}`,
		`{"lineage":}`,
	} {
		deleting := len(w.b.byState()["deleting"])
		current := w.m.current[w.target].ID
		if added, err := w.write(t, body, ""); !errors.Is(err, ErrState) || added {
			t.Errorf("%q: %v, %v", body, added, err)
		}
		if len(w.b.byState()["deleting"]) != deleting+1 || w.m.current[w.target].ID != current {
			t.Errorf("%q left objects %v", body, w.b.byState())
		}
	}

	for i, md5 := range []string{"AAAAAAAAAAAAAAAAAAAAAA==", "not base64"} {
		added, err := w.write(t, fmt.Sprintf(`{"version":4,"serial":%d,"lineage":"l"}`, 7+i), md5)
		if fips140.Enforced() {
			if err != nil || !added {
				t.Errorf("Content-MD5 %q under fips140=only: %v, %v", md5, added, err)
			}
			continue
		}
		if !errors.Is(err, ErrMD5) || added {
			t.Errorf("Content-MD5 %q: %v, %v", md5, added, err)
		}
	}
}

// TestUnitStateWriteLock checks that a write without the run's exclusive
// lock of its ID is refused before its body streams, naming the holder,
// or {} when no lock is held; and that one whose lock goes while its body
// streams is refused as it commits, its object deleted.
func TestUnitStateWriteLock(t *testing.T) {
	w := newWrites()
	other := kernel.NewULID()
	for _, tc := range []struct {
		name  string
		locks []Lock
		want  string
	}{
		{"no lock", nil, `{}`},
		{"another run's", []Lock{lockOf("x", Exclusive, other)}, `{"ID":"x"}`},
		{"another ID", []Lock{lockOf("y", Exclusive, w.run)}, `{"ID":"y"}`},
		{"shared", []Lock{lockOf("x", Shared, w.run)}, `{"ID":"x"}`},
	} {
		w.m.locks[w.target] = tc.locks
		_, err := w.write(t, plain, "")
		c, ok := errors.AsType[*Conflict](err)
		if !ok || string(c.Holder.Info) != tc.want {
			t.Errorf("%s: %v", tc.name, err)
		}
		if len(w.b.objects) != 0 {
			t.Errorf("%s: wrote %v", tc.name, w.b.byState())
		}
	}
	if (&Conflict{}).Error() != "state: the workspace holds no lock" {
		t.Errorf("no holder: %v", &Conflict{})
	}

	w.m.locks[w.target] = []Lock{lockOf("x", Exclusive, w.run)}
	w.b.onPut = func() { w.m.locks[w.target] = []Lock{lockOf("z", Exclusive, other)} }
	_, err := w.write(t, plain, "")
	if c, ok := errors.AsType[*Conflict](err); !ok || c.Holder.ID != "z" {
		t.Errorf("a lock lost while streaming: %v", err)
	}
	if got := w.b.byState(); len(got["deleting"]) != 1 || len(got) != 1 ||
		len(w.m.current) != 0 {
		t.Errorf("a lock lost while streaming left %v and %v", got, w.m.current)
	}
}

// TestUnitStateWriteDown checks that a failing store or bucket fails the
// write: a failed Put or commit leaves the object pending for the
// cleanup, and a failed delete of a refused body joins the refusal.
func TestUnitStateWriteDown(t *testing.T) {
	for _, tc := range []struct{ store, bucket, body string }{
		{"Locks", "", plain},
		{"", "Put", plain},
		{"Hold", "", plain},
		{"Current", "", plain},
		{"", "Commit", plain},
		{"AddVersion", "", plain},
		{"", "Delete", `[]`},
	} {
		w := newWrites()
		w.m.fail, w.b.broken = tc.store, tc.bucket
		_, err := w.write(t, tc.body, "")
		if !errors.Is(err, errDown) {
			t.Errorf("%s%s: %v", tc.store, tc.bucket, err)
		}
		if tc.bucket == "Delete" && !errors.Is(err, ErrState) {
			t.Errorf("a failed delete hides the refusal: %v", err)
		}
		if len(w.m.current) != 0 {
			t.Errorf("%s%s added %v", tc.store, tc.bucket, w.m.current)
		}
	}
	w := newWrites()
	if _, err := w.s.Write(t.Context(), Write{
		Target: w.target, Run: w.run, LockID: "x",
		Body: io.MultiReader(strings.NewReader(`{"version":`), iotest.ErrReader(errDown)),
		Size: 100,
	}); !errors.Is(err, errDown) || len(w.b.objects) != 0 {
		t.Errorf("a body that fails: %v, %v", err, w.b.byState())
	}
	w.s.stacks = notFound{}
	if _, err := w.write(t, plain, ""); !errors.Is(err, stacksapi.ErrNotFound) ||
		len(w.b.byState()["pending"]) != 1 {
		t.Errorf("a deleted stack: %v, %v", err, w.b.byState())
	}
}

// notFound is a Stacks whose every stack is gone.
type notFound struct{}

func (notFound) Hold(context.Context, kernel.AccountID, kernel.ULID) error {
	return stacksapi.ErrNotFound
}

// TestUnitStateRead checks that a read answers the current version and
// its object, and none for a workspace without one; that Latest answers
// its metadata; and that Outputs reads the default workspace's plain
// state's outputs by name, none for a client-encrypted one, which it
// never opens, or for a state without outputs.
func TestUnitStateRead(t *testing.T) {
	w := newWrites()
	ctx := t.Context()
	if _, _, ok, err := w.s.Read(ctx, w.target); ok || err != nil {
		t.Errorf("no version: %v, %v", ok, err)
	}
	if _, ok, err := w.s.Latest(ctx, w.target.Account, w.target.Stack, "blue"); ok || err != nil {
		t.Errorf("Latest of no version: %v, %v", ok, err)
	}
	if out, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack); out != nil || err != nil {
		t.Errorf("Outputs of no version: %v, %v", out, err)
	}
	if _, err := w.write(t, plain, ""); err != nil {
		t.Fatal(err)
	}
	v, r, ok, err := w.s.Read(ctx, w.target)
	if err != nil || !ok {
		t.Fatalf("Read: %v, %v", ok, err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != plain || v.ID != w.m.current[w.target].ID {
		t.Errorf("Read %+v %s", v, got)
	}
	latest, ok, err := w.s.Latest(ctx, w.target.Account, w.target.Stack, "blue")
	if err != nil || !ok || latest.ID != v.ID || *latest.Serial != 1 {
		t.Errorf("Latest %+v, %v, %v", latest, ok, err)
	}

	w.target.Workspace = stateapi.DefaultWorkspace
	w.m.locks[w.target] = []Lock{lockOf("x", Exclusive, w.run)}
	state := `{"version":4,"serial":1,"lineage":"l","resources":[{"outputs":{"no":{}}}],` +
		`"outputs":{"b":{"value":[1,{"c":2}],"type":"number"},` +
		`"a":{"value":"s","type":"string","sensitive":true}},"check_results":null}`
	if _, err := w.write(t, state, ""); err != nil {
		t.Fatal(err)
	}
	out, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack)
	want := []stateapi.Output{
		{Name: "a", Value: json.RawMessage(`"s"`), Sensitive: true},
		{Name: "b", Value: json.RawMessage(`[1,{"c":2}]`)},
	}
	if err != nil || !reflect.DeepEqual(out, want) {
		t.Errorf("Outputs %+v, %v", out, err)
	}
	if _, err := w.write(t, `{"version":4,"serial":2,"lineage":"l"}`, ""); err != nil {
		t.Fatal(err)
	}
	if out, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack); out != nil || err != nil {
		t.Errorf("Outputs of a state without outputs: %v, %v", out, err)
	}
	if _, err := w.write(t, `{"encrypted_data":"eA=="}`, ""); err != nil {
		t.Fatal(err)
	}
	w.b.broken = "Open"
	if out, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack); out != nil || err != nil {
		t.Errorf("Outputs of a client-encrypted state: %v, %v", out, err)
	}
}

// TestUnitStateReadDown checks that a failing store or bucket fails a
// read, and that a stored state whose outputs do not decode fails
// Outputs.
func TestUnitStateReadDown(t *testing.T) {
	w := newWrites()
	ctx := t.Context()
	w.target.Workspace = stateapi.DefaultWorkspace
	w.m.locks[w.target] = []Lock{lockOf("x", Exclusive, w.run)}
	if _, err := w.write(t, plain, ""); err != nil {
		t.Fatal(err)
	}
	w.m.fail = "Current"
	if _, _, _, err := w.s.Read(ctx, w.target); !errors.Is(err, errDown) {
		t.Errorf("Read with Current down: %v", err)
	}
	if _, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack); !errors.Is(err, errDown) {
		t.Errorf("Outputs with Current down: %v", err)
	}
	w.m.fail, w.b.broken = "", "Open"
	if _, _, _, err := w.s.Read(ctx, w.target); !errors.Is(err, errDown) {
		t.Errorf("Read with Open down: %v", err)
	}
	if _, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack); !errors.Is(err, errDown) {
		t.Errorf("Outputs with Open down: %v", err)
	}
	w.b.broken = ""
	for _, stored := range []string{`[]`, `{"a":`, `{"a":1,"outputs":`, `{"outputs":[1]}`} {
		w.b.objects["states/"+w.m.current[w.target].Key] = []byte(stored)
		if _, err := w.s.Outputs(ctx, w.target.Account, w.target.Stack); err == nil {
			t.Errorf("Outputs of %q", stored)
		}
	}
}
