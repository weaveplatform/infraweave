package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/cucumber/godog"
)

// errorSteps check the answers the route table gives.
//
// ref: decision 0007
type errorSteps struct{ s *servedSteps }

func (e *errorSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answers for "([^"]+)" and "([^"]+)" are one problem but for the ID$`, e.same)
	sc.Step(`^each item's (\w+) is '([^']*)'$`, e.eachItem)
}

// eachItem checks the field name of every item of the last answer, a
// page, against want, as JSON.
func (e *errorSteps) eachItem(name, want string) error {
	var page struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	var w any
	r := e.s.last()
	if json.Unmarshal(r.body, &page) != nil || len(page.Items) == 0 ||
		json.Unmarshal([]byte(want), &w) != nil {
		return fmt.Errorf("%w: no items to check: %d %s", errStep, r.status, r.body)
	}
	for _, item := range page.Items {
		var g any
		if json.Unmarshal(item[name], &g) != nil || !reflect.DeepEqual(g, w) {
			return fmt.Errorf("%w: an item's %s is %s, not %s", errStep, name, item[name], want)
		}
	}
	return nil
}

// same checks that the last two answers, of requests naming first and
// second, differ only where each names its own ID and in the instance
// that names each request, so a caller learns nothing from which it got.
func (e *errorSteps) same(first, second string) error {
	if len(e.s.sent) < 2 {
		return fmt.Errorf("%w: fewer than two answers", errStep)
	}
	a, b := e.s.sent[len(e.s.sent)-2], e.s.sent[len(e.s.sent)-1]
	pa, pb := problemOf(a.body, first), problemOf(b.body, second)
	if a.status != b.status || pa == nil || !reflect.DeepEqual(pa, pb) {
		return fmt.Errorf("%w: %d %s and %d %s differ", errStep, a.status, a.body, b.status, b.body)
	}
	return nil
}

// problemOf is the problem body with id written {id} and no instance, or
// nil for a body that is not a JSON object.
func problemOf(body []byte, id string) map[string]any {
	var p map[string]any
	if json.Unmarshal(bytes.ReplaceAll(body, []byte(id), []byte("{id}")), &p) != nil {
		return nil
	}
	delete(p, "instance")
	return p
}
