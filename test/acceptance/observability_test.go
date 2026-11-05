package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// observeSteps drive decision 0017's logs, probes and shutdown through the
// first server of the scenario's world.
type observeSteps struct {
	w *world
	// header is the last response's header.
	header http.Header
	// conn carries the request in flight; rest is the body it has not sent.
	conn net.Conn
	rest string
}

func (s *observeSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a running infraweave server on a PostgreSQL of its own$`, s.ownPostgres)
	sc.Step(`^a running infraweave server whose shutdown ready delay is (\S+)$`, s.readyDelay)
	sc.Step(`^a running infraweave scheduler$`, s.scheduler)
	sc.Step(`^it bound no API listener$`, s.noAPI)
	sc.Step(`^its PostgreSQL stops answering$`, s.pause)
	sc.Step(`^its PostgreSQL answers again$`, s.unpause)
	sc.Step(`^"GET ([^"]+)" on the admin listener eventually answers (\d+)$`, s.eventuallyAnswers)
	sc.Step(
		`^"GET ([^"]+)" on the admin listener eventually reports the check "([^"]+)" as (ok|fail)$`,
		s.reportsCheck,
	)
	sc.Step(`^"GET ([^"]+)" is sent on the API listener with X-Request-Id "([^"]*)"$`, s.sendWithID)
	sc.Step(`^the response carries X-Request-Id "([^"]+)"$`, s.echoedID)
	sc.Step(`^the response carries a generated X-Request-Id$`, s.generatedID)
	sc.Step(
		`^every line the server logged has the standard fields with mode "([^"]+)"$`,
		s.standardFields,
	)
	sc.Step(
		`^the server logs a "request" line with request_id "([^"]+)", route "([^"]+)" and status (\d+)$`,
		s.accessLogged,
	)
	sc.Step(`^"POST ([^"]+)" with an Idempotency-Key is in flight with half its body$`, s.inFlight)
	sc.Step(`^the server receives SIGTERM$`, s.sigterm)
	sc.Step(`^the API listener eventually refuses connections$`, s.refuses)
	sc.Step(`^the rest of the body arrives$`, s.restArrives)
	sc.Step(`^the request in flight is answered (\d+)$`, s.inFlightAnswered)
	sc.Step(`^the server exits (\d+)$`, s.exits)
}

func (s *observeSteps) ownPostgres(ctx context.Context) error {
	pg, err := s.w.s.deps.OwnPostgres(ctx)
	if err != nil {
		return err
	}
	s.w.ownPG = pg
	config, err := serverConfig(pg.DSN, nil)
	if err != nil {
		return err
	}
	_, err = s.w.launch(ctx, "all", config)
	return err
}

func (s *observeSteps) readyDelay(ctx context.Context, delay string) error {
	_, err := s.w.startServer(ctx, map[string]any{"shutdown": map[string]any{"ready_delay": delay}})
	return err
}

func (s *observeSteps) scheduler(ctx context.Context) error {
	db, err := s.w.database(ctx)
	if err != nil {
		return err
	}
	config, err := serverConfig(db.DSN, nil)
	if err != nil {
		return err
	}
	_, err = s.w.launch(ctx, "scheduler", config)
	return err
}

func (s *observeSteps) server() (*Server, error) {
	if len(s.w.servers) == 0 {
		return nil, fmt.Errorf("%w: no server is running", errStep)
	}
	return s.w.servers[0], nil
}

func (s *observeSteps) noAPI() error {
	srv, err := s.server()
	if err != nil {
		return err
	}
	if srv.API != "" {
		return fmt.Errorf("%w: it bound an API listener on %s", errStep, srv.API)
	}
	return nil
}

func (s *observeSteps) pause(ctx context.Context) error   { return s.w.ownPG.Pause(ctx) }
func (s *observeSteps) unpause(ctx context.Context) error { return s.w.ownPG.Unpause(ctx) }

// probe answers GET path on the admin listener; a refused connection is an
// answer of 0.
func (s *observeSteps) probe(ctx context.Context, path string) (int, []byte) {
	resp, _ := s.w.admin.Do(ctx, http.MethodGet, path, nil)
	if resp == nil {
		return 0, nil
	}
	return resp.Status, resp.Body
}

func (s *observeSteps) eventuallyAnswers(ctx context.Context, path string, code int) error {
	return Eventually(ctx, fmt.Sprintf("GET %s to answer %d", path, code),
		func(ctx context.Context) (bool, error) {
			if last, _ := s.probe(ctx, path); last != code {
				return false, fmt.Errorf("%w: answered %d", errStep, last)
			}
			return true, nil
		})
}

func (s *observeSteps) reportsCheck(ctx context.Context, path, check, want string) error {
	return Eventually(ctx, fmt.Sprintf("GET %s to report %s as %s", path, check, want),
		func(ctx context.Context) (bool, error) {
			_, body := s.probe(ctx, path)
			var rep struct {
				Checks map[string]struct {
					Status string `json:"status"`
				} `json:"checks"`
			}
			if err := json.Unmarshal(body, &rep); err != nil {
				return false, fmt.Errorf("%w: not the verbose form: %s", errStep, body)
			}
			if got := rep.Checks[check].Status; got != want {
				return false, fmt.Errorf("%w: %s is %q: %s", errStep, check, got, body)
			}
			return true, nil
		})
}

