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

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// world is one run granted grant on stack, a sibling stack of the same
// account the run is not of, in a space of its own, the workspace's
// current state, empty for none, and the locks and versions the backend
// asked the core to take, release and write, each write's body as it read
// it. Both stacks keep their state unless unkept, with external access as
// external says; the access domain answers missing to every state read
// and keeps each principal it was asked for. It is the backend's
// StateAccess, StateReader, Stacks and State.
type world struct {
	acct         kernel.AccountID
	run          kernel.ULID
	stack        stacksapi.Stack
	sibling      kernel.ULID
	siblingSpace kernel.ULID
	grant        runsapi.StateGrant
	unkept       bool
	external     bool
	missing      accessapi.Action
	asked        []kernel.Principal
	state        string

	accessErr, settingsErr, getErr, readerErr, lockErr, writeErr, readErr error

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
		acct: acct,
		run:  kernel.NewULID(),
		stack: stacksapi.Stack{
			ID: kernel.NewULID(), Account: acct, Slug: "one", Space: kernel.NewULID(),
		},
		sibling:      kernel.NewULID(),
		siblingSpace: kernel.NewULID(),
		grant:        runsapi.StateGrant{Read: true, Lock: runsapi.StateLockShared},
	}
}

func (w *world) Access(_ context.Context, run, stack kernel.ULID) (runsapi.StateGrant, error) {
	switch {
	case w.accessErr != nil:
		return runsapi.StateGrant{}, w.accessErr
	case run != w.run:
		return runsapi.StateGrant{}, nil
	case stack != w.stack.ID:
		return runsapi.StateGrant{Account: w.acct, Stack: w.stack.ID, Read: w.grant.Read}, nil
	}
	g := w.grant
	g.Account, g.Stack = w.acct, w.stack.ID
	return g, nil
}

func (w *world) StateRead(_ context.Context, p kernel.Principal, stack, space kernel.ULID) (accessapi.Action, error) {
	w.asked = append(w.asked, p)
	if (stack == w.sibling) != (space == w.siblingSpace) {
		return "", errors.New("the stack is not in that space")
	}
	return w.missing, w.readerErr
}

