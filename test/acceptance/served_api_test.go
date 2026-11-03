package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/server"
	"github.com/weaveplatform/infraweave/internal/server/idempotency"
	"github.com/weaveplatform/infraweave/internal/server/problem"
	"github.com/weaveplatform/infraweave/internal/server/ratelimit"
)

// servedSteps runs the server in-process on loopback ports the system
// chooses, so scenarios never contend for a fixed port.
type servedSteps struct {
	srv *server.Running
	// test serves a test-only operation behind the API listener's controls,
	// for behaviour no stubbed route can show: a POST that runs.
	test *httptest.Server
	runs atomic.Int32
	// sent holds every response of the scenario, the last one last.
	sent []response
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (s *servedSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a running infraweave server$`, s.running)
	sc.Step(
		`^a running infraweave server whose standard burst is (\d+) and expensive burst is (\d+)$`,
		s.runningWithBursts,
	)
	sc.Step(`^a running infraweave server that disables the caller "([^"]+)"$`, s.runningDisabling)
	sc.Step(`^an API listener serving a test-only "POST ([^"]+)" operation$`, s.testOperation)
	sc.Step(`^"([A-Z]+) ([^"]+)" on the admin listener answers (\d+)$`, s.adminAnswers)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" on the (admin|API) listener answers a (\d+) problem(?: "([a-z-]+)")?$`,
		s.answersProblem,
	)
	sc.Step(`^"([A-Z]+) ([^"]+)" is sent (\d+) times on the API listener$`, s.sendTimes)
	sc.Step(`^the response carries Retry-After and the rate-limit headers$`, s.rateLimitHeaders)
	sc.Step(`^the response carries Retry-After$`, s.retryAfter)
	sc.Step(
		`^"POST ([^"]+)" is sent with Idempotency-Key "([^"]+)" and body '([^']*)'$`,
		s.sendWithKey,
	)
	sc.Step(`^the operation has run (\d+) times?$`, s.ran)
	sc.Step(`^the last response replays the first with Idempotency-Replayed: true$`, s.replayed)
	sc.Step(`^the last response is a (\d+) problem "([a-z-]+)"$`, s.lastIsProblem)
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.srv != nil {
			err = errors.Join(err, s.srv.Shutdown(ctx))
			s.srv = nil
		}
		if s.test != nil {
			s.test.Close()
			s.test = nil
		}
		s.runs.Store(0)
		s.sent = nil
		return ctx, err
	})
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (s *servedSteps) start(ctx context.Context, c server.Config) error {
	c.Listen, c.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	srv, err := server.Start(ctx, c, discardLog())
	if err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	s.srv = srv
	return nil
}

func (s *servedSteps) running(ctx context.Context) error {
	return s.start(ctx, server.Defaults())
}

// runningWithBursts refills too slowly to matter while a scenario runs, so
// the bursts alone decide what is admitted.
func (s *servedSteps) runningWithBursts(ctx context.Context, standard, expensive int) error {
	c := server.Defaults()
	c.RateLimit.Standard = ratelimit.Limit{Rate: 1e-6, Burst: standard}
	c.RateLimit.Expensive = ratelimit.Limit{Rate: 1e-6, Burst: expensive}
	return s.start(ctx, c)
}

func (s *servedSteps) runningDisabling(ctx context.Context, caller string) error {
	c := server.Defaults()
	c.RateLimit.DisabledCallers = []string{caller}
	return s.start(ctx, c)
}

// testOperation composes the API listener's controls with the default
// settings around a handler that numbers each run, so a replay is told
// apart from a second run.
func (s *servedSteps) testOperation(path string) error {
	r := server.Guarded(discardLog(), server.Defaults(), server.Operations{
		http.MethodPost + " " + path: {Cost: ratelimit.Standard},
	})
	r.Post(path, func(w http.ResponseWriter, _ *http.Request) {
		n := s.runs.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", path+"/"+strconv.Itoa(int(n)))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]int32{"run": n})
	})
	s.test = httptest.NewServer(r)
	return nil
}

func (s *servedSteps) addr(listener string) string {
	switch {
	case listener == "admin":
		return s.srv.AdminAddr()
	case s.test != nil:
		return s.test.Listener.Addr().String()
	}
	return s.srv.APIAddr()
}

