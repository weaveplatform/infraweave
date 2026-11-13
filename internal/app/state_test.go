package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	stateassembly "github.com/weaveplatform/infraweave/internal/state/assembly"
)

// stateChallenge is the challenge basicRuns answers a refused request
// with, the one the state authenticator sends.
const stateChallenge = `Basic realm="infraweave-state"`

// basicRuns admits HTTP Basic with user run and a run's ID as the
// password, as that run, on the state access only, as the state
// authenticator does with a run token.
type basicRuns struct{}

func (basicRuns) Middleware(
	access func(*http.Request) accessapi.Access,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			run, err := kernel.ParseULID(pass)
			if access(r) != accessapi.State || !ok || user != "run" || err != nil {
				w.Header().Set("WWW-Authenticate", stateChallenge)
				problem.Write(w, problem.Unauthenticated.New("no valid credential"))
				return
			}
			next.ServeHTTP(w, r.WithContext(kernel.WithPrincipal(r.Context(),
				kernel.Principal{Kind: kernel.PrincipalRunner, ActorID: run})))
		})
	}
}

// stateWorld is one stack, slug one, whose state infraweave keeps, and
// the runs domain's grant of read alone to every run on it but panics,
// whose access read panics.
type stateWorld struct {
	stacksapi.Stacks
	acct   kernel.AccountID
	stack  kernel.ULID
	panics kernel.ULID
}

func (s stateWorld) BySlug(
	_ context.Context,
	acct kernel.AccountID,
	slug string,
) (stacksapi.Stack, error) {
	if acct != s.acct || slug != "one" {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return stacksapi.Stack{ID: s.stack, Account: s.acct, Slug: slug}, nil
}

func (s stateWorld) Settings(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	if acct != s.acct || id != s.stack {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	return stacksapi.Settings{ManagesState: true}, nil
}

func (s stateWorld) Access(_ context.Context, run, stack kernel.ULID) (runsapi.StateGrant, error) {
	if run == s.panics {
		panic("the access read panicked")
	}
	return runsapi.StateGrant{Account: s.acct, Read: stack == s.stack}, nil
}

// TestUnitStateSurface checks the API's controls on /state/v1 through
// the served handler: its own authenticator, whichever credential the
// API's admits; a request ID and both uncacheable marks on every answer;
// a panic's 500; the run's own rate-limit bucket; a LockInfo bound of its
// own; no idempotency keys; and problem documents for a method, a stack
// or a grant the backend refuses.
func TestUnitStateSurface(t *testing.T) {
	c := config.Defaults().Server
	c.RateLimit.Standard = ratelimit.Limit{Rate: 1e-6, Burst: 2}
	w := stateWorld{acct: kernel.NewAccountID(), stack: kernel.NewULID(), panics: kernel.NewULID()}
	h := API(discard(), c, memory(c), Domains{
		Auth:      byAddress{},
		StateAuth: basicRuns{},
		State:     stateassembly.New(stateassembly.Deps{Stacks: w, Access: w}),
	}, Platform{}, Blobs{})
	stack := stateapi.BaseURL + "/stacks/one"
	send := func(method, target, auth, body string, header ...string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Header().Get(problem.RequestIDHeader) == "" {
			t.Errorf("%s %s answered no request ID", method, target)
		}
		wantUncacheable(t, method+" "+target, rec)
		return rec
	}
	basic := func(run kernel.ULID) string {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.SetBasicAuth("run", run.String())
		return r.Header.Get("Authorization")
	}

	for _, auth := range []string{"", "Bearer an-api-token", "Basic bm90LWEtcnVu"} {
		rec := send(http.MethodGet, stack, auth, "")
		wantProblem(t, rec, http.StatusUnauthorized, "no valid credential")
		if got := rec.Header().Get("WWW-Authenticate"); got != stateChallenge {
			t.Errorf("%q: WWW-Authenticate %q", auth, got)
		}
	}

	run := basic(kernel.NewULID())
	rec := send(http.MethodGet, stack, run, "")
	wantProblem(t, rec, http.StatusNotImplemented, "does not serve state versions")
	if rec.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("the run drew from no bucket")
	}
	rec = send(http.MethodPost, stack, run, "{}", idempotency.Header, "k1")
	wantProblem(t, rec, http.StatusForbidden, "may not write")
	if rec.Header().Get(idempotency.ReplayedHeader) != "" {
		t.Error("a state request was kept under its idempotency key")
	}
	wantProblem(t, send(http.MethodGet, stack, run, ""), http.StatusTooManyRequests, "")

	wantProblem(t, send(http.MethodGet, stack, basic(w.panics), ""),
		http.StatusInternalServerError, "")
	wantProblem(t, send(stateapi.MethodLock, stack, basic(kernel.NewULID()), `{"ID":"l"}`),
		http.StatusForbidden, "may not lock")
	wantProblem(t, send(stateapi.MethodLock, stack, basic(kernel.NewULID()),
		`{"ID":"`+strings.Repeat("a", 16<<10)+`"}`),
		http.StatusRequestEntityTooLarge, "16384 bytes")
	wantProblem(
		t,
		send(http.MethodGet, stateapi.BaseURL+"/stacks/two", basic(kernel.NewULID()), ""),
		http.StatusNotFound,
		"two",
	)
	wantProblem(t, send(http.MethodDelete, stack, basic(kernel.NewULID()), ""),
		http.StatusMethodNotAllowed, "")
	wantProblem(t, send(http.MethodGet, stateapi.BaseURL+"/nope", basic(kernel.NewULID()), ""),
		http.StatusNotFound, "")

	// A route under /state/v1 that the route table lacks is refused, as
	// every surface's are.
	r := Guarded(discard(), c, apiOperations(), memory(c), byPrefix{state: basicRuns{}}, nil, nil)
	r.Put(stateapi.StackPattern, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h = r
	wantProblem(t, send(http.MethodPut, stack, basic(kernel.NewULID()), ""),
		http.StatusForbidden, "")

	// An API built without the state domain answers 501 on its routes.
	h = API(discard(), c, memory(c), Domains{StateAuth: basicRuns{}}, Platform{}, Blobs{})
	wantProblem(t, send(stateapi.MethodLock, stack, basic(kernel.NewULID()), ""),
		http.StatusNotImplemented, "No state domain")
}
