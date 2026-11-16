package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/moving"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// State is what the backend asks of the state domain's core.
type State interface {
	Lock(ctx context.Context, t core.Target, want core.Lock) error
	Unlock(ctx context.Context, u core.Unlock) error
	Write(ctx context.Context, w core.Write) (bool, error)
	Read(ctx context.Context, t core.Target) (core.Version, io.ReadCloser, bool, error)
}

// Stacks is what the backend reads of the stacks domain; stacksapi.Stacks
// is one.
type Stacks interface {
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error)
	BySlug(ctx context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error)
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error)
}

// Handler serves the state backend's routes for the principal the state
// authenticator has put on each request's context: a run's, from its run
// token or in-run token, or an API key's or token's.
type Handler struct {
	access  runsapi.StateAccess
	reader  accessapi.StateReader
	stacks  Stacks
	state   State
	maxSize int64
}

// New returns the backend over the runs domain's grants to runs, the
// access domain's decisions on every other read, the stacks it reads and
// the core, writing versions of at most maxSize bytes.
func New(access runsapi.StateAccess, reader accessapi.StateReader, stacks Stacks, state State, maxSize int64) *Handler {
	return &Handler{access: access, reader: reader, stacks: stacks, state: state, maxSize: maxSize}
}

// Routes adds GET, POST, LOCK and UNLOCK on stateapi.StackPattern and
// stateapi.WorkspacePattern to r, registering LOCK and UNLOCK with chi
// first. LOCK and UNLOCK bound their bodies to core.MaxLockInfo with the
// API's own body bound, so an oversized LockInfo answers the same 413;
// POST bounds its body to the handler's maxSize. Any other method on them
// answers 405 through r's own handler.
func (h *Handler) Routes(r chi.Router) { mount(r, h.read, h.write, h.lock, h.unlock) }

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

// read answers the workspace's current state, streamed and unsealed with
// no Content-MD5, from which the client computes its own; 204 when the
// workspace has none, which Terraform reads as no state. Each write of
// the answer moves its connection's deadlines.
//
// ref: decision 0010 § "Reading"
func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	t, g, _, ok := h.target(w, r)
	if !ok || !may(w, r, g.Read) {
		return
	}
	v, body, ok, err := h.state.Read(r.Context(), t)
	switch {
	case err != nil:
		answer(w, err)
		return
	case !ok:
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.FormatInt(v.Size, 10))
	d := moving.Start(w, moving.Stall, false)
	// A copy that fails cuts the answer short of its Content-Length, which
	// the client reads as a failed transfer.
	_, _ = io.Copy(d.Writer(w), body)
}

// write stores r's body as a new version of the workspace under the
// exclusive lock its ID query parameter names. The body must declare its
// length, at most the handler's maxSize, since the store takes an
// object's size before it streams. Each read of the body moves its
// connection's deadlines.
//
// ref: decision 0010 § "Writing a version"
func (h *Handler) write(w http.ResponseWriter, r *http.Request) {
	t, g, run, ok := h.target(w, r)
	if !ok || !may(w, r, g.Write) {
		return
	}
	switch {
	case r.ContentLength < 0:
		problem.Write(w, problem.InvalidRequest.New("A state write declares its Content-Length."))
		return
	case r.ContentLength > h.maxSize:
		problem.Write(w, problem.TooLarge(h.maxSize))
		return
	}
	body := &failed{ReadCloser: moving.Start(w, moving.Stall, true).Reader(r.Body)}
	_, err := h.state.Write(r.Context(), core.Write{
		Target: t, Run: run, LockID: r.URL.Query().Get("ID"), Body: body,
		Size: r.ContentLength, ContentMD5: r.Header.Get("Content-MD5"),
	})
	if err != nil && body.err != nil {
		problem.BodyError(w, body.err)
		return
	}
	answer(w, err)
}

// failed is a body that keeps the first error a read of it met other than
// its end, which explains a write that fails while the body streams.
type failed struct {
	io.ReadCloser
	err error
}

func (f *failed) Read(p []byte) (int, error) {
	n, err := f.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && f.err == nil {
		f.err = err
	}
	return n, err //nolint:wrapcheck // the body's own error, as io.Reader returns it
}

