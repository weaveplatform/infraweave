package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// servedSteps drive an infraweave serve subprocess of the scenario's world.
type servedSteps struct {
	w *harness.World
}

func (s *servedSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a running infraweave server$`, s.running)
	sc.Step(
		`^a running infraweave server whose standard burst is (\d+) and expensive burst is (\d+)$`,
		s.runningWithBursts,
	)
	sc.Step(`^a running infraweave server that disables the caller "([^"]+)"$`, s.runningDisabling)
	sc.Step(`^"([A-Z]+) ([^"]+)" on the admin listener answers (\d+)$`, s.adminAnswers)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" on the (admin|API) listener answers a (\d+) problem(?: "([a-z-]+)")?$`,
		s.answersProblem,
	)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" without a credential answers a (\d+) problem(?: "([a-z-]+)")?$`,
		s.anonymousProblem,
	)
	sc.Step(`^the response challenges for a bearer token$`, s.challenges)
	sc.Step(`^"([A-Z]+) ([^"]+)" is sent (\d+) times on the API listener$`, s.sendTimes)
	sc.Step(`^the response carries Retry-After and the rate-limit headers$`, s.rateLimitHeaders)
	sc.Step(`^the response carries Retry-After$`, s.retryAfter)
	sc.Step(`^the problem names the request in its instance$`, s.namesRequest)
	s.registerReplicas(sc)
}

func (s *servedSteps) running(ctx context.Context) error {
	_, err := s.w.StartServer(ctx, nil)
	return err
}

// runningWithBursts refills too slowly to matter while a scenario runs, so
// the bursts alone decide what is admitted.
func (s *servedSteps) runningWithBursts(ctx context.Context, standard, expensive int) error {
	return s.w.Start(ctx, map[string]any{
		"standard":  map[string]any{"rate": 1e-6, "burst": standard},
		"expensive": map[string]any{"rate": 1e-6, "burst": expensive},
	})
}

func (s *servedSteps) runningDisabling(ctx context.Context, caller string) error {
	return s.w.Start(ctx, map[string]any{"disabled_callers": []string{caller}})
}

func (s *servedSteps) adminAnswers(ctx context.Context, method, path, code string) error {
	resp, err := s.w.Send(ctx, method, "admin", path)
	if err != nil {
		return err
	}
	if strconv.Itoa(resp.Status) != code {
		return fmt.Errorf(
			"%w: %s %s answered %d: %s",
			harness.ErrStep,
			method,
			path,
			resp.Status,
			resp.Body,
		)
	}
	return nil
}

func (s *servedSteps) answersProblem(
	ctx context.Context,
	method, path, listener, code, slug string,
) error {
	resp, err := s.w.Send(ctx, method, listener, path)
	if err != nil {
		return err
	}
	return harness.IsProblem(resp, method+" "+path, code, slug)
}

// anonymousProblem sends a request with no credential to the first
// server's API listener.
func (s *servedSteps) anonymousProblem(ctx context.Context, method, path, code, slug string) error {
	c, err := sdk.New("http://"+s.w.Servers[0].API, sdk.WithRetry(sdk.NoRetry))
	if err != nil {
		return fmt.Errorf("sdk: %w", err)
	}
	resp, err := c.Do(ctx, method, path, nil)
	if resp == nil {
		return err
	}
	r := harness.Response{Status: resp.Status, Header: resp.Header, Body: resp.Body, Err: err}
	s.w.Sent = append(s.w.Sent, r)
	return harness.IsProblem(r, method+" "+path, code, slug)
}

// challenges checks the last answer's WWW-Authenticate (RFC 6750).
func (s *servedSteps) challenges() error {
	if got := s.w.Last().Header.Get("WWW-Authenticate"); got != "Bearer" {
		return fmt.Errorf("%w: WWW-Authenticate %q, want Bearer", harness.ErrStep, got)
	}
	return nil
}

func (s *servedSteps) sendTimes(ctx context.Context, method, path string, n int) error {
	for i := range n {
		resp, err := s.w.Send(ctx, method, "API", path)
		if err != nil {
			return err
		}
		if resp.Status == http.StatusTooManyRequests || resp.Header.Get("X-RateLimit-Limit") == "" {
			return fmt.Errorf("%w: request %d of %d answered %d with %v",
				harness.ErrStep, i+1, n, resp.Status, resp.Header)
		}
	}
	return nil
}

func (s *servedSteps) rateLimitHeaders() error {
	h := s.w.Last().Header
	for _, name := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if _, err := strconv.Atoi(h.Get(name)); err != nil {
			return fmt.Errorf(
				"%w: %s is %q, want whole seconds or tokens",
				harness.ErrStep,
				name,
				h.Get(name),
			)
		}
	}
	if h.Get("X-RateLimit-Remaining") != "0" {
		return fmt.Errorf(
			"%w: X-RateLimit-Remaining %q on a 429",
			harness.ErrStep,
			h.Get("X-RateLimit-Remaining"),
		)
	}
	return nil
}

func (s *servedSteps) retryAfter() error {
	if n, err := strconv.Atoi(s.w.Last().Header.Get("Retry-After")); err != nil || n < 1 {
		return fmt.Errorf(
			"%w: Retry-After %q",
			harness.ErrStep,
			s.w.Last().Header.Get("Retry-After"),
		)
	}
	return nil
}

// namesRequest checks that a problem names its request: the last problem's
// instance is urn:infraweave:request: and the X-Request-Id it was answered
// with.
//
// ref: decision 0022
func (s *servedSteps) namesRequest() error {
	resp := s.w.Last()
	p, ok := sdk.AsProblem(resp.Err)
	id := resp.Header.Get("X-Request-Id")
	if !ok || id == "" || p.Instance != "urn:infraweave:request:"+id {
		return fmt.Errorf(
			"%w: instance %q, X-Request-Id %q: %s",
			harness.ErrStep,
			p.Instance,
			id,
			resp.Body,
		)
	}
	return nil
}