func (s *observeSteps) sendWithID(ctx context.Context, path, id string) error {
	resp, _ := s.w.api.Do(
		ctx,
		http.MethodGet,
		path,
		nil,
		sdk.Header{Name: "X-Request-Id", Value: id},
	)
	if resp == nil {
		return fmt.Errorf("%w: GET %s got no answer", errStep, path)
	}
	s.header = resp.Header
	return nil
}

func (s *observeSteps) echoedID(id string) error {
	if got := s.header.Get("X-Request-Id"); got != id {
		return fmt.Errorf("%w: X-Request-Id %q, want %q", errStep, got, id)
	}
	return nil
}

// generated is the form of a request ID the server makes: 128 random bits
// in unpadded base32.
var generated = regexp.MustCompile(`^[A-Z2-7]{26}$`)

func (s *observeSteps) generatedID() error {
	if got := s.header.Get("X-Request-Id"); !generated.MatchString(got) {
		return fmt.Errorf("%w: X-Request-Id %q is not a generated ID", errStep, got)
	}
	return nil
}

// lines are the JSON objects the first server has logged.
func (s *observeSteps) lines() ([]map[string]any, error) {
	srv, err := s.server()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(srv.Stdout()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("%w: a line is not JSON: %s", errStep, sc.Bytes())
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *observeSteps) standardFields(mode string) error {
	lines, err := s.lines()
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("%w: nothing was logged", errStep)
	}
	for _, l := range lines {
		ts, _ := l["time"].(string)
		at, err := time.Parse(time.RFC3339Nano, ts)
		version, _ := l["version"].(string)
		if err != nil || at.Location() != time.UTC || l["level"] == nil || l["msg"] == nil ||
			l["service"] != "infraweave" || l["mode"] != mode || version == "" {
			return fmt.Errorf("%w: a line lacks the standard fields: %v", errStep, l)
		}
	}
	return nil
}

// accessLogged waits, since the line is written once the handler returns,
// which may be after the client has its answer.
func (s *observeSteps) accessLogged(ctx context.Context, id, route string, status int) error {
	return Eventually(ctx, "the access log line of "+id, func(context.Context) (bool, error) {
		lines, err := s.lines()
		if err != nil {
			return false, err
		}
		for _, l := range lines {
			if l["msg"] == "request" && l["request_id"] == id && l["route"] == route &&
				l["status"] == float64(status) && l["duration_ms"] != nil {
				return true, nil
			}
		}
		return false, nil
	})
}

// inFlight sends the headers and half the body of a POST with an
// Idempotency-Key: the key middleware reads the whole body before it
// answers, so the request stays in its handler until the rest arrives.
func (s *observeSteps) inFlight(ctx context.Context, path string) error {
	srv, err := s.server()
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", srv.API)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	// The connection outlives the step; the deadline bounds a server that
	// never answers.
	_ = conn.SetDeadline(time.Now().Add(waitLimit))
	body := `{"name":"in-flight","space":"root"}`
	half := body[:len(body)/2]
	if _, err := fmt.Fprintf(
		conn,
		"POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\n"+
			"Idempotency-Key: in-flight\r\nContent-Length: %d\r\n\r\n%s",
		path,
		srv.API,
		len(body),
		half,
	); err != nil {
		_ = conn.Close()
		return fmt.Errorf("send: %w", err)
	}
	s.conn, s.rest = conn, body[len(half):]
	return nil
}

func (s *observeSteps) sigterm() error {
	srv, err := s.server()
	if err != nil {
		return err
	}
	return srv.Signal(syscall.SIGTERM)
}

func (s *observeSteps) refuses(ctx context.Context) error {
	srv, err := s.server()
	if err != nil {
		return err
	}
	return Eventually(ctx, "the API listener to refuse", func(ctx context.Context) (bool, error) {
		c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", srv.API)
		if err != nil {
			return true, nil //nolint:nilerr // a refusal is what this waits for
		}
		_ = c.Close()
		return false, nil
	})
}

func (s *observeSteps) restArrives() error {
	if _, err := s.conn.Write([]byte(s.rest)); err != nil {
		return fmt.Errorf("send the rest: %w", err)
	}
	return nil
}

func (s *observeSteps) inFlightAnswered(code int) error {
	defer s.conn.Close()
	resp, err := http.ReadResponse(bufio.NewReader(s.conn), nil)
	if err != nil {
		return fmt.Errorf("%w: the request in flight got no answer: %w", errStep, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != code {
		return fmt.Errorf("%w: the request in flight was answered %d", errStep, resp.StatusCode)
	}
	return nil
}

func (s *observeSteps) exits(ctx context.Context, want string) error {
	srv, err := s.server()
	if err != nil {
		return err
	}
	code, err := srv.ExitCode(ctx)
	if err != nil {
		return err
	}
	if strconv.Itoa(code) != want {
		return fmt.Errorf("%w: exited %d, want %s: %s", errStep, code, want, srv.Stdout())
	}
	return nil
}
