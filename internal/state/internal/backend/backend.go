package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// Locks is what the backend asks of the state domain's core.
type Locks interface {
	Lock(ctx context.Context, t core.Target, want core.Lock) error
	Unlock(ctx context.Context, u core.Unlock) error
}

// Stacks is what the backend reads of the stacks domain; stacksapi.Stacks
// is one.
type Stacks interface {
	BySlug(ctx context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error)
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error)
}

// Handler serves the state backend's routes for the run each request's
// run token names, which the state authenticator has put on its context.
type Handler struct {
	access runsapi.StateAccess
	stacks Stacks
	locks  Locks
}

// New returns the backend over the runs domain's access decisions, the
// stacks it reads and the core's locks.
func New(access runsapi.StateAccess, stacks Stacks, locks Locks) *Handler {
	return &Handler{access: access, stacks: stacks, locks: locks}
}

// Routes adds GET, POST, LOCK and UNLOCK on stateapi.StackPattern and
// stateapi.WorkspacePattern to r, registering LOCK and UNLOCK with chi
// first. LOCK and UNLOCK bound their bodies to core.MaxLockInfo with the
// API's own body bound, so an oversized LockInfo answers the same 413.
// Any other method on them answers 405 through r's own handler.
func (h *Handler) Routes(r chi.Router) { mount(r, h.versions, h.versions, h.lock, h.unlock) }

// Unserved adds the routes Routes adds, each answering 501, for an API
// built without the state domain.
func Unserved(r chi.Router) {
	f := func(w http.ResponseWriter, _ *http.Request) {
		problem.Write(w, problem.NotImplemented.New("No state domain serves this route."))
	}
	mount(r, f, f, f, f)
}

func mount(r chi.Router, get, post, lock, unlock http.HandlerFunc) {
	chi.RegisterMethod(stateapi.MethodLock)
	chi.RegisterMethod(stateapi.MethodUnlock)
	bound := r.With(middleware.LimitBodies(core.MaxLockInfo, func(*http.Request) bool {
		return false
	}))
	for _, p := range []string{stateapi.StackPattern, stateapi.WorkspacePattern} {
		r.Get(p, get)
		r.Post(p, post)
		bound.Method(stateapi.MethodLock, p, lock)
		bound.Method(stateapi.MethodUnlock, p, unlock)
	}
}

// versions answers a read or write of a state version, which this build
// does not serve yet, once the run may make it.
func (h *Handler) versions(w http.ResponseWriter, r *http.Request) {
	_, g, _, ok := h.target(w, r)
	if !ok {
		return
	}
	if (r.Method == http.MethodGet && !g.Read) || (r.Method == http.MethodPost && !g.Write) {
		problem.Write(w, problem.Forbidden.New("This run may not "+verbs[r.Method]+
			" the stack's state in its current state."))
		return
	}
	problem.Write(w, problem.NotImplemented.New("This build does not serve state versions."))
}

var verbs = map[string]string{
	http.MethodGet: "read", http.MethodPost: "write",
	stateapi.MethodLock: "lock", stateapi.MethodUnlock: "unlock",
}

// lock takes the lock r's LockInfo names, shared or exclusive as the
// run's grant says.
func (h *Handler) lock(w http.ResponseWriter, r *http.Request) {
	t, g, run, ok := h.target(w, r)
	if !ok || !h.mayLock(w, r, g) {
		return
	}
	id, info, ok := lockInfo(w, r)
	if !ok {
		return
	}
	if id == "" {
		problem.Write(w, problem.InvalidRequest.New("The LockInfo body has no ID."))
		return
	}
	mode := core.Shared
	if g.Lock == runsapi.StateLockExclusive {
		mode = core.Exclusive
	}
	answer(w, h.locks.Lock(r.Context(), t, core.Lock{ID: id, Mode: mode, Run: run, Info: info}))
}

// unlock releases the lock r's LockInfo names, or with an empty body
// every lock on the workspace.
func (h *Handler) unlock(w http.ResponseWriter, r *http.Request) {
	t, g, run, ok := h.target(w, r)
	if !ok || !h.mayLock(w, r, g) {
		return
	}
	id, info, ok := lockInfo(w, r)
	if !ok {
		return
	}
	if id == "" && len(info) > 0 {
		problem.Write(w, problem.InvalidRequest.New("The LockInfo body has no ID."))
		return
	}
	answer(w, h.locks.Unlock(r.Context(), core.Unlock{
		Target: t, Run: run, ID: id, Break: g.Lock == runsapi.StateLockExclusive,
		RemoteIP: ratelimit.ClientAddr(r),
	}))
}

// mayLock answers 403 when g grants no lock.
func (h *Handler) mayLock(w http.ResponseWriter, r *http.Request, g runsapi.StateGrant) bool {
	if g.Lock == runsapi.StateLockNone {
		problem.Write(w, problem.Forbidden.New("This run may not "+verbs[r.Method]+
			" the stack's state in its current state."))
		return false
	}
	return true
}

