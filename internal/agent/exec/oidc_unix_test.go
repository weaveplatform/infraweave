//go:build unix

package exec_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// oidcToken is a federation token's stand-in, long enough that masking
// and the archive's scan match it alone.
const oidcToken = "eyJhbGciOiJSUzI1NiJ9.eyJzY29wZSI6InJlYWQifQ.c2lnbmF0dXJl"

// TestUnitExecOIDCToken checks that a handoff's federation token reaches
// the phase as INFRAWEAVE_OIDC_TOKEN, after a stack variable of that name,
// and at /mnt/workspace/infraweave.oidc, read-only to the runner's user,
// before the first hook, replacing a mounted file there; that the log
// masks it; and that the saved workspace leaves its file out.
func TestUnitExecOIDCToken(t *testing.T) {
	w := newWorld(t)
	token, mine, stale := oidcToken, "mine", []byte("stale")
	w.h.Secrets.OidcToken = &token
	w.h.Spec.Environment = append(w.h.Spec.Environment, wire.RunSpecVariable{Name: "INFRAWEAVE_OIDC_TOKEN", Value: &mine})
	w.h.Spec.Files = append(w.h.Spec.Files, wire.RunSpecFile{Path: workspace.OIDCTokenFile, Content: &stale})
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {
			`[ "$(cat /dev/null "$OIDC_FILE")" = "$INFRAWEAVE_OIDC_TOKEN" ] && echo "same token"`,
			`echo "token $INFRAWEAVE_OIDC_TOKEN"`,
			`echo "mode $(stat -c %a "$OIDC_FILE" 2>/dev/null || stat -f %Lp "$OIDC_FILE")"`,
			`( echo again > "$OIDC_FILE" ) 2>/dev/null || echo "read-only"`,
		},
	}
	w.environ = append(w.environ, "OIDC_FILE="+filepath.Join(w.workspace, workspace.OIDCTokenFile))
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	out := w.out.String()
	for _, want := range []string{"same token\n", "token *****\n", "mode 400\n", "read-only\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, oidcToken) {
		t.Errorf("the log holds the token: %s", out)
	}
	if b, err := os.ReadFile(filepath.Join(w.workspace, workspace.OIDCTokenFile)); err != nil || string(b) != oidcToken {
		t.Errorf("the token's file = %q, %v", b, err)
	}
	saved := w.w.recorded[exec.WorkspaceName]
	dir := filepath.Join(t.TempDir(), "restored")
	if err := workspace.Read(bytes.NewReader(saved), dir, w.pool, runID, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, workspace.OIDCTokenFile)); !os.IsNotExist(err) {
		t.Errorf("the saved workspace holds the token's file: %v", err)
	}
}

// TestUnitExecNoOIDCToken checks that a handoff without a federation token
// gives the phase neither the variable nor the file.
func TestUnitExecNoOIDCToken(t *testing.T) {
	w := newWorld(t)
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {
			`echo "variable ${INFRAWEAVE_OIDC_TOKEN-unset}"`,
			`[ -e "$OIDC_FILE" ] || echo "no file"`,
		},
	}
	w.environ = append(w.environ, "OIDC_FILE="+filepath.Join(w.workspace, workspace.OIDCTokenFile))
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if out := w.out.String(); !strings.Contains(out, "variable unset\n") || !strings.Contains(out, "no file\n") {
		t.Errorf("output: %s", out)
	}
}

