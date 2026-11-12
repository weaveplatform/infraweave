package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// registerPersonal adds the steps of the keys the Personal API Keys tag
// answers, kept beside the created keys so the exchange steps reach them.
func (p *keySteps) registerPersonal(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's key is the key "(\w+)"$`, p.keepPersonal)
	sc.Step(`^the home account's member is "(\w+)"$`, func(name string) error {
		p.w.IDs[name] = harness.MemberID
		return nil
	})
	sc.Step(`^the answer is '([^']*)'$`, p.is)
	sc.Step(`^the answer lists the keys "([^"]*)"$`, func(want string) error {
		return p.names("items", want)
	})
	sc.Step(`^the answer's (apiKeys|ownedApiKeys) are the keys "([^"]*)"$`, p.names)
}

// keepPersonal names the last answer's key, with the secret only its creation
// answers.
func (p *keySteps) keepPersonal(name string) error {
	var v struct {
		ID     string  `json:"id"`
		Secret *string `json:"secret"`
	}
	r := p.w.Last()
	if json.Unmarshal(r.Body, &v) != nil || v.ID == "" || v.Secret == nil {
		return fmt.Errorf("%w: the answer is no new key: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	p.keys[name] = &createdKey{id: v.ID, secret: *v.Secret, first: *v.Secret}
	p.w.IDs[name] = v.ID
	return nil
}

// is checks the last answer's whole body against want, as JSON.
func (p *keySteps) is(want string) error {
	var g, w any
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &g) != nil ||
		json.Unmarshal([]byte(want), &w) != nil || !reflect.DeepEqual(g, w) {
		return fmt.Errorf("%w: answered %d %s, want %s", harness.ErrStep, r.Status, r.Body, want)
	}
	return nil
}

// names checks the last answer's field, a list of keys or of references
// to them, against the named keys, comma-separated, in the list's order.
func (p *keySteps) names(field, want string) error {
	var body map[string]json.RawMessage
	var keys []struct {
		ID string `json:"id"`
	}
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &body) != nil ||
		json.Unmarshal(body[field], &keys) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := make([]string, 0, len(keys))
	for _, k := range keys {
		got = append(got, k.ID)
	}
	var ids []string
	if want != "" {
		for _, name := range strings.Split(want, ",") {
			ids = append(ids, p.w.IDs[strings.TrimSpace(name)])
		}
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		return fmt.Errorf("%w: the keys are %v, want %s %v", harness.ErrStep, got, want, ids)
	}
	return nil
}
