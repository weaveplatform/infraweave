package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// githubSteps serves a TLS GitHub host. Accounts register their own Apps
// through the API; starting the host provides only its URL and trusted CA.
type githubSteps struct {
	w     *harness.World
	fake  *githubfake.Fake
	srv   *githubfake.Server
	proxy *harness.GitHubProxy
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

// VCS starts the fake on first use and returns only its trusted certificate.
func (g *githubSteps) VCS(_ context.Context, w *harness.World) (map[string]any, []byte, error) {
	// Feature-level fake tags also reach live examples; the lab owns their endpoints.
	if slices.Contains(w.Tags, "@vcs-lab") {
		return nil, nil, nil
	}

	if slices.Contains(w.Tags, "@github") && g.srv == nil {
		g.fake = githubfake.New()
		g.srv = g.fake.Start()
		g.w.IDs["githubHost"] = g.srv.URL
		if slices.Contains(w.Tags, "@provider:github-public") {
			var err error
			g.proxy, err = harness.NewGitHubProxy(g.fake.PublicHandler())
			if err != nil {
				return nil, nil, err
			}
			w.Setenv("HTTPS_PROXY", g.proxy.URL)
			w.Setenv("HTTP_PROXY", g.proxy.URL)
			w.Setenv("NO_PROXY", "127.0.0.1,localhost,::1")
		}
	}
	if g.srv != nil {
		ca := slices.Clone(g.srv.CA)
		if g.proxy != nil {
			ca = append(ca, g.proxy.CA...)
		}
		return nil, ca, nil
	}
	return nil, nil, nil
}

// Stop stops the fake, if the scenario started it.
func (g *githubSteps) Stop() {
	if g.proxy != nil {
		g.proxy.Close()
	}
	if g.srv != nil {
		g.srv.Close()
	}
}

// app registers an App installed on org's repository infra and keeps its
// ID as {keyID} and its private key, escaped for a JSON string, as
// {keyKey}.
func (g *githubSteps) app(key, org string) error {
	if g.fake == nil {
		return fmt.Errorf("%w: a GitHub App needs a @github scenario", harness.ErrStep)
	}
	app := g.fake.AddApp(key, org)
	g.fake.Install(app.ID, org, "Organization", "infra", "web")
	pem, err := json.Marshal(string(app.PrivateKey))
	if err != nil {
		return fmt.Errorf("escape the key of %s: %w", key, err)
	}
	g.w.IDs[key+"ID"] = strconv.FormatInt(app.ID, 10)
	g.w.IDs[key+"Key"] = strings.Trim(string(pem), `"`)
	g.w.IDs[key+"WebhookSecret"] = app.WebhookSecret
	return nil
}

// commit adds a commit to branch of path, an owner/name, which a new
// branch starts from the last commit, and keeps it as key.
func (g *githubSteps) commit(branch, path, key string) error {
	owner, name, ok := strings.Cut(path, "/")
	if g.fake == nil || !ok {
		return fmt.Errorf("%w: no GitHub repository %q in this scenario", harness.ErrStep, path)
	}
	g.w.IDs[key] = g.fake.Repo(owner, name).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// deaf unsubscribes the App kept as key from every event, so its
// connection test fails.
func (g *githubSteps) deaf(key string) error {
	id, err := strconv.ParseInt(g.w.IDs[key+"ID"], 10, 64)
	if err != nil {
		return fmt.Errorf("%w: no GitHub App %s", harness.ErrStep, key)
	}
	g.fake.SetApp(id, githubfake.Permissions, []string{})
	return nil
}

// code keeps as key a code the fake converts once into a new App on
// acme, as GitHub redirects back with after its manifest form.
func (g *githubSteps) code(key string) error {
	if g.fake == nil {
		return fmt.Errorf("%w: a manifest code needs a @github scenario", harness.ErrStep)
	}
	g.w.IDs[key] = g.fake.ManifestCode("acme")
	return nil
}

// state keeps the state the last answer's manifest form posts with.
func (g *githubSteps) state(key string) error {
	var m struct {
		Endpoint string `json:"endpoint"`
	}
	r := g.w.Last()
	if err := json.Unmarshal(r.Body, &m); err != nil {
		return fmt.Errorf("%w: no manifest form in %d %s", harness.ErrStep, r.Status, r.Body)
	}
	u, err := url.Parse(m.Endpoint)
	if err != nil || u.Query().Get("state") == "" {
		return fmt.Errorf("%w: no state in the endpoint %q", harness.ErrStep, m.Endpoint)
	}
	g.w.IDs[key] = u.Query().Get("state")
	return nil
}
