package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// servedSteps drive an infraweave serve subprocess of the scenario's world.
type servedSteps struct {
	w *world
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
	sc.Step(`^"([A-Z]+) ([^"]+)" on the admin listener answers (\d+)$`, s.adminAnswers)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" on the (admin|API) listener answers a (\d+) problem(?: "([a-z-]+)")?$`,
		s.answersProblem,
	)
	sc.Step(`^"([A-Z]+) ([^"]+)" is sent (\d+) times on the API listener$`, s.sendTimes)
	sc.Step(`^the response carries Retry-After and the rate-limit headers$`, s.rateLimitHeaders)
	sc.Step(`^the response carries Retry-After$`, s.retryAfter)
}

// serverConfig is a strict configuration file for one scenario's server:
// both listeners on loopback port 0, and the settings the scenario names.
func serverConfig(rateLimit map[string]any) ([]byte, error) {
	server := map[string]any{"listen": "127.0.0.1:0", "admin_listen": "127.0.0.1:0"}
	if rateLimit != nil {
		server["rate_limit"] = rateLimit
	}
	out, err := yaml.Marshal(map[string]any{"server": server})
	if err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	return out, nil
}

func (s *servedSteps) start(ctx context.Context, rateLimit map[string]any) error {
	c, err := serverConfig(rateLimit)
	if err != nil {
		return err
	}
	_, err = s.w.startServer(ctx, c)
	return err
}

func (s *servedSteps) running(ctx context.Context) error { return s.start(ctx, nil) }

// runningWithBursts refills too slowly to matter while a scenario runs, so
// the bursts alone decide what is admitted.
func (s *servedSteps) runningWithBursts(ctx context.Context, standard, expensive int) error {
	return s.start(ctx, map[string]any{
		"standard":  map[string]any{"rate": 1e-6, "burst": standard},
		"expensive": map[string]any{"rate": 1e-6, "burst": expensive},
	})
}

func (s *servedSteps) runningDisabling(ctx context.Context, caller string) error {
	return s.start(ctx, map[string]any{"disabled_callers": []string{caller}})
}

func (s *servedSteps) addr(listener string) string {
	if listener == "admin" {
		return s.w.server.Admin
	}
	return s.w.server.API
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
