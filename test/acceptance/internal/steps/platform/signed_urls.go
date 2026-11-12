package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// signedURLSteps are the steps of the object store's signed URLs.
type signedURLSteps struct{ w *harness.World }

func (s *signedURLSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^"([^"]*)" put with no credential at a URL signed for "([^"]+)" through (A|B) answers (\d+)$`,
		s.putSigned,
	)
	sc.Step(`^a URL signed for reading "([^"]+)" through (A|B) reads "([^"]*)" with no credential$`,
		s.getSigned)
	sc.Step(
		`^an? (tampered|expired) URL signed for (GET|PUT) of "([^"]+)" through (A|B) answers a (\d+) problem "([a-z-]+)"$`,
		s.badSigned,
	)
	sc.Step(`^"GET ([^"]+)" with no credential through (A|B) answers a (\d+) problem "([a-z-]+)"$`,
		s.noCredential)
}

// signURL signs method on key as the named server's fs driver does: under
// the keyring of the scenario's database, for the server's own listener.
// The URL serve signs names objectstore.fs.url, which no client of the
// scenario resolves, so the step signs for the listener instead; the
// token is the same, as the URL's base is no part of it.
func (s *signedURLSteps) signURL(
	ctx context.Context,
	name, method, key string,
	ttl time.Duration,
	size int64,
) (string, error) {
	keys, pool, err := s.w.Keyring(ctx)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	c := objectstore.Defaults().FS
	c.Root, c.URL = s.w.Objects(), "http://"+s.server(name).API
	b, err := fs.New(c, keys)
	if err != nil {
		return "", fmt.Errorf("fs: %w", err)
	}
	defer b.Close()
	signed, err := b.SignURL(ctx, method, key, ttl, objectstore.Constraints{Size: size})
	if err != nil {
		return "", fmt.Errorf("sign %s %s: %w", method, key, err)
	}
	return signed.URL, nil
}

// server is the named replica's process.
func (s *signedURLSteps) server(name string) *harness.Server {
	if name == "B" {
		return s.w.Servers[1]
	}
	return s.w.Servers[0]
}

// sendBare sends a request with no Authorization header, as any client
// holding a signed URL does, and records the answer.
func (s *signedURLSteps) sendBare(
	ctx context.Context,
	method, url, body string,
) (harness.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return harness.Response{}, fmt.Errorf("request: %w", err)
	}
	req.ContentLength = int64(len(body))
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return harness.Response{}, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		return harness.Response{}, fmt.Errorf("read %s %s: %w", method, url, err)
	}
	r := harness.Response{Status: resp.StatusCode, Header: resp.Header, Body: got}
	// The SDK's client would carry an error answer's problem; this one
	// decodes it, so the problem steps read it alike.
	var p sdk.Problem
	if r.Status >= http.StatusBadRequest && json.Unmarshal(got, &p) == nil {
		r.Err = p
	}
	s.w.Sent = append(s.w.Sent, r)
	return r, nil
}

func (s *signedURLSteps) putSigned(ctx context.Context, body, key, name string, code int) error {
	url, err := s.signURL(ctx, name, http.MethodPut, key, time.Minute, int64(len(body)))
	if err != nil {
		return err
	}
	resp, err := s.sendBare(ctx, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	if resp.Status != code {
		return fmt.Errorf(
			"%w: PUT answered %d, want %d: %s",
			harness.ErrStep,
			resp.Status,
			code,
			resp.Body,
		)
	}
	return nil
}

func (s *signedURLSteps) getSigned(ctx context.Context, key, name, want string) error {
	url, err := s.signURL(ctx, name, http.MethodGet, key, time.Minute, 0)
	if err != nil {
		return err
	}
	resp, err := s.sendBare(ctx, http.MethodGet, url, "")
	if err != nil {
		return err
	}
	if resp.Status != http.StatusOK || string(resp.Body) != want {
		return fmt.Errorf(
			"%w: GET answered %d %q, want 200 %q",
			harness.ErrStep,
			resp.Status,
			resp.Body,
			want,
		)
	}
	return nil
}

// badSigned sends a URL whose token does not verify: one character of the
// token changed, or an expiry already past.
func (s *signedURLSteps) badSigned(
	ctx context.Context,
	how, method, key, name, code, slug string,
) error {
	ttl := time.Minute
	if how == "expired" {
		ttl = -time.Minute
	}
	url, err := s.signURL(ctx, name, method, key, ttl, 1)
	if err != nil {
		return err
	}
	if how == "tampered" {
		// A character in the middle, unlike the last, always carries
		// significant bits, so the decoded token changes.
		i := len(url) - 20
		c := byte('A')
		if url[i] == c {
			c = 'B'
		}
		url = url[:i] + string(c) + url[i+1:]
	}
	resp, err := s.sendBare(ctx, method, url, "x")
	if err != nil {
		return err
	}
	return harness.IsProblem(resp, how+" "+method+" URL", code, slug)
}

func (s *signedURLSteps) noCredential(ctx context.Context, path, name, code, slug string) error {
	resp, err := s.sendBare(ctx, http.MethodGet, "http://"+s.server(name).API+path, "")
	if err != nil {
		return err
	}
	return harness.IsProblem(resp, "GET "+path, code, slug)
}
