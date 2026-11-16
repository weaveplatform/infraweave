package state

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// revocationBound is how long a step waits for a revoked API key to stop
// reading state: disabling or deleting the key revokes the session of its
// kept token, which every replica drops as the revocation's notification
// arrives, well within it.
//
// ref: decision 0010 § "Who may call what"
const revocationBound = 30 * time.Second

// credential matches the credential a step sends: a tenant's API key as
// <id>:<secret>, or with another secret, a kept token, or none.
var credential = regexp.MustCompile(`^(?:the (\w+) key( and another secret)?|token "(\w+)"|no password)$`)

func (b *backendSteps) registerExternal(sc *godog.ScenarioContext) {
	const cred = `(the \w+ key(?: and another secret)?|token "\w+"|no password)`
	sc.Step(`^"(GET|POST|LOCK|UNLOCK) ([^"]+)" is sent to the state backend as "(\w+)" with `+
		cred+` through ([AB])$`, b.sendState)
	sc.Step(`^"GET ([^"]+)" sent to the state backend as "(\w+)" with `+cred+
		` through ([AB]) answers (\d+) within 30 seconds$`, b.revoked)
	sc.Step(`^the answer challenges with '([^']*)'$`, b.challenges)
	sc.Step(`^a workspace "(\w+)" reads the output "(\w+)" of the stack "([^"]+)" with `+
		`(the \w+ key|token "\w+") through ([AB])$`, b.userWorkspace)
	sc.Step(`^the workspace of the run "(\w+)" reads the output "(\w+)" of the stack "([^"]+)" `+
		`as "(\w+)"$`, b.runReads)
	sc.Step(`^the relay passed a GET of the state of the stack "([^"]+)" answered (\d+)$`,
		b.passedStack)
	sc.Step(`^the last output of the (?:run|workspace) "(\w+)" holds '([^']*)'$`, b.holds)
	sc.Step(`^Terraform's service discovery through ([AB]) names no Terraform Cloud service$`,
		b.noCloud)
}

// cloudServices are the service IDs through which Terraform's remote
// backend and cloud block find a Terraform Cloud API.
var cloudServices = []string{"tfe.v2", "tfe.v2.1", "tfe.v2.2", "state.v2"}

