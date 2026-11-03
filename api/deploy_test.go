package api_test

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/openapibundle"
)

// conventions is decision 0003, whose table holds the slugs its spec
// components use.
const conventions = "../docs/research/decisions/0003-api-conventions.md"

// problems is decision 0022, whose table is the complete list of problem
// slugs. Its rows for 0003's slugs must keep 0003's status.
const problems = "../docs/research/decisions/0022-api-errors-and-update-semantics.md"

const problemPrefix = "urn:infraweave:problem:"

// TestUnitDeployBundleCurrent fails when a fragment under src/deploy changes
// without `make bundle`, so the committed and embedded document never drifts
// from what the fragments say.
func TestUnitDeployBundleCurrent(t *testing.T) {
	want, err := openapibundle.Bundle("src/deploy")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(api.Deploy, want) {
		t.Error("api/deploy.openapi.yaml is stale; run make bundle and commit the result")
	}
}

func loadDeploy(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.Deploy)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestUnitDeployConventionComponents checks that every component decision
// 0003 names exists and says what it is for.
func TestUnitDeployConventionComponents(t *testing.T) {
	c := loadDeploy(t).Components
	for _, name := range []string{"IdempotencyKey", "Cursor", "Limit"} {
		if p := c.Parameters[name]; p == nil || p.Value.Description == "" {
			t.Errorf("parameter %s missing or undocumented", name)
		}
	}
	for _, name := range []string{
		"RateLimitLimit", "RateLimitRemaining", "RateLimitReset", "RetryAfter", "IdempotencyReplayed",
	} {
		if h := c.Headers[name]; h == nil || h.Value.Description == "" {
			t.Errorf("header %s missing or undocumented", name)
		}
	}
	for _, name := range []string{
		"TooManyRequests", "IdempotencyConflict", "IdempotencyMismatch", "ApiDisabled", "InvalidCursor",
	} {
		if r := c.Responses[name]; r == nil || r.Value.Description == nil ||
			*r.Value.Description == "" {
			t.Errorf("response %s missing or undocumented", name)
		}
	}
	tooMany := c.Responses["TooManyRequests"].Value.Headers
	for _, h := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if tooMany[h] == nil {
			t.Errorf("TooManyRequests lacks %s", h)
		}
	}
	if c.Responses["ApiDisabled"].Value.Headers["Retry-After"] == nil {
		t.Error("ApiDisabled lacks Retry-After")
	}
}

// slugTable reads a decision record's problem table: every row that starts
// with a backticked slug and a three-digit status.
func slugTable(t *testing.T, record string) map[string]int {
	t.Helper()
	text, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]int{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| ([0-9]{3}) \\|").
		FindAllStringSubmatch(string(text), -1) {
		listed[m[1]], _ = strconv.Atoi(m[2])
	}
	if len(listed) == 0 {
		t.Fatalf("%s lists no problem slugs", record)
	}
	return listed
}

// TestUnitDeployProblemTypes checks that every problem example in the spec
// uses a slug decision 0022 lists, with the status the record gives it, so
// that record stays the one complete list of problem types. It also checks
// that every slug decision 0003 defines is used and has the same status in
// both records.
func TestUnitDeployProblemTypes(t *testing.T) {
	listed := slugTable(t, problems)
	used := map[string]bool{}
	for name, r := range loadDeploy(t).Components.Responses {
		mt := r.Value.Content.Get("application/problem+json")
		ex, ok := mt.Example.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := ex["type"].(string)
		slug, found := strings.CutPrefix(typ, problemPrefix)
		status, known := listed[slug]
		switch {
		case !found || !known:
			t.Errorf("response %s: type %q is not a slug listed in %s", name, typ, problems)
		case ex["status"] != float64(status):
			t.Errorf("response %s: status %v, record says %d", name, ex["status"], status)
		}
		used[slug] = true
	}
	for slug, status := range slugTable(t, conventions) {
		if !used[slug] {
			t.Errorf("%s lists %s, but no response uses it", conventions, slug)
		}
		if listed[slug] != status {
			t.Errorf("%s gives %s status %d, %s gives %d", conventions, slug, status, problems, listed[slug])
		}
	}
}