func (s *servedSteps) send(
	ctx context.Context,
	method, listener, path string,
	header http.Header,
	body string,
) (response, error) {
	req, err := http.NewRequestWithContext(
		ctx, method, "http://"+s.addr(listener)+path, strings.NewReader(body))
	if err != nil {
		return response{}, fmt.Errorf("build request: %w", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, fmt.Errorf("read body: %w", err)
	}
	r := response{status: resp.StatusCode, header: resp.Header, body: b}
	s.sent = append(s.sent, r)
	return r, nil
}

func (s *servedSteps) last() response {
	if len(s.sent) == 0 {
		return response{}
	}
	return s.sent[len(s.sent)-1]
}

func (s *servedSteps) adminAnswers(ctx context.Context, method, path, code string) error {
	resp, err := s.send(ctx, method, "admin", path, nil, "")
	if err != nil {
		return err
	}
	if strconv.Itoa(resp.status) != code {
		return fmt.Errorf(
			"%w: %s %s answered %d: %s",
			errStep,
			method,
			path,
			resp.status,
			resp.body,
		)
	}
	return nil
}

func (s *servedSteps) answersProblem(
	ctx context.Context,
	method, path, listener, code, slug string,
) error {
	resp, err := s.send(ctx, method, listener, path, nil, "")
	if err != nil {
		return err
	}
	return isProblem(resp, method+" "+path, code, slug)
}

// isProblem checks that resp is a problem with status code and, when slug
// is not empty, of that type.
func isProblem(resp response, what, code, slug string) error {
	if strconv.Itoa(resp.status) != code {
		return fmt.Errorf(
			"%w: %s answered %d, want %s: %s",
			errStep,
			what,
			resp.status,
			code,
			resp.body,
		)
	}
	if ct := resp.header.Get("Content-Type"); ct != problem.ContentType {
		return fmt.Errorf("%w: %s answered %s, not a problem", errStep, what, ct)
	}
	var p problem.Problem
	if err := json.Unmarshal(resp.body, &p); err != nil || p.Status != resp.status {
		return fmt.Errorf(
			"%w: %s body is not a problem with its status: %s",
			errStep,
			what,
			resp.body,
		)
	}
	if slug != "" && p.Type != problem.TypePrefix+slug {
		return fmt.Errorf("%w: %s answered type %q, want %q", errStep, what, p.Type, slug)
	}
	return nil
}

func (s *servedSteps) sendTimes(ctx context.Context, method, path string, n int) error {
	for i := range n {
		resp, err := s.send(ctx, method, "API", path, nil, "")
		if err != nil {
			return err
		}
		if resp.status == http.StatusTooManyRequests || resp.header.Get("X-RateLimit-Limit") == "" {
			return fmt.Errorf("%w: request %d of %d answered %d with %v",
				errStep, i+1, n, resp.status, resp.header)
		}
	}
	return nil
}

func (s *servedSteps) rateLimitHeaders() error {
	h := s.last().header
	for _, name := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if _, err := strconv.Atoi(h.Get(name)); err != nil {
			return fmt.Errorf(
				"%w: %s is %q, want whole seconds or tokens",
				errStep,
				name,
				h.Get(name),
			)
		}
	}
	if h.Get("X-RateLimit-Remaining") != "0" {
		return fmt.Errorf(
			"%w: X-RateLimit-Remaining %q on a 429",
			errStep,
			h.Get("X-RateLimit-Remaining"),
		)
	}
	return nil
}

func (s *servedSteps) retryAfter() error {
	if n, err := strconv.Atoi(s.last().header.Get("Retry-After")); err != nil || n < 1 {
		return fmt.Errorf("%w: Retry-After %q", errStep, s.last().header.Get("Retry-After"))
	}
	return nil
}

func (s *servedSteps) sendWithKey(ctx context.Context, path, key, body string) error {
	h := http.Header{}
	h.Set(idempotency.Header, key)
	h.Set("Content-Type", "application/json")
	_, err := s.send(ctx, http.MethodPost, "API", path, h, body)
	return err
}

func (s *servedSteps) ran(n int) error {
	if got := s.runs.Load(); int(got) != n {
		return fmt.Errorf("%w: the operation ran %d times, want %d", errStep, got, n)
	}
	return nil
}

func (s *servedSteps) replayed() error {
	if len(s.sent) < 2 {
		return fmt.Errorf("%w: %d responses, want two to compare", errStep, len(s.sent))
	}
	first, last := s.sent[0], s.last()
	switch {
	case first.header.Get(idempotency.ReplayedHeader) != "":
		return fmt.Errorf("%w: the first response is marked replayed", errStep)
	case last.header.Get(idempotency.ReplayedHeader) != "true":
		return fmt.Errorf("%w: the repeat is not marked replayed: %v", errStep, last.header)
	case last.status != first.status || !bytes.Equal(last.body, first.body) ||
		last.header.Get("Location") != first.header.Get("Location") ||
		last.header.Get("Content-Type") != first.header.Get("Content-Type"):
		return fmt.Errorf("%w: the repeat %d %s differs from the first %d %s",
			errStep, last.status, last.body, first.status, first.body)
	}
	return nil
}

func (s *servedSteps) lastIsProblem(code, slug string) error {
	return isProblem(s.last(), "the last request", code, slug)
}