// noCloud checks that replica serves no Terraform Cloud API for external
// state: its service discovery document, when it serves one, names none
// of cloudServices, and Terraform Cloud's ping is not served. A path the
// API does not serve answers a request with no credential 401, before any
// 404, so neither is told apart from another refusal.
//
// ref: decision 0010 § "Who may call what"
func (b *backendSteps) noCloud(ctx context.Context, replica string) error {
	get := func(path string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.w.Base(replica)+path, nil)
		if err != nil {
			return 0, nil, fmt.Errorf("request: %w", err)
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("GET %s: %w", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, body, err //nolint:wrapcheck // the body's read
	}
	status, body, err := get("/.well-known/terraform.json")
	if err != nil {
		return err
	}
	var services map[string]any
	if status == http.StatusOK && json.Unmarshal(body, &services) != nil {
		return fmt.Errorf("%w: discovery answered %s", harness.ErrStep, body)
	}
	for _, id := range cloudServices {
		if _, ok := services[id]; ok {
			return fmt.Errorf("%w: discovery names %s: %s", harness.ErrStep, id, body)
		}
	}
	if status, body, err = get("/api/v2/ping"); err != nil {
		return err
	}
	if status < http.StatusBadRequest {
		return fmt.Errorf("%w: GET /api/v2/ping answered %d %s", harness.ErrStep, status, body)
	}
	return nil
}

// password is the HTTP Basic password the credential phrase names.
func (b *backendSteps) password(phrase string) (string, error) {
	m := credential.FindStringSubmatch(phrase)
	switch {
	case m == nil:
		return "", fmt.Errorf("%w: no credential %q", harness.ErrStep, phrase)
	case m[3] != "":
		tok, ok := b.w.Tokens[m[3]]
		if !ok {
			return "", fmt.Errorf("%w: no token %q", harness.ErrStep, m[3])
		}
		return tok, nil
	case m[1] == "":
		return "", nil
	}
	t, ok := harness.Tenants[m[1]]
	if !ok {
		return "", fmt.Errorf("%w: no tenant %q", harness.ErrStep, m[1])
	}
	secret := t.Secret()
	if m[2] != "" {
		secret = "iwk_another"
	}
	return t.Key + ":" + secret, nil
}

// sendState sends method on path, under /state/v1, to replica with HTTP
// Basic credentials of user and the credential phrase names, as the http
// backend does, and records the answer for the answer steps. A LOCK or
// UNLOCK carries a LockInfo and a POST a state.
func (b *backendSteps) sendState(ctx context.Context, method, path, user, phrase, replica string) error {
	_, err := b.state(ctx, method, path, user, phrase, replica)
	return err
}

func (b *backendSteps) state(ctx context.Context, method, path, user, phrase, replica string) (harness.Response, error) {
	pass, err := b.password(phrase)
	if err != nil {
		return harness.Response{}, err
	}
	body := map[string]string{
		http.MethodGet: "", http.MethodPost: `{"version":4,"serial":1,"lineage":"x"}`,
		stateapi.MethodLock: `{"ID":"external"}`, stateapi.MethodUnlock: `{"ID":"external"}`,
	}[method]
	req, err := http.NewRequestWithContext(ctx, method, b.w.Base(replica)+path,
		strings.NewReader(body))
	if err != nil {
		return harness.Response{}, fmt.Errorf("request: %w", err)
	}
	req.ContentLength = int64(len(body))
	req.SetBasicAuth(user, pass)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return harness.Response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		return harness.Response{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	r := harness.Response{Status: resp.StatusCode, Header: resp.Header, Body: got}
	var p sdk.Problem
	if r.Status >= http.StatusBadRequest && json.Unmarshal(got, &p) == nil {
		r.Err = p
	}
	b.w.Sent = append(b.w.Sent, r)
	return r, nil
}

// revoked sends a GET of path as sendState does until it answers status,
// for at most revocationBound.
func (b *backendSteps) revoked(ctx context.Context, path, user, phrase, replica string, status int) error {
	ctx, cancel := context.WithTimeout(ctx, revocationBound)
	defer cancel()
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, fmt.Sprintf("GET %s answering %d", path, status),
		func(ctx context.Context) (bool, error) {
			r, err := b.state(ctx, http.MethodGet, path, user, phrase, replica)
			return err == nil && r.Status == status, nil
		})
}

// challenges checks the last answer's WWW-Authenticate.
func (b *backendSteps) challenges(want string) error {
	if got := b.w.Last().Header.Get("WWW-Authenticate"); got != want {
		return fmt.Errorf("%w: WWW-Authenticate %q, want %q", harness.ErrStep, got, want)
	}
	return nil
}

// address is the state backend's address of the stack seg names, through
// the relay, as a container on the scenario's network reaches it.
func (b *backendSteps) address(seg string) string {
	return b.relay.URL + stateapi.BaseURL + "/stacks/" + seg
}

// userWorkspace writes a workspace of its own, name, as a user's machine
// holds one: a configuration whose terraform_remote_state reads the stack
// seg names through the relay to replica with the user name api and the
// credential phrase names, and whose output name is the stack's output.
// The credential reaches the tool only in its environment, as a variable,
// and the workspace keeps its own state on disk.
func (b *backendSteps) userWorkspace(ctx context.Context, name, output, seg, phrase, replica string) error {
	if err := b.start(ctx, replica); err != nil {
		return err
	}
	pass, err := b.password(phrase)
	if err != nil {
		return err
	}
	dir := filepath.Join(b.w.Dir, "workspace-"+name)
	config := fmt.Sprintf(`variable "password" {
  type      = string
  sensitive = true
}

data "terraform_remote_state" "stack" {
  backend = "http"
  config = {
    address  = %q
    username = "api"
    password = var.password
  }
}

output %q {
  value = data.terraform_remote_state.stack.outputs.%s
}
`, b.address(seg), output, output)
	if err := write(dir, "main.tf", config); err != nil {
		return err
	}
	b.runs[name] = &takenRun{dir: dir, env: append([]string{"TF_VAR_password=" + pass}, toolEnv...)}
	return nil
}

// runReads adds to the workspace of the run kept as run a
// terraform_remote_state of the stack seg names, through the relay, with
// no credentials, so the http backend takes the run's own from the
// environment the runner sets, and an output as of the stack's output.
func (b *backendSteps) runReads(run, output, seg, as string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	return write(tr.dir, "remote.tf", fmt.Sprintf(`data "terraform_remote_state" %q {
  backend = "http"
  config = {
    address = %q
  }
}

output %q {
  value = data.terraform_remote_state.%s.outputs.%s
}
`, as, b.address(seg), as, as, output))
}

func write(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	return nil
}

// passedStack checks that the relay passed a GET of the state of the
// stack seg names, by that name, answered status.
func (b *backendSteps) passedStack(seg string, status int) error {
	path := stateapi.BaseURL + "/stacks/" + seg
	if slices.Contains(b.relay.Calls(),
		harness.Call{Method: http.MethodGet, Path: path, Status: status}) {
		return nil
	}
	return fmt.Errorf("%w: the relay passed no GET %s answered %d: %+v", harness.ErrStep, path,
		status, b.relay.Calls())
}
