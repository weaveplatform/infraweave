package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/runlog"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// issuer is a fake worker API that reissues federation tokens living ttl
// from the bubble's clock, each named by its count, recording when each
// call came; it fails the calls fail names, counting from 1.
type issuer struct {
	mu    sync.Mutex
	ttl   time.Duration
	fail  map[int]error
	calls []time.Time
	asked []string
}

func (i *issuer) OIDCToken(_ context.Context, token, run string) (wire.OIDCToken, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls = append(i.calls, time.Now())
	i.asked = append(i.asked, token+" "+run)
	if err := i.fail[len(i.calls)]; err != nil {
		return wire.OIDCToken{}, err
	}
	return wire.OIDCToken{
		Token:     "eyJ.refreshed-" + strconv.Itoa(len(i.calls)) + ".sig",
		ExpiresAt: time.Now().Add(i.ttl).Unix(),
	}, nil
}

// since is when each call came, after start.
func (i *issuer) since(start time.Time) []time.Duration {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]time.Duration, 0, len(i.calls))
	for _, c := range i.calls {
		out = append(out, c.Sub(start))
	}
	return out
}

// lockedLog is the refresher's slog output.
type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p) //nolint:wrapcheck // a test double
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// replaced records the tokens a refresher replaces the file with, or
// fails the replaces fail names, counting from 1.
type replaced struct {
	mu     sync.Mutex
	fail   map[int]error
	calls  int
	tokens []string
}

func (r *replaced) replace(raw string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if err := r.fail[r.calls]; err != nil {
		return err
	}
	r.tokens = append(r.tokens, raw)
	return nil
}

func (r *replaced) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tokens...)
}

// newRefresher is a refresher of run "run" with token "run-token" over
// api, replacing through rep, logging to log, on the bubble's clock.
func newRefresher(api *issuer, rep *replaced, log *lockedLog) *refresher {
	return &refresher{
		api: api, now: time.Now, log: slog.New(slog.NewTextHandler(log, nil)),
		run: "run", token: "run-token", replace: rep.replace,
	}
}

// TestUnitRefreshIn checks that a token is refreshed fifteen minutes
// before it expires, and one that lives no longer than that halfway to
// its expiry, at once when it has expired.
func TestUnitRefreshIn(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for left, want := range map[time.Duration]time.Duration{
		time.Hour:        45 * time.Minute,
		20 * time.Minute: 5 * time.Minute,
		15 * time.Minute: 7*time.Minute + 30*time.Second,
		2 * time.Second:  time.Second,
		0:                0,
		-time.Minute:     0,
	} {
		if got := refreshIn(now.Add(left), now); got != want {
			t.Errorf("a token %v from its expiry is refreshed in %v, want %v", left, got, want)
		}
	}
}

// TestUnitRefresherTimer checks, on the bubble's clock, that a token of
// twenty minutes is refreshed five minutes in, with the run's token for
// the run, and its successor five minutes after it is issued, each
// replacing the file, and that the refresh ends when its context does.
func TestUnitRefresherTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, rep, log := &issuer{ttl: 20 * time.Minute}, &replaced{}, &lockedLog{}
		f := newRefresher(api, rep, log)
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.keep(ctx, start.Add(20*time.Minute))
		}()
		time.Sleep(5*time.Minute - time.Second) //nolint:forbidigo // advances only the synctest virtual clock
		synctest.Wait()
		if n := len(api.since(start)); n != 0 {
			t.Fatalf("%d calls before the refresh is due", n)
		}
		time.Sleep(6 * time.Minute) //nolint:forbidigo // advances only the synctest virtual clock
		synctest.Wait()
		if got := api.since(start); len(got) != 2 || got[0] != 5*time.Minute || got[1] != 10*time.Minute {
			t.Errorf("calls at %v, want 5m and 10m", got)
		}
		if got := rep.got(); len(got) != 2 || got[0] != "eyJ.refreshed-1.sig" || got[1] != "eyJ.refreshed-2.sig" {
			t.Errorf("replaced with %v", got)
		}
		if api.asked[0] != "run-token run" {
			t.Errorf("asked with %q", api.asked[0])
		}
		cancel()
		<-done
		if strings.Contains(log.String(), "refreshed-") {
			t.Errorf("the log holds a token: %s", log.String())
		}
	})
}

