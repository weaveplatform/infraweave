package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// urlSteps are the steps of the signed state URLs: they keep the URL an
// answer names, and the object ID beside an upload URL, and send it with
// no credential to a replica.
type urlSteps struct {
	w       *harness.World
	urls    map[string]string
	objects map[string]string
}

func (u *urlSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's url is kept as "(\w+)"$`, u.keep)
	sc.Step(`^'([^']*)' is put with no credential at the URL "(\w+)"( tampered)? through ([AB])$`,
		u.put)
	sc.Step(`^the upload of the URL "(\w+)" is one sealed object in the uploads store$`,
		u.uploaded)
	sc.Step(
		`^the URL "(\w+)" read with no credential through ([AB]) is the current state version `+
			`of the run "(\w+)", holding "([^"]+)"$`,
		u.current,
	)
	sc.Step(`^the URL "(\w+)" read with no credential through ([AB]) answers a (\d+) problem `+
		`"([a-z-]+)"$`, u.refused)
}

// keep keeps the last answer's url as name, and its objectId when it
// names one.
func (u *urlSteps) keep(name string) error {
	var a struct {
		URL      string `json:"url"`
		ObjectID string `json:"objectId"`
	}
	last := u.w.Last()
	if json.Unmarshal(last.Body, &a) != nil || a.URL == "" {
		return fmt.Errorf("%w: no url in %d %s", harness.ErrStep, last.Status, last.Body)
	}
	if u.urls == nil {
		u.urls, u.objects = map[string]string{}, map[string]string{}
	}
	u.urls[name], u.objects[name] = a.URL, a.ObjectID
	return nil
}

// at is the URL kept as name on the replica's listener. Every URL is
// absolute under server.public_url, which no client of the scenario
// resolves; the path is the same on either replica.
func (u *urlSteps) at(name, replica string) (string, error) {
	url, ok := u.urls[name]
	if !ok {
		return "", fmt.Errorf("%w: no URL %q", harness.ErrStep, name)
	}
	path, ok := strings.CutPrefix(url, harness.PublicURL)
	if !ok {
		return "", fmt.Errorf("%w: %s is not under %s", harness.ErrStep, url, harness.PublicURL)
	}
	return u.w.Base(replica) + path, nil
}

// send sends a request with no Authorization header, as any client
// holding a signed URL does, and records the answer for the answer steps.
func (u *urlSteps) send(ctx context.Context, method, url, body string) (harness.Response, error) {
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
	var p sdk.Problem
	if r.Status >= http.StatusBadRequest && json.Unmarshal(got, &p) == nil {
		r.Err = p
	}
	u.w.Sent = append(u.w.Sent, r)
	return r, nil
}

// put PUTs body to the URL kept as name, a character inside its token
// changed when tampered, leaving the answer to the answer steps.
func (u *urlSteps) put(ctx context.Context, body, name, tampered, replica string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	if tampered != "" {
		i, c := len(url)-10, "A"
		if url[i] == 'A' {
			c = "B"
		}
		url = url[:i] + c + url[i+1:]
	}
	_, err = u.send(ctx, http.MethodPut, url, body)
	return err
}

// uploaded checks that the upload URL kept as name stored one live
// object in the uploads store under its object ID, with a sealed content
// key.
func (u *urlSteps) uploaded(ctx context.Context, name string) error {
	object, ok := u.objects[name]
	if !ok || object == "" {
		return fmt.Errorf("%w: no object ID kept with %q", harness.ErrStep, name)
	}
	var n int
	if err := u.w.Row(ctx, `SELECT count(*) FROM blob_objects
		WHERE store = 'uploads' AND state = 'live' AND content_key_sealed IS NOT NULL
			AND split_part(key, '/', 3) = $1`, []any{object}, &n); err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: %d sealed objects of %s in uploads", harness.ErrStep, n, object)
	}
	return nil
}

// current checks that the URL kept as name serves, with no credential,
// the current version of the workspace the run kept as run wrote to, as
// its SHA-256 records it, and that the state holds want, unsealed.
func (u *urlSteps) current(ctx context.Context, name, replica, run, want string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	r, err := u.send(ctx, http.MethodGet, url, "")
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("%w: GET answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	id, ok := u.w.IDs[run]
	if !ok {
		return fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var sum []byte
	if err := u.w.Row(ctx, `SELECT v.sha256
		FROM state_workspaces s JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE (s.account_id, s.stack_id, s.name) IN (
			SELECT account_id, stack_id, workspace FROM managed_state_versions WHERE run_id = $1)`,
		[]any{harness.UUIDOf(id)}, &sum); err != nil {
		return err
	}
	if got := sha256.Sum256(r.Body); !bytes.Equal(got[:], sum) ||
		!bytes.Contains(r.Body, []byte(want)) {
		return fmt.Errorf("%w: the download's SHA-256 is %x, the current version's %x; "+
			"holding %q: %t", harness.ErrStep, got, sum, want, bytes.Contains(r.Body, []byte(want)))
	}
	return nil
}

// refused checks that the URL kept as name answers a GET with no
// credential with the problem of status and slug.
func (u *urlSteps) refused(ctx context.Context, name, replica, status, slug string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	r, err := u.send(ctx, http.MethodGet, url, "")
	if err != nil {
		return err
	}
	return harness.IsProblem(r, "the download", status, slug) //nolint:wrapcheck // a step error
}
