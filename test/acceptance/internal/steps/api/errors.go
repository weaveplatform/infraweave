package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// errorSteps check the answers the route table gives.
//
// ref: decision 0007
type errorSteps struct {
	w *harness.World
}

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
	r := e.w.Last()
	if json.Unmarshal(r.Body, &page) != nil || len(page.Items) == 0 ||
		json.Unmarshal([]byte(want), &w) != nil {
		return fmt.Errorf("%w: no items to check: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	for _, item := range page.Items {
		var g any
		if json.Unmarshal(item[name], &g) != nil || !reflect.DeepEqual(g, w) {
			return fmt.Errorf(
				"%w: an item's %s is %s, not %s",
				harness.ErrStep,
				name,
				item[name],
				want,
			)
		}
	}
	return nil
}

// same checks that the last two answers, of requests naming first and
// second, differ only where each names its own ID and in the instance
// that names each request, so a caller learns nothing from which it got.
func (e *errorSteps) same(first, second string) error {
	if len(e.w.Sent) < 2 {
		return fmt.Errorf("%w: fewer than two answers", harness.ErrStep)
	}
	a, b := e.w.Sent[len(e.w.Sent)-2], e.w.Sent[len(e.w.Sent)-1]
	pa, pb := problemOf(a.Body, first), problemOf(b.Body, second)
	if a.Status != b.Status || pa == nil || !reflect.DeepEqual(pa, pb) {
		return fmt.Errorf(
			"%w: %d %s and %d %s differ",
			harness.ErrStep,
			a.Status,
			a.Body,
			b.Status,
			b.Body,
		)
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
