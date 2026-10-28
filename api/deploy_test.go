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

// conventions is decision 0003, which lists every problem slug once.
const conventions = "../docs/research/decisions/0003-api-conventions.md"

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

// TestUnitDeployProblemTypes checks that every problem example in the spec
// uses a slug decision 0003 lists, with the status the record gives it, so
// the record stays the one list of problem types.
func TestUnitDeployProblemTypes(t *testing.T) {
	record, err := os.ReadFile(conventions)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]int{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| ([0-9]{3}) \\|").
		FindAllStringSubmatch(string(record), -1) {
		listed[m[1]], _ = strconv.Atoi(m[2])
	}
	if len(listed) == 0 {
		t.Fatalf("%s lists no problem slugs", conventions)
	}
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
			t.Errorf("response %s: type %q is not a slug listed in %s", name, typ, conventions)
		case ex["status"] != float64(status):
			t.Errorf("response %s: status %v, record says %d", name, ex["status"], status)
		}
		used[slug] = true
	}
	for slug := range listed {
		if !used[slug] {
			t.Errorf("%s lists %s, but no response uses it", conventions, slug)
		}
	}
}
