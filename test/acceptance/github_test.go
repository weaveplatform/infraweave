package acceptance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
)

// githubSteps run the GitHub host a @github scenario's servers reach: an
// in-process githubfake over TLS, whose certificate they trust through
// vcs.ca_file, with the operator's App installed on the organization
// acme, which the servers' vcs.github_app names. Its URL is {githubHost}
// to the scenario's requests.
type githubSteps struct {
	o    *orgSteps
	fake *githubfake.Fake
	srv  *githubfake.Server
	// app0 is the vcs.github_app settings every server of the scenario
	// reads.
	app0 map[string]any
	// ids are the scenario's kept values, which the requests name as
	// {key}, shared with the organization steps.
	ids map[string]string
}

func (g *githubSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a GitHub App "(\w+)" installed on the organization "([^"]+)"$`, g.app)
	sc.Step(`^the GitHub App "(\w+)" subscribes to no events$`, g.deaf)
	sc.Step(`^the GitHub host hands back a manifest code kept as "(\w+)"$`, g.code)
	sc.Step(`^the answer's manifest state is kept as "(\w+)"$`, g.state)
	sc.Step(
		`^a commit to "([\w./-]+)" of the GitHub repository "([^"]+)" is kept as "(\w+)"$`,
		g.commit,
	)
}

// start serves the fake with the operator's App and answers the vcs
// settings that reach it: the operator's App, its key and webhook secret
// written to files under dir, and the fake's certificate, which the
// caller writes to vcs.ca_file.
func (g *githubSteps) start(dir string) (map[string]any, []byte, error) {
	g.fake = githubfake.New()
	g.srv = g.fake.Start()
	op := g.fake.AddApp("infraweave", "acme")
	g.fake.Install(op.ID, "acme", "Organization", "infra", "web")
	key, secret := filepath.Join(dir, "github-app.pem"), filepath.Join(dir, "github-app.secret")
	if err := os.WriteFile(key, op.PrivateKey, 0o600); err != nil {
		return nil, nil, fmt.Errorf("operator App key: %w", err)
	}
	if err := os.WriteFile(secret, []byte(op.WebhookSecret), 0o600); err != nil {
		return nil, nil, fmt.Errorf("operator App secret: %w", err)
	}
	g.ids["githubHost"] = g.srv.URL
	g.app0 = map[string]any{
		"app_id": strconv.FormatInt(op.ID, 10), "private_key_file": key,
		"webhook_secret_file": secret, "api_host": g.srv.URL,
	}
	return g.app0, g.srv.CA, nil
}

// stop stops the fake, if the scenario started it.
func (g *githubSteps) stop() {
	if g.srv != nil {
		g.srv.Close()
	}
}

// app registers an App installed on org's repository infra and keeps its
// ID as {keyID} and its private key, escaped for a JSON string, as
// {keyKey}.
func (g *githubSteps) app(key, org string) error {
	if g.fake == nil {
		return fmt.Errorf("%w: a GitHub App needs a @github scenario", errStep)
	}
	app := g.fake.AddApp(key, org)
	g.fake.Install(app.ID, org, "Organization", "infra")
	pem, err := json.Marshal(string(app.PrivateKey))
	if err != nil {
		return fmt.Errorf("escape the key of %s: %w", key, err)
	}
	g.ids[key+"ID"] = strconv.FormatInt(app.ID, 10)
	g.ids[key+"Key"] = strings.Trim(string(pem), `"`)
	return nil
}

// commit adds a commit to branch of path, an owner/name, which a new
// branch starts from the last commit, and keeps it as key.
func (g *githubSteps) commit(branch, path, key string) error {
	owner, name, ok := strings.Cut(path, "/")
	if g.fake == nil || !ok {
		return fmt.Errorf("%w: no GitHub repository %q in this scenario", errStep, path)
	}
	g.ids[key] = g.fake.Repo(owner, name).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// deaf unsubscribes the App kept as key from every event, so its
// connection test fails.
func (g *githubSteps) deaf(key string) error {
	id, err := strconv.ParseInt(g.ids[key+"ID"], 10, 64)
	if err != nil {
		return fmt.Errorf("%w: no GitHub App %s", errStep, key)
	}
	g.fake.SetApp(id, githubfake.Permissions, []string{})
	return nil
}

// code keeps as key a code the fake converts once into a new App on
// acme, as GitHub redirects back with after its manifest form.
func (g *githubSteps) code(key string) error {
	if g.fake == nil {
		return fmt.Errorf("%w: a manifest code needs a @github scenario", errStep)
	}
	g.ids[key] = g.fake.ManifestCode("acme")
	return nil
}

// state keeps the state the last answer's manifest form posts with.
func (g *githubSteps) state(key string) error {
	var m struct {
		Endpoint string `json:"endpoint"`
	}
	r := g.o.a.s.last()
	if err := json.Unmarshal(r.body, &m); err != nil {
		return fmt.Errorf("%w: no manifest form in %d %s", errStep, r.status, r.body)
	}
	u, err := url.Parse(m.Endpoint)
	if err != nil || u.Query().Get("state") == "" {
		return fmt.Errorf("%w: no state in the endpoint %q", errStep, m.Endpoint)
	}
	g.ids[key] = u.Query().Get("state")
	return nil
}
