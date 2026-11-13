package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// world is one run granted grant on stack, a sibling stack of the same
// account the run is not of, the workspace's current state, empty for
// none, and the locks and versions the backend asked the core to take,
// release and write, each write's body as it read it. It is the
// backend's StateAccess, Stacks and State.
type world struct {
	acct    kernel.AccountID
	run     kernel.ULID
	stack   stacksapi.Stack
	sibling kernel.ULID
	grant   runsapi.StateGrant
	unkept  bool
	state   string

	accessErr, settingsErr, lockErr, writeErr, readErr error

	locked   []locked
	unlocked []core.Unlock
	written  []core.Write
	bodies   []string
}

type locked struct {
	t    core.Target
	want core.Lock
}

func newWorld() *world {
	acct := kernel.NewAccountID()
	return &world{
		acct:    acct,
		run:     kernel.NewULID(),
		stack:   stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "one"},
		sibling: kernel.NewULID(),
		grant:   runsapi.StateGrant{Read: true, Lock: runsapi.StateLockShared},
	}
}

func (w *world) Access(_ context.Context, run, stack kernel.ULID) (runsapi.StateGrant, error) {
	switch {
	case w.accessErr != nil:
		return runsapi.StateGrant{}, w.accessErr
	case run != w.run:
		return runsapi.StateGrant{}, nil
	case stack != w.stack.ID:
		return runsapi.StateGrant{Account: w.acct}, nil
	}
	g := w.grant
	g.Account = w.acct
	return g, nil
}

