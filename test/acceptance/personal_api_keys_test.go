package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/cucumber/godog"
)

// personalSteps carry the keys the Personal API Keys tag answers into
// keySteps, so its exchange steps reach them, and name them for
// orgSteps' request paths.
type personalSteps struct {
	o *orgSteps
	k *keySteps
}

func (p *personalSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's key is the key "(\w+)"$`, p.keep)
	sc.Step(`^the home account's member is "(\w+)"$`, func(name string) error {
		p.o.ids[name] = memberID
		return nil
	})
	sc.Step(`^the answer is '([^']*)'$`, p.is)
	sc.Step(`^the answer lists the keys "([^"]*)"$`, p.lists)
}

// keep names the last answer's key, with the secret only its creation
// answers.
func (p *personalSteps) keep(name string) error {
	var v struct {
		ID     string  `json:"id"`
		Secret *string `json:"secret"`
	}
	r := p.o.a.s.last()
	if json.Unmarshal(r.body, &v) != nil || v.ID == "" || v.Secret == nil {
		return fmt.Errorf("%w: the answer is no new key: %d %s", errStep, r.status, r.body)
	}
	p.k.keys[name] = &createdKey{id: v.ID, secret: *v.Secret, first: *v.Secret}
	p.o.ids[name] = v.ID
	return nil
}

// is checks the last answer's whole body against want, as JSON.
func (p *personalSteps) is(want string) error {
	var g, w any
	r := p.o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &g) != nil ||
		json.Unmarshal([]byte(want), &w) != nil || !reflect.DeepEqual(g, w) {
		return fmt.Errorf("%w: answered %d %s, want %s", errStep, r.status, r.body, want)
	}
	return nil
}

// lists checks the last answer, a page of keys, against the named keys,
// comma-separated, in the page's order.
func (p *personalSteps) lists(want string) error {
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	r := p.o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", errStep, r.status, r.body)
	}
	got := make([]string, 0, len(page.Items))
	for _, k := range page.Items {
		got = append(got, k.ID)
	}
	var ids []string
	if want != "" {
		for _, name := range strings.Split(want, ",") {
			ids = append(ids, p.o.ids[strings.TrimSpace(name)])
		}
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		return fmt.Errorf("%w: the keys are %v, want %s %v", errStep, got, want, ids)
	}
	return nil
}
