package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/iac/version"
)

// fixtures is where make vendors-fixtures records the vendors' indexes.
const fixtures = "../../internal/vendors/internal/index/testdata/"

// catalogSteps drive the Version Catalog over a chi server that serves
// the recorded indexes as the vendors serve them: HashiCorp's a page at a
// time, OpenTofu's whole.
type catalogSteps struct {
	o *orgSteps

	mu sync.Mutex
	// terraform and opentofu are the recorded indexes' bodies.
	terraform []map[string]any
	opentofu  json.RawMessage
	// withheld is the Terraform release the index does not list yet.
	withheld string
	// down answers every request for the Terraform index 503.
	down bool
	// reads counts each index's whole reads: a request for HashiCorp's
	// first page, or for OpenTofu's document.
	reads map[string]int
	srv   *httptest.Server
}

func (c *catalogSteps) register(sc *godog.ScenarioContext) {
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if c.srv != nil {
			c.srv.Close()
		}
		return ctx, err
	})
	sc.Step(
		`^the release indexes serve the recorded fixtures, Terraform's without its newest release$`,
		c.serve,
	)
	sc.Step(`^the Terraform index publishes its newest release$`, c.publish)
	sc.Step(`^the Terraform index stops answering$`, c.stop)
	sc.Step(
		`^token "(\w+)" eventually reads the (Terraform|OpenTofu) versions through ([AB]) as the fixture's stable releases newest first(, without the newest)?$`,
		c.eventuallyLists,
	)
	sc.Step(
		`^token "(\w+)" reads the (Terraform|OpenTofu) versions through ([AB]) in pages of (\d+) as the fixture's stable releases newest first(, without the newest)?$`,
		c.lists,
	)
	sc.Step(`^each release index was read once$`, c.readOnce)
	sc.Step(
		`^token "(\w+)" resolves the (Terraform|OpenTofu) constraints "([^"]*)" through ([AB])$`,
		c.resolve,
	)
	sc.Step(`^the answer is the version "([^"]+)"$`, c.isVersion)
	sc.Step(`^the installation switches off the features "([^"]+)"$`, c.switchOff)
}

// switchOff switches the comma-separated features off for every server
// the scenario starts after it.
func (c *catalogSteps) switchOff(names string) error {
	off := strings.Split(names, ",")
	for i := range off {
		off[i] = strings.TrimSpace(off[i])
	}
	c.o.a.s.w.features = map[string]any{"disabled": off}
	return nil
}

// serve starts the index server for the scenario's servers to read.
func (c *catalogSteps) serve() error {
	var tf, tofu struct {
		Body json.RawMessage `json:"body"`
	}
	for name, into := range map[string]any{"terraform": &tf, "opentofu": &tofu} {
		raw, err := os.ReadFile(fixtures + name + ".json")
		if err != nil {
			return fmt.Errorf("%w: %w", errStep, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			return fmt.Errorf("%w: fixture %s: %w", errStep, name, err)
		}
	}
	if err := json.Unmarshal(tf.Body, &c.terraform); err != nil {
		return fmt.Errorf("%w: terraform fixture: %w", errStep, err)
	}
	c.opentofu, c.reads = tofu.Body, map[string]int{}
	newest, err := stableReleases("terraform", tf.Body)
	if err != nil {
		return err
	}
	c.withheld = newest[0]

	r := chi.NewRouter()
	r.Get("/terraform", c.hashicorp)
	r.Get("/opentofu", func(w http.ResponseWriter, _ *http.Request) {
		c.count("opentofu")
		_, _ = w.Write(c.opentofu) // a client gone has no answer
	})
	c.srv = httptest.NewServer(r)
	c.o.a.s.w.vendors = map[string]any{
		"terraform_index": c.srv.URL + "/terraform", "opentofu_index": c.srv.URL + "/opentofu",
	}
	return nil
}

// count records a read of index and reports whether Terraform's is down.
func (c *catalogSteps) count(index string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads[index]++
	return c.down
}

// hashicorp pages the Terraform fixture as HashiCorp's releases API does:
// limit releases, created before after's time when it names one.
func (c *catalogSteps) hashicorp(w http.ResponseWriter, req *http.Request) {
	after := req.URL.Query().Get("after")
	down := c.isDown()
	if after == "" {
		down = c.count("terraform")
	}
	if down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 20 {
		http.Error(w, "limit is 1 to 20", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	withheld := c.withheld
	c.mu.Unlock()
	cut, _ := time.Parse(time.RFC3339Nano, after)
	page := []map[string]any{}
	for _, rel := range c.terraform {
		created, _ := time.Parse(time.RFC3339Nano, rel["timestamp_created"].(string))
		if rel["version"] == withheld || after != "" && !created.Before(cut) {
			continue
		}
		page = append(page, rel)
	}
	_ = json.NewEncoder(w).Encode(page[:min(limit, len(page))]) // as above
}

func (c *catalogSteps) isDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.down
}

func (c *catalogSteps) publish() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.withheld = ""
	return nil
}

func (c *catalogSteps) stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.down = true
	return nil
}