func (w *world) BySlug(
	_ context.Context,
	acct kernel.AccountID,
	slug string,
) (stacksapi.Stack, error) {
	if acct != w.acct || slug != w.stack.Slug {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return w.stack, nil
}

func (w *world) Settings(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	switch {
	case w.settingsErr != nil:
		return stacksapi.Settings{}, w.settingsErr
	case acct != w.acct || (id != w.stack.ID && id != w.sibling):
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	return stacksapi.Settings{ManagesState: !w.unkept}, nil
}

func (w *world) Lock(_ context.Context, t core.Target, want core.Lock) error {
	w.locked = append(w.locked, locked{t, want})
	return w.lockErr
}

func (w *world) Unlock(_ context.Context, u core.Unlock) error {
	w.unlocked = append(w.unlocked, u)
	return w.lockErr
}

func (w *world) Write(_ context.Context, v core.Write) (bool, error) {
	body, err := io.ReadAll(v.Body)
	v.Body = nil
	w.written, w.bodies = append(w.written, v), append(w.bodies, string(body))
	if err != nil {
		return false, err
	}
	return w.writeErr == nil, w.writeErr
}

func (w *world) Read(context.Context, core.Target) (core.Version, io.ReadCloser, bool, error) {
	if w.readErr != nil || w.state == "" {
		return core.Version{}, nil, false, w.readErr
	}
	v := core.Version{Version: stateapi.Version{Size: int64(len(w.state))}}
	return v, io.NopCloser(strings.NewReader(w.state)), true, nil
}

// maxSize is the largest version the tests' backend takes.
const maxSize = 1 << 10

// serve sends method on path with body as w's run, after set adjusts the
// world and the request, through a router holding only the backend.
func (w *world) serve(
	t *testing.T,
	method, path, body string,
	set func(*world, *http.Request) *http.Request,
) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	New(w, w, w, maxSize).Routes(r)
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req = req.WithContext(kernel.WithPrincipal(req.Context(),
		kernel.Principal{Kind: kernel.PrincipalRunner, ActorID: w.run}))
	if set != nil {
		req = set(w, req)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const lockBody = `{"ID":"l1","Operation":"OperationTypePlan"}`

// TestUnitBackend checks each route's answer: who may call it, how its
// stack segment resolves, what the run's grant allows, how a LockInfo
// body is read, and how each outcome of the core is written.
func TestUnitBackend(t *testing.T) {
	failed := errors.New("down")
	exclusive := func(w *world, r *http.Request) *http.Request {
		w.grant = runsapi.StateGrant{Read: true, Lock: runsapi.StateLockExclusive, Write: true}
		return r
	}
	none := func(w *world, r *http.Request) *http.Request {
		w.grant = runsapi.StateGrant{}
		return r
	}
	lockFails := func(err error) func(*world, *http.Request) *http.Request {
		return func(w *world, r *http.Request) *http.Request {
			w.lockErr = err
			return r
		}
	}
	writeFails := func(err error) func(*world, *http.Request) *http.Request {
		return func(w *world, r *http.Request) *http.Request {
			w.writeErr = err
			return exclusive(w, r)
		}
	}
	readFails := func(err error) func(*world, *http.Request) *http.Request {
		return func(w *world, r *http.Request) *http.Request {
			w.readErr = err
			return r
		}
	}
	cases := []struct {
		name               string
		method, path, body string
		set                func(*world, *http.Request) *http.Request
		status             int
		// reaches marks a refusal the core's own call makes.
		reaches bool
	}{
		{
			name: "no principal", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: func(_ *world, r *http.Request) *http.Request {
				return r.WithContext(t.Context())
			},
			status: http.StatusUnauthorized,
		},
		{
			name: "an API key", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: func(_ *world, r *http.Request) *http.Request {
				return r.WithContext(kernel.WithPrincipal(r.Context(),
					kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID()}))
			},
			status: http.StatusUnauthorized,
		},
		{
			name: "a slug", method: http.MethodGet, path: "/state/v1/stacks/one",
			status: http.StatusNoContent,
		},
		{
			name: "a canonical ID", method: http.MethodGet, path: "/state/v1/stacks/{stack}",
			status: http.StatusNoContent,
		},
		{
			name: "a workspace", method: http.MethodGet,
			path: "/state/v1/stacks/one/workspaces/blue", status: http.StatusNoContent,
		},
		{
			name: "a read that fails", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: readFails(failed), status: http.StatusInternalServerError,
		},
		{
			name: "a read of a stack gone", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: readFails(stacksapi.ErrNotFound), status: http.StatusNotFound,
		},
		{
			name: "a lowercase ID is a slug", method: http.MethodGet,
			path: "/state/v1/stacks/{lower}", status: http.StatusNotFound,
		},
		{
			name: "an unknown slug", method: http.MethodGet, path: "/state/v1/stacks/two",
			status: http.StatusNotFound,
		},
		{
			name: "another account's stack", method: http.MethodGet,
			path: "/state/v1/stacks/" + kernel.NewULID().String(), status: http.StatusNotFound,
		},
		{
			name: "another stack of the run's account", method: http.MethodGet,
			path: "/state/v1/stacks/{sibling}", status: http.StatusForbidden,
		},
		{
			name: "a run no account has", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: func(_ *world, r *http.Request) *http.Request {
				return r.WithContext(kernel.WithPrincipal(r.Context(),
					kernel.Principal{Kind: kernel.PrincipalRunner, ActorID: kernel.NewULID()}))
			},
			status: http.StatusNotFound,
		},
		{
			name: "a run no account has, by ID", method: http.MethodGet,
			path: "/state/v1/stacks/{stack}",
			set: func(_ *world, r *http.Request) *http.Request {
				return r.WithContext(kernel.WithPrincipal(r.Context(),
					kernel.Principal{Kind: kernel.PrincipalRunner, ActorID: kernel.NewULID()}))
			},
			status: http.StatusNotFound,
		},
		{
			name: "a stack whose state is not kept", method: http.MethodGet,
			path: "/state/v1/stacks/one",
			set: func(w *world, r *http.Request) *http.Request {
				w.unkept = true
				return r
			},
			status: http.StatusNotFound,
		},
		{
			name: "the access read fails", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: func(w *world, r *http.Request) *http.Request {
				w.accessErr = kernel.ErrUnavailable
				return r
			},
			status: http.StatusServiceUnavailable,
		},
		{
			name: "the access read fails, by ID", method: http.MethodGet,
			path: "/state/v1/stacks/{stack}",
			set: func(w *world, r *http.Request) *http.Request {
				w.accessErr = failed
				return r
			},
			status: http.StatusInternalServerError,
		},
		{
			name: "the settings read fails", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: func(w *world, r *http.Request) *http.Request {
				w.settingsErr = failed
				return r
			},
			status: http.StatusInternalServerError,
		},
		{
			name: "an escape that does not decode", method: http.MethodGet,
			path: "/state/v1/stacks/one",
			set: func(_ *world, r *http.Request) *http.Request {
				r.URL.RawPath = "/state/v1/stacks/one/workspaces/b%zz"
				return r
			},
			status: http.StatusNotFound,
		},
		{
			name: "a read without the grant", method: http.MethodGet,
			path: "/state/v1/stacks/one", set: none, status: http.StatusForbidden,
		},
		{
			name: "a write without the grant", method: http.MethodPost,
			path: "/state/v1/stacks/one", body: "{}", status: http.StatusForbidden,
		},
		{
			name: "a write", method: http.MethodPost, path: "/state/v1/stacks/one", body: "{}",
			set: exclusive, status: http.StatusOK,
		},
		{
			name: "a write of no declared length", method: http.MethodPost,
			path: "/state/v1/stacks/one", body: "{}",
			set: func(w *world, r *http.Request) *http.Request {
				r.ContentLength = -1
				return exclusive(w, r)
			},
			status: http.StatusBadRequest,
		},
		{
			name: "a write over max_size", method: http.MethodPost, path: "/state/v1/stacks/one",
			body: strings.Repeat(" ", maxSize+1), set: exclusive,
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name: "a write without the lock", method: http.MethodPost,
			path: "/state/v1/stacks/one", body: "{}",
			set:    writeFails(&core.Conflict{Holder: core.Lock{Info: json.RawMessage(`{}`)}}),
			status: http.StatusLocked,
		},
		{
			name: "a write that fails its Content-MD5", method: http.MethodPost,
			path: "/state/v1/stacks/one", body: "{}", set: writeFails(core.ErrMD5),
			status: http.StatusBadRequest,
		},
		{
			name: "a write that is no state", method: http.MethodPost,
			path: "/state/v1/stacks/one", body: "[]", set: writeFails(core.ErrState),
			status: http.StatusBadRequest,
		},
		{
			name: "a write whose body cannot be read", method: http.MethodPost,
			path: "/state/v1/stacks/one",
			set: func(w *world, r *http.Request) *http.Request {
				r.ContentLength = 10
				r.Body = io.NopCloser(iotest.ErrReader(errors.New("cut")))
				return exclusive(w, r)
			},
			status: http.StatusBadRequest, reaches: true,
		},
		{
			name: "the write fails", method: http.MethodPost, path: "/state/v1/stacks/one",
			body: "{}", set: writeFails(failed), status: http.StatusInternalServerError,
		},
		{
			name: "a lock without the grant", method: "LOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: none, status: http.StatusForbidden,
		},
		{
			name: "a lock without a body", method: "LOCK", path: "/state/v1/stacks/one",
			status: http.StatusBadRequest,
		},
		{
			name: "a lock whose body has no ID", method: "LOCK", path: "/state/v1/stacks/one",
			body: "{}", status: http.StatusBadRequest,
		},
		{
			name: "a lock whose body is no object", method: "LOCK", path: "/state/v1/stacks/one",
			body: "[]", status: http.StatusBadRequest,
		},
		{
			name: "a LockInfo over the bound", method: "LOCK", path: "/state/v1/stacks/one",
			body:   `{"ID":"` + strings.Repeat("a", core.MaxLockInfo) + `"}`,
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name: "a chunked LockInfo over the bound", method: "LOCK",
			path: "/state/v1/stacks/one",
			body: `{"ID":"` + strings.Repeat("a", core.MaxLockInfo) + `"}`,
			set: func(_ *world, r *http.Request) *http.Request {
				r.ContentLength = -1
				return r
			},
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name: "a body that cannot be read", method: "LOCK", path: "/state/v1/stacks/one",
			set: func(_ *world, r *http.Request) *http.Request {
				r.ContentLength = -1
				r.Body = io.NopCloser(iotest.ErrReader(errors.New("cut")))
				return r
			},
			status: http.StatusBadRequest,
		},
		{
			name: "a lock held by another", method: "LOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: lockFails(&core.Conflict{}), status: http.StatusLocked,
		},
		{
			name: "a release of another's lock", method: "UNLOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: lockFails(core.ErrBreak), status: http.StatusForbidden,
		},
		{
			name: "an unlock of no workspace", method: "UNLOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: lockFails(core.ErrNoWorkspace), status: http.StatusNotFound,
		},
		{
			name: "a stack gone under the lock", method: "LOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: lockFails(stacksapi.ErrNotFound), status: http.StatusNotFound,
		},
		{
			name: "a workspace name the table refuses", method: "LOCK",
			path: "/state/v1/stacks/one", body: lockBody,
			set: lockFails(core.ErrWorkspaceName), status: http.StatusBadRequest,
		},
		{
			name: "the lock write fails", method: "LOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: lockFails(failed), status: http.StatusInternalServerError,
		},
		{
			name: "an unlock without the grant", method: "UNLOCK", path: "/state/v1/stacks/one",
			body: lockBody, set: none, status: http.StatusForbidden,
		},
		{
			name: "an unlock whose body has no ID", method: "UNLOCK",
			path: "/state/v1/stacks/one", body: "{}", status: http.StatusBadRequest,
		},
		{
			name: "another method", method: http.MethodDelete, path: "/state/v1/stacks/one",
			status: http.StatusMethodNotAllowed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			path := strings.NewReplacer(
				"{stack}", w.stack.ID.String(),
				"{lower}", strings.ToLower(w.stack.ID.String()),
				"{sibling}", w.sibling.String(),
			).Replace(c.path)
			rec := w.serve(t, c.method, path, c.body, c.set)
			if rec.Code != c.status {
				t.Fatalf("%s %s = %d %s, want %d", c.method, path, rec.Code, rec.Body, c.status)
			}
			ct := rec.Header().Get("Content-Type")
			if c.status >= 400 && c.status != http.StatusLocked &&
				c.status != http.StatusMethodNotAllowed &&
				ct != problem.ContentType {
				t.Errorf("Content-Type = %q, want a problem document", ct)
			}
			if c.status >= 400 && c.status != http.StatusLocked && !c.reaches &&
				len(w.locked)+len(w.unlocked)+len(w.written) > 0 && w.lockErr == nil &&
				w.writeErr == nil {
				t.Errorf("a refused request reached the core: %v %v %v", w.locked, w.unlocked,
					w.written)
			}
		})
	}
}

// TestUnitLockModes checks that a lock is taken for the run on the
// workspace its path names, unescaped, shared or exclusive as the run's
// grant says, with the LockInfo as sent; and that a conflict answers the
// holder's LockInfo as it was sent.
func TestUnitLockModes(t *testing.T) {
	w := newWorld()
	if rec := w.serve(t, "LOCK", "/state/v1/stacks/one/workspaces/a%2Fb", lockBody,
		nil); rec.Code != http.StatusOK {
		t.Fatalf("a shared lock = %d %s", rec.Code, rec.Body)
	}
	w.grant.Lock = runsapi.StateLockExclusive
	if rec := w.serve(t, "LOCK", "/state/v1/stacks/"+w.stack.ID.String(), lockBody,
		nil); rec.Code != http.StatusOK {
		t.Fatalf("an exclusive lock = %d %s", rec.Code, rec.Body)
	}
	want := []locked{
		{
			core.Target{Account: w.acct, Stack: w.stack.ID, Workspace: "a/b"},
			core.Lock{ID: "l1", Mode: core.Shared, Run: w.run, Info: json.RawMessage(lockBody)},
		},
		{
			core.Target{Account: w.acct, Stack: w.stack.ID},
			core.Lock{ID: "l1", Mode: core.Exclusive, Run: w.run, Info: json.RawMessage(lockBody)},
		},
	}
	if !reflect.DeepEqual(w.locked, want) {
		t.Errorf("locked %+v, want %+v", w.locked, want)
	}

	holder := `{"ID":"held","Who":"someone"}`
	w.lockErr = &core.Conflict{Holder: core.Lock{ID: "held", Info: json.RawMessage(holder)}}
	rec := w.serve(t, "LOCK", "/state/v1/stacks/one", lockBody, nil)
	if rec.Code != http.StatusLocked || rec.Body.String() != holder ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("a conflict = %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

// TestUnitUnlockCalls checks that an unlock names the run, the LockInfo's
// ID and the caller's address, and may break another run's lock only for
// a run granted the exclusive lock; an empty body is a force-unlock.
func TestUnitUnlockCalls(t *testing.T) {
	w := newWorld()
	if rec := w.serve(t, "UNLOCK", "/state/v1/stacks/one", lockBody,
		nil); rec.Code != http.StatusOK {
		t.Fatalf("an unlock = %d %s", rec.Code, rec.Body)
	}
	w.grant.Lock = runsapi.StateLockExclusive
	if rec := w.serve(t, "UNLOCK", "/state/v1/stacks/one/workspaces/blue", "",
		nil); rec.Code != http.StatusOK {
		t.Fatalf("a force-unlock = %d %s", rec.Code, rec.Body)
	}
	at := core.Target{Account: w.acct, Stack: w.stack.ID}
	blue := at
	blue.Workspace = "blue"
	want := []core.Unlock{
		{Target: at, Run: w.run, ID: "l1", RemoteIP: "192.0.2.1"},
		{Target: blue, Run: w.run, Break: true, RemoteIP: "192.0.2.1"},
	}
	if !reflect.DeepEqual(w.unlocked, want) {
		t.Errorf("unlocked %+v, want %+v", w.unlocked, want)
	}
}

// TestUnitWriteCalls checks that a write names the run, the workspace its
// path names, the lock its ID query parameter names, the declared length
// and the Content-MD5 as sent, and streams the body as sent; and that a
// write refused for want of the lock answers 423 with {} when none is
// held.
func TestUnitWriteCalls(t *testing.T) {
	w := newWorld()
	w.grant = runsapi.StateGrant{Read: true, Lock: runsapi.StateLockExclusive, Write: true}
	body := `{"version":4,"serial":1,"lineage":"l"}`
	rec := w.serve(t, http.MethodPost, "/state/v1/stacks/one/workspaces/a%2Fb?ID=l1", body,
		func(_ *world, r *http.Request) *http.Request {
			r.Header.Set("Content-MD5", "md5")
			return r
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("a write = %d %s", rec.Code, rec.Body)
	}
	want := []core.Write{{
		Target: core.Target{Account: w.acct, Stack: w.stack.ID, Workspace: "a/b"},
		Run:    w.run, LockID: "l1", Size: int64(len(body)), ContentMD5: "md5",
	}}
	if !reflect.DeepEqual(w.written, want) || w.bodies[0] != body {
		t.Errorf("written %+v %q, want %+v", w.written, w.bodies, want)
	}

	w.writeErr = &core.Conflict{Holder: core.Lock{Info: json.RawMessage(`{}`)}}
	rec = w.serve(t, http.MethodPost, "/state/v1/stacks/one", body, nil)
	if rec.Code != http.StatusLocked || rec.Body.String() != `{}` {
		t.Errorf("a write without the lock = %d %s", rec.Code, rec.Body)
	}
}

// TestUnitReadAnswers checks that a read answers the current state as
// JSON of its size with no Content-MD5, and 204 with no body when there
// is none.
func TestUnitReadAnswers(t *testing.T) {
	w := newWorld()
	w.state = `{"version":4,"serial":1,"lineage":"l"}`
	rec := w.serve(t, http.MethodGet, "/state/v1/stacks/one", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != w.state ||
		rec.Header().Get("Content-Type") != "application/json" ||
		rec.Header().Get("Content-Length") != "38" || rec.Header().Get("Content-MD5") != "" {
		t.Errorf("a read = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	w.state = ""
	if rec := w.serve(t, http.MethodGet, "/state/v1/stacks/one", "", nil); rec.Code !=
		http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("no state = %d %s", rec.Code, rec.Body)
	}
}

// TestUnitUnserved checks that an API built without the state domain
// answers 501 on every backend route and 405 on any other method.
func TestUnitUnserved(t *testing.T) {
	r := chi.NewRouter()
	Unserved(r)
	for _, m := range []string{http.MethodGet, http.MethodPost, "LOCK", "UNLOCK"} {
		for _, p := range []string{"/state/v1/stacks/one", "/state/v1/stacks/one/workspaces/x"} {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), m, p, nil))
			if rec.Code != http.StatusNotImplemented {
				t.Errorf("%s %s = %d", m, p, rec.Code)
			}
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/state/v1/stacks/one", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d", rec.Code)
	}
}