func (w *world) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error) {
	switch {
	case w.getErr != nil:
		return stacksapi.Stack{}, w.getErr
	case acct != w.acct || id != w.stack.ID:
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return w.stack, nil
}

func (w *world) BySlug(_ context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error) {
	if acct != w.acct || slug != w.stack.Slug {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return w.stack, nil
}

func (w *world) Settings(_ context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	switch {
	case w.settingsErr != nil:
		return stacksapi.Settings{}, w.settingsErr
	case acct != w.acct || (id != w.stack.ID && id != w.sibling):
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	st := stacksapi.Stack{ID: id, Account: acct, Space: w.siblingSpace}
	if id == w.stack.ID {
		st = w.stack
	}
	return stacksapi.Settings{Stack: st, ManagesState: !w.unkept, ExternalStateAccess: w.external}, nil
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
func (w *world) serve(t *testing.T, method, path, body string, set func(*world, *http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	New(w, w, w, w, maxSize).Routes(r)
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
			name: "an API key of no account", method: http.MethodGet, path: "/state/v1/stacks/one",
			set: func(_ *world, r *http.Request) *http.Request {
				return r.WithContext(kernel.WithPrincipal(r.Context(),
					kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: kernel.NewULID()}))
			},
			status: http.StatusNotFound,
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
// grant says, naming who triggered the run, with the LockInfo as sent;
// and that a conflict answers the holder's LockInfo as it was sent.
func TestUnitLockModes(t *testing.T) {
	w := newWorld()
	w.grant.TriggeredBy = "alice"
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
			core.Lock{
				ID: "l1", Mode: core.Shared, Run: w.run, TriggeredBy: "alice",
				Info: json.RawMessage(lockBody),
			},
		},
		{
			core.Target{Account: w.acct, Stack: w.stack.ID},
			core.Lock{
				ID: "l1", Mode: core.Exclusive, Run: w.run, TriggeredBy: "alice",
				Info: json.RawMessage(lockBody),
			},
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

// TestUnitExternal checks the reads of a stack's state by every caller
// but a run token on its own stack: an API key's and an in-run token's
// principal decided as it is, and a run token's as its run's stack, with
// its stack's home space; and each refusal in order, 404 before any 403,
// none reaching the core.
func TestUnitExternal(t *testing.T) {
	failed := errors.New("down")
	key := func(w *world, r *http.Request) *http.Request {
		w.external = true
		return r.WithContext(kernel.WithPrincipal(r.Context(), kernel.Principal{
			Kind: kernel.PrincipalAPIKey, AccountID: w.acct, ActorID: kernel.NewULID(),
		}))
	}
	inRun := func(w *world, r *http.Request) *http.Request {
		w.external = true
		return r.WithContext(kernel.WithPrincipal(r.Context(), kernel.Principal{
			Kind: kernel.PrincipalRun, AccountID: w.acct, ActorID: w.run,
			StackID: w.stack.ID, StackSpaceID: w.stack.Space,
		}))
	}
	runToken := func(w *world, r *http.Request) *http.Request {
		w.external = true
		return r
	}
	and := func(first func(*world, *http.Request) *http.Request, then func(*world)) func(*world, *http.Request) *http.Request {
		return func(w *world, r *http.Request) *http.Request {
			r = first(w, r)
			then(w)
			return r
		}
	}
	cases := []struct {
		name, method, path string
		set                func(*world, *http.Request) *http.Request
		status             int
		action             string
	}{
		{name: "an API key by slug", path: "/state/v1/stacks/one", set: key, status: http.StatusOK},
		{name: "an API key by ID", path: "/state/v1/stacks/{sibling}", set: key, status: http.StatusOK},
		{
			name: "an API key with no state yet", path: "/state/v1/stacks/one",
			set: and(key, func(w *world) { w.state = "" }), status: http.StatusNoContent,
		},
		{
			name: "an API key on a workspace", path: "/state/v1/stacks/one/workspaces/blue",
			set: key, status: http.StatusOK,
		},
		{
			name: "an API key on another account's stack",
			path: "/state/v1/stacks/" + kernel.NewULID().String(), set: key,
			status: http.StatusNotFound,
		},
		{
			name: "a stack the caller cannot see", path: "/state/v1/stacks/one",
			set:    and(key, func(w *world) { w.missing = accessapi.ActionSpaceRead }),
			status: http.StatusNotFound,
		},
		{
			name: "a stack whose state is not kept", path: "/state/v1/stacks/one",
			set: and(key, func(w *world) { w.unkept = true }), status: http.StatusNotFound,
		},
		{
			name: "a write", method: http.MethodPost, path: "/state/v1/stacks/one", set: key,
			status: http.StatusForbidden,
		},
		{
			name: "a lock", method: "LOCK", path: "/state/v1/stacks/one", set: key,
			status: http.StatusForbidden,
		},
		{
			name: "an unlock", method: "UNLOCK", path: "/state/v1/stacks/one", set: key,
			status: http.StatusForbidden,
		},
		{
			name: "a lock by a run of another stack", method: "LOCK",
			path: "/state/v1/stacks/{sibling}", set: runToken, status: http.StatusForbidden,
		},
		{
			name: "external access off", path: "/state/v1/stacks/one",
			set: and(key, func(w *world) { w.external = false }), status: http.StatusForbidden,
		},
		{
			name: "no STACK_STATE_READ", path: "/state/v1/stacks/one",
			set:    and(key, func(w *world) { w.missing = accessapi.ActionStackStateRead }),
			status: http.StatusForbidden, action: "STACK_STATE_READ",
		},
		{
			name: "no STACK_STATE_DOWNLOAD", path: "/state/v1/stacks/one",
			set:    and(key, func(w *world) { w.missing = accessapi.ActionStackStateDownload }),
			status: http.StatusForbidden, action: "STACK_STATE_DOWNLOAD",
		},
		{
			name: "the decision fails", path: "/state/v1/stacks/one",
			set:    and(key, func(w *world) { w.readerErr = failed }),
			status: http.StatusInternalServerError,
		},
		{
			name: "a run token on another stack", path: "/state/v1/stacks/{sibling}",
			set: runToken, status: http.StatusOK,
		},
		{
			name:   "a run token on another stack in a state allowing no read",
			path:   "/state/v1/stacks/{sibling}",
			set:    and(runToken, func(w *world) { w.grant = runsapi.StateGrant{} }),
			status: http.StatusForbidden,
		},
		{
			name: "a run token whose own stack is gone", path: "/state/v1/stacks/{sibling}",
			set:    and(runToken, func(w *world) { w.getErr = stacksapi.ErrNotFound }),
			status: http.StatusNotFound,
		},
		{
			name: "a run token whose own stack cannot be read", path: "/state/v1/stacks/{sibling}",
			set:    and(runToken, func(w *world) { w.getErr = failed }),
			status: http.StatusInternalServerError,
		},
		{
			name: "an in-run token on another stack", path: "/state/v1/stacks/{sibling}",
			set: inRun, status: http.StatusOK,
		},
		{
			name: "an in-run token on its own stack", path: "/state/v1/stacks/one",
			set: inRun, status: http.StatusForbidden,
		},
		{
			name: "an in-run token of a run no account has", path: "/state/v1/stacks/one",
			set: and(inRun, func(w *world) { w.run = kernel.NewULID() }), status: http.StatusNotFound,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			w.state = `{"version":4}`
			method := c.method
			if method == "" {
				method = http.MethodGet
			}
			path := strings.ReplaceAll(c.path, "{sibling}", w.sibling.String())
			rec := w.serve(t, method, path, lockBody, c.set)
			if rec.Code != c.status {
				t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, c.status)
			}
			var p problem.Problem
			if c.status >= 400 && (json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Action != c.action) {
				t.Errorf("the problem = %s, want action %q", rec.Body, c.action)
			}
			if len(w.locked)+len(w.unlocked)+len(w.written) > 0 {
				t.Errorf("an external call reached the core: %v %v %v", w.locked, w.unlocked,
					w.written)
			}
		})
	}
}

// TestUnitExternalPrincipal checks the principal each caller's read is
// decided as: an API key's and an in-run token's as they are, and a run
// token's as a run of its stack, in its stack's home space, so its
// stack's bindings and its read of that space decide.
func TestUnitExternalPrincipal(t *testing.T) {
	w := newWorld()
	w.external = true
	path := "/state/v1/stacks/" + w.sibling.String()
	if rec := w.serve(t, http.MethodGet, path, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("a run token = %d %s", rec.Code, rec.Body)
	}
	want := kernel.Principal{
		Kind: kernel.PrincipalRun, AccountID: w.acct, ActorID: w.run,
		StackID: w.stack.ID, StackSpaceID: w.stack.Space,
	}
	if len(w.asked) != 1 || !reflect.DeepEqual(w.asked[0], want) {
		t.Errorf("a run token decided as %+v, want %+v", w.asked, want)
	}
	key := kernel.Principal{Kind: kernel.PrincipalAPIKey, AccountID: w.acct, ActorID: kernel.NewULID()}
	rec := w.serve(t, http.MethodGet, path, "", func(_ *world, r *http.Request) *http.Request {
		return r.WithContext(kernel.WithPrincipal(r.Context(), key))
	})
	if rec.Code != http.StatusNoContent || len(w.asked) != 2 || !reflect.DeepEqual(w.asked[1], key) {
		t.Errorf("an API key = %d, decided as %+v", rec.Code, w.asked)
	}
}