var verbs = map[string]string{
	http.MethodGet: "read", http.MethodPost: "write",
	stateapi.MethodLock: "lock", stateapi.MethodUnlock: "unlock",
}

// lock takes the lock r's LockInfo names, shared or exclusive as the
// run's grant says.
func (h *Handler) lock(w http.ResponseWriter, r *http.Request) {
	t, g, run, ok := h.target(w, r)
	if !ok || !may(w, r, g.Lock != runsapi.StateLockNone) {
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
	answer(w, h.state.Lock(r.Context(), t, core.Lock{ID: id, Mode: mode, Run: run, Info: info}))
}

// unlock releases the lock r's LockInfo names, or with an empty body
// every lock on the workspace.
func (h *Handler) unlock(w http.ResponseWriter, r *http.Request) {
	t, g, run, ok := h.target(w, r)
	if !ok || !may(w, r, g.Lock != runsapi.StateLockNone) {
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
	answer(w, h.state.Unlock(r.Context(), core.Unlock{
		Target: t, Run: run, ID: id, Break: g.Lock == runsapi.StateLockExclusive,
		RemoteIP: ratelimit.ClientAddr(r),
	}))
}

// may answers 403 unless the run's grant allows r's method.
func may(w http.ResponseWriter, r *http.Request, allowed bool) bool {
	if !allowed {
		problem.Write(w, problem.Forbidden.New("This run may not "+verbs[r.Method]+
			" the stack's state in its current state."))
		return false
	}
	return true
}

// target is the workspace r names, what its caller may do there, and the
// run it acts for. A run token on its own stack holds what the runs
// domain grants the run's state and acts for its run; any other caller,
// as external decides, may at most read, and acts for no run. A stack
// the caller's account does not have, one whose state infraweave does not
// keep, and a run no account has answer 404, so a caller learns nothing
// of another account's stacks.
//
// ref: decision 0010 § "Who may call what"
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (core.Target, runsapi.StateGrant, kernel.ULID, bool) {
	ctx := r.Context()
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		problem.Write(w, problem.Unauthenticated.New("A credential is required."))
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	seg, okStack := param(r, "stack")
	ws, okWorkspace := param(r, "workspace")
	if !okStack || !okWorkspace {
		problem.Write(w, problem.NotFound.New("No such stack or workspace."))
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	g, id, err := h.grant(ctx, p, seg)
	var st stacksapi.Settings
	if err == nil {
		if st, err = h.stacks.Settings(ctx, g.Account, id); err == nil && !st.ManagesState {
			err = stacksapi.ErrNotFound
		}
	}
	if !found(w, err, seg) {
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	t := core.Target{Account: g.Account, Stack: id, Workspace: ws}
	if p.Kind == kernel.PrincipalRunner && id == g.Stack {
		return t, g, p.ActorID, true
	}
	if !h.external(w, r, p, g, st) {
		return core.Target{}, runsapi.StateGrant{}, kernel.ULID{}, false
	}
	return t, runsapi.StateGrant{Account: g.Account, Read: true}, kernel.ULID{}, true
}

// found answers err, a stack's lookup by seg: 404 for one the caller's
// account does not have or whose state infraweave does not keep, and the
// failure of any other; true when there is none.
func found(w http.ResponseWriter, err error, seg string) bool {
	switch {
	case errors.Is(err, stacksapi.ErrNotFound):
		problem.Write(w, problem.NotFound.New("No stack whose state infraweave keeps is named "+
			seg+"."))
		return false
	case err != nil:
		problem.Failure(w, err)
		return false
	}
	return true
}

// isRun reports whether p acts for a run: through its run token, or its
// in-run token, whose ActorID is the run too.
func isRun(p kernel.Principal) bool {
	return p.Kind == kernel.PrincipalRunner || p.Kind == kernel.PrincipalRun
}

// grant is the grant on the stack seg names of the run p acts for, or
// for any other caller one naming p's account alone, and the stack's ID:
// its canonical ULID, or else its slug in that account.
// stacksapi.ErrNotFound when no account has the run, or the account no
// stack of that slug.
func (h *Handler) grant(ctx context.Context, p kernel.Principal, seg string) (runsapi.StateGrant, kernel.ULID, error) {
	g := runsapi.StateGrant{Account: p.AccountID}
	id, err := kernel.ParseULID(seg)
	if err != nil || id.String() != seg {
		if isRun(p) {
			if g, err = h.access.Access(ctx, p.ActorID, kernel.ULID{}); err != nil || g.Account.IsZero() {
				return runsapi.StateGrant{}, kernel.ULID{}, orNotFound(err)
			}
		}
		st, err := h.stacks.BySlug(ctx, g.Account, seg)
		if err != nil {
			return runsapi.StateGrant{}, kernel.ULID{}, err //nolint:wrapcheck // the stacks domain names it
		}
		id = st.ID
	}
	if !isRun(p) {
		return g, id, nil
	}
	g, err = h.access.Access(ctx, p.ActorID, id)
	if err != nil || g.Account.IsZero() {
		return runsapi.StateGrant{}, kernel.ULID{}, orNotFound(err)
	}
	return g, id, nil
}

// external decides a read of st's state by any caller but a run token on
// its own stack, and answers its refusal, in order: 404 when the caller
// cannot see the stack; then 403 for an in-run token on its own stack,
// for any method but GET, for a stack with external access off, for a
// caller lacking STACK_STATE_READ or STACK_STATE_DOWNLOAD on the stack's
// space, and for a run whose state allows it no read. A run, by either
// of its tokens, decides as its stack, g's, through the stack's bindings
// and its read of its home space.
//
// ref: decision 0010 § "Who may call what"
func (h *Handler) external(w http.ResponseWriter, r *http.Request, p kernel.Principal, g runsapi.StateGrant, st stacksapi.Settings) bool {
	ctx := r.Context()
	run := isRun(p)
	if p.Kind == kernel.PrincipalRunner {
		own, err := h.stacks.Get(ctx, g.Account, g.Stack)
		if !found(w, err, g.Stack.String()) {
			return false
		}
		p = kernel.Principal{
			Kind: kernel.PrincipalRun, AccountID: g.Account, ActorID: p.ActorID,
			StackID: g.Stack, StackSpaceID: own.Space,
		}
	}
	missing, err := h.reader.StateRead(ctx, p, st.ID, st.Space)
	if err != nil {
		problem.Failure(w, err)
		return false
	}
	var refusal problem.Problem
	switch {
	case missing == accessapi.ActionSpaceRead:
		return found(w, stacksapi.ErrNotFound, chi.URLParam(r, "stack"))
	case run && st.ID == g.Stack:
		refusal = problem.Forbidden.New("An in-run token does not reach its own stack's state; " +
			"the run token does.")
	case r.Method != http.MethodGet:
		refusal = problem.Forbidden.New("Only the stack's own runs may " + verbs[r.Method] +
			" its state; any other caller may only read it.")
	case !st.ExternalStateAccess:
		refusal = problem.Forbidden.New("The stack's external state access is off.")
	case missing != "":
		refusal = problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", missing))
		refusal.Action = string(missing)
	case run && !g.Read:
		return may(w, r, false)
	default:
		return true
	}
	problem.Write(w, refusal)
	return false
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

// answer writes the outcome of a lock, an unlock or a write: 200, 423
// with the holder's LockInfo, which Terraform reports as the lock's
// holder, or {} for a write when no lock is held, or a problem.
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
	case errors.Is(err, core.ErrMD5):
		problem.Write(w, problem.InvalidRequest.New("The body does not match its Content-MD5."))
	case errors.Is(err, core.ErrState):
		problem.Write(w, problem.InvalidRequest.New("The body is not a state: a JSON object "+
			"with version, serial and lineage, or a client-encrypted state with neither "+
			"serial nor lineage."))
	case errors.Is(err, core.ErrWorkspaceName):
		problem.Write(w, problem.InvalidRequest.New(fmt.Sprintf(
			"A workspace name is UTF-8 without NUL, at most %d bytes.", core.MaxWorkspaceName)))
	default:
		problem.Failure(w, err)
	}
}
