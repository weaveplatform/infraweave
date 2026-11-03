package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/server"
	"github.com/weaveplatform/infraweave/internal/server/problem"
)

// servedSteps runs the server in-process on loopback ports the system
// chooses, so scenarios never contend for a fixed port.
type servedSteps struct {
	srv *server.Running
}

func (s *servedSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a running infraweave server$`, s.running)
	sc.Step(`^"([A-Z]+) ([^"]+)" on the admin listener answers (\d+)$`, s.adminAnswers)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" on the (admin|API) listener answers a (\d+) problem$`,
		s.answersProblem,
	)
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.srv != nil {
			err = errors.Join(err, s.srv.Shutdown(ctx))
			s.srv = nil
		}
		return ctx, err
	})
}

func (s *servedSteps) running(ctx context.Context) error {
	c := server.Defaults()
	c.Listen, c.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	srv, err := server.Start(ctx, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	s.srv = srv
	return nil
}

func (s *servedSteps) addr(listener string) string {
	if listener == "admin" {
		return s.srv.AdminAddr()
	}
	return s.srv.APIAddr()
}

func (s *servedSteps) do(
	ctx context.Context,
	method, listener, path string,
) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+s.addr(listener)+path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	return resp, body, nil
}

func (s *servedSteps) adminAnswers(ctx context.Context, method, path, code string) error {
	resp, body, err := s.do(ctx, method, "admin", path)
	if err != nil {
		return err
	}
	if strconv.Itoa(resp.StatusCode) != code {
		return fmt.Errorf("%w: %s %s answered %d: %s", errStep, method, path, resp.StatusCode, body)
	}
	return nil
}

func (s *servedSteps) answersProblem(
	ctx context.Context,
	method, path, listener, code string,
) error {
	resp, body, err := s.do(ctx, method, listener, path)
	if err != nil {
		return err
	}
	if strconv.Itoa(resp.StatusCode) != code {
		return fmt.Errorf(
			"%w: %s %s answered %d, want %s: %s",
			errStep,
			method,
			path,
			resp.StatusCode,
			code,
			body,
		)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		return fmt.Errorf("%w: %s %s answered %s, not a problem", errStep, method, path, ct)
	}
	var p problem.Problem
	if err := json.Unmarshal(body, &p); err != nil || p.Status != resp.StatusCode {
		return fmt.Errorf(
			"%w: %s %s body is not a problem with its status: %s",
			errStep,
			method,
			path,
			body,
		)
	}
	return nil
}