// target is the workspace r names, the grant of the run r authenticates
// as on its stack, and the run. A stack no account of the run's has, one
// whose state infraweave does not keep, and a run no account has answer
// 404, so a run learns nothing of another account's stacks.
//
// ref: decision 0010 § "Who may call what"
func (h *Handler) target(
	w http.ResponseWriter,
	r *http.Request,
) (core.Target, runsapi.StateGrant, kernel.ULID, bool) {
	ctx := r.Context()
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok || p.Kind != kernel.PrincipalRunner {
		problem.Write(w, problem.Unauthenticated.New("A run token is required."))
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	seg, okStack := param(r, "stack")
	ws, okWorkspace := param(r, "workspace")
	if !okStack || !okWorkspace {
		problem.Write(w, problem.NotFound.New("No such stack or workspace."))
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	g, id, err := h.grant(ctx, p.ActorID, seg)
	if err == nil {
		var st stacksapi.Settings
		if st, err = h.stacks.Settings(ctx, g.Account, id); err == nil && !st.ManagesState {
			err = stacksapi.ErrNotFound
		}
	}
	switch {
	case errors.Is(err, stacksapi.ErrNotFound):
		problem.Write(w, problem.NotFound.New("No stack whose state infraweave keeps is named "+
			seg+"."))
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	case err != nil:
		problem.Failure(w, err)
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	return core.Target{Account: g.Account, Stack: id, Workspace: ws}, g, p.ActorID, true
}

// grant is run's grant on the stack seg names, its canonical ULID or else
// its slug in the run's account, and the stack's ID: stacksapi.ErrNotFound
// when no account has the run or the run's account no stack of that slug.
func (h *Handler) grant(
	ctx context.Context,
	run kernel.ULID,
	seg string,
) (runsapi.StateGrant, kernel.ULID, error) {
	id, err := kernel.ParseULID(seg)
	if err != nil || id.String() != seg {
		g, err := h.access.Access(ctx, run, kernel.ULID{})
		if err != nil || g.Account.IsZero() {
			return runsapi.StateGrant{}, kernel.ULID{}, orNotFound(err)
		}
		st, err := h.stacks.BySlug(ctx, g.Account, seg)
		if err != nil {
			return runsapi.StateGrant{}, kernel.ULID{}, err //nolint:wrapcheck // the stacks domain names it
		}
		id = st.ID
	}
	g, err := h.access.Access(ctx, run, id)
	if err != nil || g.Account.IsZero() {
		return runsapi.StateGrant{}, kernel.ULID{}, orNotFound(err)
	}
	return g, id, nil
}

// orNotFound is err, or stacksapi.ErrNotFound when there is none: a grant
// that names no account is of a run no account has.
func orNotFound(err error) error {
	if err != nil {
		return err //nolint:wrapcheck // the runs domain names the read
	}
	return stacksapi.ErrNotFound
}

// param is r's path parameter name as its route matched it, unescaped
// when chi routed on the escaped path, as it does when the path holds an
// escape such as %2F; false for an escape that does not decode.
func param(r *http.Request, name string) (string, bool) {
	v := chi.URLParam(r, name)
	if r.URL.RawPath == "" {
		return v, true
	}
	v, err := url.PathUnescape(v)
	return v, err == nil
}

// lockInfo reads r's LockInfo body, which mount bounds: its ID and the
// body as sent, both empty for an empty body. A body past the bound
// answers 413, and one that is no JSON object 400.
func lockInfo(w http.ResponseWriter, r *http.Request) (string, json.RawMessage, bool) {
	raw, err := io.ReadAll(r.Body)
	switch {
	case err != nil:
		problem.BodyError(w, err)
		return "", nil, false
	case len(raw) == 0:
		return "", nil, true
	}
	var info struct {
		ID string `json:"ID"` //nolint:tagliatelle // the name Terraform's LockInfo gives it
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		problem.Write(w, problem.InvalidRequest.New("The LockInfo body is not a JSON object."))
		return "", nil, false
	}
	return info.ID, raw, true
}

// answer writes the outcome of a lock or an unlock: 200, 423 with the
// holder's LockInfo, which Terraform reports as the lock's holder, or a
// problem.
//
// ref: decision 0010 § "Errors on the backend paths"
func answer(w http.ResponseWriter, err error) {
	conflict, isConflict := errors.AsType[*core.Conflict](err)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case isConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write(conflict.Holder.Info)
	case errors.Is(err, core.ErrBreak):
		problem.Write(w, problem.Forbidden.New(
			"Only a run that may write the state releases another run's lock."))
	case errors.Is(err, core.ErrNoWorkspace), errors.Is(err, stacksapi.ErrNotFound):
		problem.Write(w, problem.NotFound.New("No such stack or workspace."))
	case errors.Is(err, core.ErrWorkspaceName):
		problem.Write(w, problem.InvalidRequest.New(fmt.Sprintf(
			"A workspace name is UTF-8 without NUL, at most %d bytes.", core.MaxWorkspaceName)))
	default:
		problem.Failure(w, err)
	}
}