// stableReleases are the stable releases of the recorded index name's
// body, newest first, as the catalogue lists them.
func stableReleases(name string, body json.RawMessage) ([]string, error) {
	var raw []string
	if name == "terraform" {
		var rels []struct {
			Version    string `json:"version"`
			Prerelease bool   `json:"is_prerelease"` //nolint:tagliatelle // HashiCorp's spelling
			Status     struct {
				State string `json:"state"`
			} `json:"status"`
		}
		if err := json.Unmarshal(body, &rels); err != nil {
			return nil, fmt.Errorf("%w: %w", errStep, err)
		}
		for _, r := range rels {
			if !r.Prerelease && r.Status.State != "withdrawn" {
				raw = append(raw, r.Version)
			}
		}
	} else {
		var idx struct {
			Versions []struct {
				ID string `json:"id"`
			} `json:"versions"`
		}
		if err := json.Unmarshal(body, &idx); err != nil {
			return nil, fmt.Errorf("%w: %w", errStep, err)
		}
		for _, v := range idx.Versions {
			raw = append(raw, v.ID)
		}
	}
	var vs []version.Version
	for _, s := range raw {
		if v, err := version.Parse(s); err == nil && v.Prerelease == "" {
			vs = append(vs, v)
		}
	}
	slices.SortFunc(vs, func(a, b version.Version) int { return b.Compare(a) })
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		if len(out) == 0 || out[len(out)-1] != v.String() {
			out = append(out, v.String())
		}
	}
	return out, nil
}

// want is the versions the catalogue lists of tool, from its fixture,
// less the newest when without is set.
func (c *catalogSteps) want(tool, without string) ([]string, error) {
	name := strings.ToLower(tool)
	body := c.opentofu
	if name == "terraform" {
		raw, _ := json.Marshal(c.terraform) // decoded JSON encodes
		body = raw
	}
	all, err := stableReleases(name, body)
	if err != nil || without == "" {
		return all, err
	}
	return all[1:], nil
}

// path is tool's operation path.
func path(tool, op string) string {
	if tool == "OpenTofu" {
		return "/api/v1/open-tofu-" + op
	}
	return "/api/v1/terraform-" + op
}

// read pages through tool's versions with token name, n at a time.
func (c *catalogSteps) read(
	ctx context.Context,
	name, tool, replica string,
	n int,
) ([]string, error) {
	got := []string{}
	cursor := ""
	for range 100 {
		q := url.Values{"limit": {strconv.Itoa(n)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		r, err := c.o.a.do(
			ctx,
			replica,
			name,
			http.MethodGet,
			path(tool, "versions")+"?"+q.Encode(),
			nil,
		)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items      []string `json:"items"`
			NextCursor string   `json:"nextCursor"`
		}
		if json.Unmarshal(r.body, &page) != nil || r.status != http.StatusOK {
			return nil, fmt.Errorf("%w: not a page of versions: %d %s", errStep, r.status, r.body)
		}
		if page.NextCursor != "" && len(page.Items) != n {
			return nil, fmt.Errorf(
				"%w: a page before the last holds %d versions",
				errStep,
				len(page.Items),
			)
		}
		got = append(got, page.Items...)
		if cursor = page.NextCursor; cursor == "" {
			return got, nil
		}
	}
	return nil, fmt.Errorf("%w: the versions did not end after 100 pages", errStep)
}

// lists checks tool's versions, read n at a time, against the fixture's.
func (c *catalogSteps) lists(
	ctx context.Context,
	name, tool, replica string,
	n int,
	without string,
) error {
	want, err := c.want(tool, without)
	if err != nil {
		return err
	}
	got, err := c.read(ctx, name, tool, replica, n)
	if err == nil && !reflect.DeepEqual(got, want) {
		err = fmt.Errorf("%w: %s lists %v, want %v", errStep, tool, got, want)
	}
	return err
}

// eventuallyLists waits for a refresh to list the fixture's versions.
func (c *catalogSteps) eventuallyLists(
	ctx context.Context,
	name, tool, replica, without string,
) error {
	return Eventually(ctx, tool+" versions to be listed", func(ctx context.Context) (bool, error) {
		err := c.lists(ctx, name, tool, replica, 10, without)
		return err == nil, err
	})
}

// readOnce checks that both servers' startups read each index once
// between them: the periodic task fires once a period across replicas.
func (c *catalogSteps) readOnce() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reads["terraform"] != 1 || c.reads["opentofu"] != 1 {
		return fmt.Errorf("%w: the indexes were read %v times, want once each", errStep, c.reads)
	}
	return nil
}

// resolve asks for tool's effective version under constraints.
func (c *catalogSteps) resolve(ctx context.Context, name, tool, constraints, replica string) error {
	q := url.Values{"constraints": {constraints}}
	_, err := c.o.a.do(
		ctx,
		replica,
		name,
		http.MethodGet,
		path(tool, "effective-version")+"?"+q.Encode(),
		nil,
	)
	return err
}

// isVersion checks the last answer, a JSON string, is v.
func (c *catalogSteps) isVersion(v string) error {
	r := c.o.a.s.last()
	var got string
	if json.Unmarshal(r.body, &got) != nil || r.status != http.StatusOK || got != v {
		return fmt.Errorf("%w: answered %d %s, want %q", errStep, r.status, r.body, v)
	}
	return nil
}
