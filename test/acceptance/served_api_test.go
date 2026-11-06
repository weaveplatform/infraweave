package acceptance

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strconv"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// servedSteps drive an infraweave serve subprocess of the scenario's world.
type servedSteps struct {
	w *world
	// sent holds every response of the scenario, the last one last.
	sent []response
	// vault is the Vault KEK the data keys are wrapped by after a rewrap;
	// nil while they are under the local KEK.
	vault *kek.VaultConfig
}

type response struct {
	status int
	header http.Header
	body   []byte
	// err is the SDK's typed error of an answer of 400 or more.
	err error
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
	sc.Step(`^the problem names the request in its instance$`, s.namesRequest)
	s.registerReplicas(sc)
	s.registerSecrets(sc)
	s.registerQueueStats(sc)
	(&queueSteps{s: s}).register(sc)
}

// secretsWith is the secrets settings with kek as secrets.kek; nil
// selects the local KEK, which StartServer passes in KEK.
func secretsWith(kek map[string]any) map[string]any {
	if kek == nil {
		kek = map[string]any{"provider": "local"}
	}
	return map[string]any{"kek": kek}
}

// objectStore is the fs driver under root. Replicas of one scenario share
// the root, as replicas share a volume. No scenario follows a signed URL
// yet, so the URL names no listener.
func objectStore(root string) map[string]any {
	return map[string]any{
		"driver": "fs",
		"fs":     map[string]any{"root": root, "url": "http://infraweave.test"},
	}
}

// serverConfig is a strict configuration file for one server: both
// listeners on loopback port 0, the database at dsn, the local KEK, object
// storage under objects, and the server settings the scenario names over
// them. The ready delay is 0 unless the scenario names one, so stopping a
// server costs no wait.
func serverConfig(dsn, objects string, settings map[string]any) ([]byte, error) {
	return serverConfigKEK(dsn, objects, settings, nil)
}

// publicURL is every server's server.public_url, which the modes that
// serve the API require; no client dials it.
const publicURL = "http://infraweave.test"

// serverConfigKEK is serverConfig with kek as the secrets.kek settings;
// nil keeps the local KEK.
func serverConfigKEK(dsn, objects string, settings, kek map[string]any) ([]byte, error) {
	server := map[string]any{
		"listen":       "127.0.0.1:0",
		"admin_listen": "127.0.0.1:0",
		"shutdown":     map[string]any{"ready_delay": "0s"},
		"public_url":   publicURL,
	}
	maps.Copy(server, settings)
	out, err := yaml.Marshal(map[string]any{
		"server":      server,
		"database":    map[string]any{"url": dsn},
		"secrets":     secretsWith(kek),
		"objectstore": objectStore(objects),
	})
	if err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	return out, nil
}

func (s *servedSteps) start(ctx context.Context, rateLimit map[string]any) error {
	_, err := s.w.startServer(ctx, map[string]any{"rate_limit": rateLimit})
	return err
}

func (s *servedSteps) running(ctx context.Context) error {
	_, err := s.w.startServer(ctx, nil)
	return err
}

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

// send drives the server through pkg/sdk, as every infraweave client
// does. The scenario's clients send each call once, so a step sees the
// answer it asserts on rather than the SDK's retry of it.
func (s *servedSteps) send(ctx context.Context, method, listener, path string) (response, error) {
	c := s.w.api
	if listener == "admin" {
		c = s.w.admin
	}
	resp, err := c.Do(ctx, method, path, nil)
	if resp == nil {
		return response{}, err
	}
	r := response{status: resp.Status, header: resp.Header, body: resp.Body, err: err}
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
	resp, err := s.send(ctx, method, "admin", path)
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
	resp, err := s.send(ctx, method, listener, path)
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
	p, ok := sdk.AsProblem(resp.err)
	if !ok || p.Status != resp.status {
		return fmt.Errorf("%w: %s answered no problem with its status: %w: %s",
			errStep, what, resp.err, resp.body)
	}
	if slug != "" && p.Slug() != slug {
		return fmt.Errorf("%w: %s answered type %q, want %q", errStep, what, p.Type, slug)
	}
	return nil
}

func (s *servedSteps) sendTimes(ctx context.Context, method, path string, n int) error {
	for i := range n {
		resp, err := s.send(ctx, method, "API", path)
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

// namesRequest checks decision 0022's correlation: the last problem's
// instance is urn:infraweave:request: and the X-Request-Id it was answered
// with.
func (s *servedSteps) namesRequest() error {
	resp := s.last()
	p, ok := sdk.AsProblem(resp.err)
	id := resp.header.Get("X-Request-Id")
	if !ok || id == "" || p.Instance != "urn:infraweave:request:"+id {
		return fmt.Errorf(
			"%w: instance %q, X-Request-Id %q: %s",
			errStep,
			p.Instance,
			id,
			resp.body,
		)
	}
	return nil
}