// TestUnitExecOIDCTokenSaveFails checks that planning fails, naming the
// file, when a hook copies the federation token into the workspace, and
// that a token's file the runner cannot write fails the first phase.
func TestUnitExecOIDCTokenSaveFails(t *testing.T) {
	w := newWorld(t)
	token := oidcToken
	w.h.Secrets.OidcToken = &token
	w.h.Spec.Hooks = map[string][]string{
		"after_plan": {`echo "$INFRAWEAVE_OIDC_TOKEN" > leak.txt`},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	events := w.events()
	if last := events[len(events)-1]; !strings.HasPrefix(last, "PLANNING failed 5: ") ||
		!strings.Contains(last, "holds one of the run's credentials: the file source/leak.txt") {
		t.Errorf("the last report: %s", last)
	}

	w = newWorld(t)
	w.h.Secrets.OidcToken = &token
	if err := os.Mkdir(filepath.Join(w.workspace, workspace.OIDCTokenFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.workspace, workspace.OIDCTokenFile, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	events = w.events()
	if last := events[len(events)-1]; !strings.Contains(last, "failed") ||
		!strings.Contains(last, "The OIDC federation token cannot be written: ") {
		t.Errorf("the last report: %s", last)
	}
}

// federation is a fake worker API that reissues federation tokens of
// oidcToken's length, each living ttl, refusing the calls fail names,
// counting from 1, and keeping each token it issued.
type federation struct {
	mu     sync.Mutex
	ttl    time.Duration
	fail   map[int]error
	calls  int
	tokens []string
	// late receives each call that comes once ended is set.
	ended bool
	late  chan struct{}
}

func (f *federation) OIDCToken(_ context.Context, token, run string) (wire.OIDCToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.ended {
		select {
		case f.late <- struct{}{}:
		default:
		}
	}
	if token != "run-token" || run != runID {
		return wire.OIDCToken{}, &client.Error{Op: "reissue the federation token", Status: 401}
	}
	if err := f.fail[f.calls]; err != nil {
		return wire.OIDCToken{}, err
	}
	raw := fmt.Sprintf("%s%04d", oidcToken[:len(oidcToken)-4], f.calls)
	f.tokens = append(f.tokens, raw)
	return wire.OIDCToken{Token: raw, ExpiresAt: time.Now().Add(f.ttl).Unix()}, nil
}

func (f *federation) issued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tokens)
}

// awaitRefresh is a hook that waits, up to ten seconds, for the token's
// file to differ from INFRAWEAVE_OIDC_TOKEN, then reads it whole three
// hundred times while refreshes go on, printing any read of another
// length than the token's.
var awaitRefresh = []string{
	`i=0; while [ "$(cat "$OIDC_FILE")" = "$INFRAWEAVE_OIDC_TOKEN" ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done`,
	`[ "$(cat "$OIDC_FILE")" != "$INFRAWEAVE_OIDC_TOKEN" ] && echo "variable kept"`,
	`echo "refreshed $(cat "$OIDC_FILE")"`,
	`echo "mode $(stat -c %a "$OIDC_FILE" 2>/dev/null || stat -f %Lp "$OIDC_FILE")"`,
	`n=0; while [ $n -lt 300 ]; do t=$(cat "$OIDC_FILE"); [ ${#t} -eq ` + strconv.Itoa(len(oidcToken)) +
		` ] || echo "partial ${#t}"; n=$((n+1)); done`,
}

// refreshing is a world whose handoff's federation token is refreshed
// about a second into the first phase, fifteen minutes before it
// expires, and whose reissued tokens live two seconds, so each is
// refreshed a second after it is issued; fail's calls are refused.
func refreshing(t *testing.T, fail map[int]error) (*world, *federation) {
	t.Helper()
	w := newWorld(t)
	token, exp := oidcToken, time.Now().Add(15*time.Minute+time.Second).Unix()
	w.h.Secrets.OidcToken, w.h.Secrets.OidcTokenExpiresAt = &token, &exp
	f := &federation{ttl: 2 * time.Second, fail: fail}
	w.fed, w.logs = f, &lockedBuffer{}
	w.environ = append(w.environ, "OIDC_FILE="+filepath.Join(w.workspace, workspace.OIDCTokenFile))
	return w, f
}

// TestUnitExecOIDCTokenRefresh checks that a phase that outlives its
// federation token sees the token's file replaced with a reissued one,
// read-only, while INFRAWEAVE_OIDC_TOKEN keeps the first; that no read of
// the file finds a part of a token; that the log masks every reissued
// token; that a refused call leaves the file as it was, logs a warning
// that names no token, and the next call replaces it; and that the
// saved workspace leaves out the file and the one renamed over it.
func TestUnitExecOIDCTokenRefresh(t *testing.T) {
	w, f := refreshing(t, map[int]error{1: &client.Error{Op: "reissue the federation token", Status: 503}})
	w.h.Spec.Hooks = map[string][]string{"before_init": awaitRefresh}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	out := w.out.String()
	for _, want := range []string{"variable kept\n", "refreshed *****\n", "mode 400\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "partial") {
		t.Errorf("a read found a part of a token: %s", out)
	}
	issued := f.issued()
	if len(issued) == 0 {
		t.Fatalf("no token reissued: %s\n%v", w.logs.String(), w.events())
	}
	logs := w.logs.String()
	for _, tok := range append(issued, oidcToken) {
		if strings.Contains(out, tok) || strings.Contains(logs, tok) {
			t.Errorf("the output or the runner's log holds %s", tok)
		}
	}
	if !strings.Contains(logs, `msg="federation token not refreshed"`) || !strings.Contains(logs, "503") ||
		!strings.Contains(logs, `msg="federation token refreshed"`) {
		t.Errorf("the runner's log: %s", logs)
	}
	b, err := os.ReadFile(filepath.Join(w.workspace, workspace.OIDCTokenFile))
	if err != nil || !slices.Contains(issued, string(b)) {
		t.Errorf("the token's file = %q, %v; issued %v", b, err, issued)
	}
	if _, err := os.Stat(filepath.Join(w.workspace, workspace.OIDCTokenNext)); !os.IsNotExist(err) {
		t.Errorf("the refresh left %s: %v", workspace.OIDCTokenNext, err)
	}
	saved := w.w.recorded[exec.WorkspaceName]
	dir := filepath.Join(t.TempDir(), "restored")
	if err := workspace.Read(bytes.NewReader(saved), dir, w.pool, runID, 1<<20); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{workspace.OIDCTokenFile, workspace.OIDCTokenNext} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("the saved workspace holds %s: %v", name, err)
		}
	}
	// The refresh ends with the run, so no call comes in the 1.5 seconds
	// after it, longer than a refresh of a 2-second token waits.
	f.mu.Lock()
	f.ended, f.late = true, make(chan struct{}, 1)
	f.mu.Unlock()
	select {
	case <-f.late:
		t.Error("a token was asked for after the run ended")
	case <-time.After(1500 * time.Millisecond):
	}
}