// TestUnitRefresherRetries checks that failed calls and a failed replace
// are retried from a second, doubling, logged as warnings that name
// neither token, and that the first success replaces the file and sets
// the next refresh from the new token's expiry.
func TestUnitRefresherRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		down := &client.Error{Op: "reissue the federation token", Status: 503}
		api := &issuer{ttl: 20 * time.Minute, fail: map[int]error{1: down, 2: down}}
		rep := &replaced{fail: map[int]error{1: errors.New("disk full")}}
		log := &lockedLog{}
		f := newRefresher(api, rep, log)
		start := time.Now()
		next, ok := f.renew(t.Context(), start.Add(15*time.Minute))
		// The calls came at once, after a second, after two more for the
		// replace refused, and after four.
		if got := api.since(start); !ok || len(got) != 4 || got[1] != time.Second || got[2] != 3*time.Second ||
			got[3] != 7*time.Second {
			t.Errorf("renew = %v, %t; calls at %v", next, ok, got)
		}
		if want := start.Add(7*time.Second + 20*time.Minute).Truncate(time.Second); !next.Equal(want) {
			t.Errorf("next expiry %v, want %v", next, want)
		}
		if got := rep.got(); len(got) != 1 || got[0] != "eyJ.refreshed-4.sig" {
			t.Errorf("replaced with %v", got)
		}
		out := log.String()
		if n := strings.Count(out, "level=WARN msg=\"federation token not refreshed\""); n != 3 ||
			strings.Contains(out, "refreshed-") || strings.Contains(out, "run-token") {
			t.Errorf("%d warnings: %s", n, out)
		}
		if !strings.Contains(out, "disk full") || !strings.Contains(out, "503") {
			t.Errorf("the warnings name no failure: %s", out)
		}
	})
}

// TestUnitRefresherGivesUp checks that a refresh that keeps failing
// stops retrying once the held token would expire before the next try,
// with a warning, and ends, leaving the file as it was; and that one
// whose context ends stops at once.
func TestUnitRefresherGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		down := &client.Error{Op: "reissue the federation token", Status: 403, Type: "feature-not-in-tier"}
		fail := map[int]error{}
		for i := 1; i < 100; i++ {
			fail[i] = down
		}
		api, rep, log := &issuer{ttl: 20 * time.Minute, fail: fail}, &replaced{}, &lockedLog{}
		f := newRefresher(api, rep, log)
		start := time.Now()
		f.keep(t.Context(), start.Add(20*time.Minute))
		calls := api.since(start)
		last := calls[len(calls)-1]
		if calls[0] != 5*time.Minute || last >= 20*time.Minute || last < 19*time.Minute ||
			len(rep.got()) != 0 {
			t.Errorf("calls at %v, replaced %v", calls, rep.got())
		}
		if !strings.Contains(log.String(), "federation token not refreshed before it expires") {
			t.Errorf("no final warning: %s", log.String())
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		api.fail = map[int]error{len(api.since(start)) + 1: down}
		if _, ok := f.renew(ctx, time.Now().Add(time.Hour)); ok {
			t.Error("a renew after its context ended succeeded")
		}
		ctx, cancel = context.WithCancel(t.Context())
		api.fail = map[int]error{len(api.since(start)) + 1: down}
		go func() {
			time.Sleep(time.Millisecond) //nolint:forbidigo // advances only the synctest virtual clock
			cancel()
		}()
		if _, ok := f.renew(ctx, time.Now().Add(time.Hour)); ok {
			t.Error("a renew whose context ended while it waited succeeded")
		}
	})
}

// TestUnitReplaceOIDCTokenFails checks that a refreshed token that
// cannot be put at the token's file, because the workspace is gone, its
// temporary name is a directory or the file's name is one, answers an
// error naming the step and leaves the file as it was, yet the token is
// masked and forbidden in the saved workspace all the same.
func TestUnitReplaceOIDCTokenFails(t *testing.T) {
	for name, tc := range map[string]struct {
		block string
		want  string
	}{
		"no workspace":          {"", "open the workspace"},
		"a directory as .next":  {workspace.OIDCTokenNext, "write the refreshed federation token"},
		"a directory as a file": {workspace.OIDCTokenFile, "replace the federation token"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ws := filepath.Join(dir, "workspace")
			if tc.block != "" {
				if err := os.MkdirAll(filepath.Join(ws, tc.block, "held"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			log, err := runlog.New(t.Context(), runlog.Deps{Out: io.Discard, Log: slog.New(slog.DiscardHandler)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = log.Close(context.Background()) })
			r := &run{d: Deps{Workspace: ws}, log: log}
			if err := r.replaceOIDCToken("eyJ.refreshed.sig"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("replace = %v, want %q", err, tc.want)
			}
			if got := r.oidcTokens(); len(got) != 1 || string(got[0]) != "eyJ.refreshed.sig" {
				t.Errorf("forbidden tokens %q", got)
			}
		})
	}
}