// TestUnitExecOIDCTokenRefreshForbidden checks that a hook that copies a
// reissued token into the workspace fails the planning, naming the file,
// as one that copies the handoff's does.
func TestUnitExecOIDCTokenRefreshForbidden(t *testing.T) {
	w, _ := refreshing(t, nil)
	w.h.Spec.Hooks = map[string][]string{
		"before_init": awaitRefresh[:1],
		"after_plan":  {`cat "$OIDC_FILE" > leak.txt`},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	events := w.events()
	if last := events[len(events)-1]; !strings.HasPrefix(last, "PLANNING failed 5: ") ||
		!strings.Contains(last, "holds one of the run's credentials: the file source/leak.txt") {
		t.Errorf("the last report: %s", last)
	}
}

// TestUnitExecOIDCTokenNoRefresh checks that a handoff whose token names
// no expiry, or a runner with no Federation, refreshes nothing.
func TestUnitExecOIDCTokenNoRefresh(t *testing.T) {
	w, f := refreshing(t, nil)
	w.h.Secrets.OidcTokenExpiresAt = nil
	w.h.Spec.Hooks = map[string][]string{"before_init": {"sleep 1.5"}}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Errorf("%d calls without an expiry", f.calls)
	}
	w, f = refreshing(t, nil)
	w.fed = nil
	w.h.Spec.Hooks = map[string][]string{"before_init": {"sleep 1.5"}}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(w.workspace, workspace.OIDCTokenFile)); err != nil ||
		string(b) != oidcToken || f.calls != 0 {
		t.Errorf("without a Federation the file is %q, %v, after %d calls", b, err, f.calls)
	}
}
