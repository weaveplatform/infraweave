package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// answerSteps are the steps every area's scenarios send requests and
// check answers with: a request sent through either server by the
// browser or with a named token, or with a body of a given size, and the
// last answer's status, problem and members.
type answerSteps struct{ w *harness.World }

func (a *answerSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^(the browser|token "\w+") sends "([A-Z]+) ([^"]+)"(?: with '([^']*)')? through ([AB])$`,
		a.w.SendAs,
	)
	sc.Step(`^token "(\w+)" sends "([A-Z]+) ([^"]+)" with a body of (\d+) bytes through ([AB])$`,
		a.sized)
	sc.Step(`^it answers (\d+)$`, a.answers)
	sc.Step(`^it answers a (\d+) problem "([a-z-]+)"$`, a.answersProblem)
	sc.Step(`^the answer's (\w+) is '([^']*)'$`, a.member)
	sc.Step(`^the answer leaves out "([^"]+)"$`, a.leavesOut)
}

// sized sends a JSON object of exactly size bytes, whose one member no
// schema names, with the named token.
func (a *answerSteps) sized(
	ctx context.Context,
	name, method, path string,
	size int,
	replica string,
) error {
	const frame = len(`{"x":""}`)
	if size < frame {
		return fmt.Errorf("%w: a body of %d bytes is under %d", harness.ErrStep, size, frame)
	}
	body := json.RawMessage(`{"x":"` + strings.Repeat("x", size-frame) + `"}`)
	r, err := a.w.Do(ctx, replica, name, method, a.w.Resolve(path), body)
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}

func (a *answerSteps) answers(code int) error {
	r := a.w.Last()
	if r.Status != code {
		return fmt.Errorf("%w: answered %d, want %d: %s", harness.ErrStep, r.Status, code, r.Body)
	}
	return nil
}

func (a *answerSteps) answersProblem(code, slug string) error {
	return harness.IsProblem(a.w.Last(), "the request", code, slug)
}

// member checks the last answer's top-level member name against want as
// JSON, with each {name} resolved.
func (a *answerSteps) member(name, want string) error {
	var got map[string]json.RawMessage
	var g, w any
	r := a.w.Last()
	if json.Unmarshal(r.Body, &got) != nil || json.Unmarshal(got[name], &g) != nil ||
		json.Unmarshal([]byte(a.w.Resolve(want)), &w) != nil || !reflect.DeepEqual(g, w) {
		return fmt.Errorf("%w: %s is not %s: %d %s", harness.ErrStep, name, want, r.Status, r.Body)
	}
	return nil
}

// leavesOut checks that the last answer names nothing of text, such as
// another organization's account.
func (a *answerSteps) leavesOut(text string) error {
	r := a.w.Last()
	if r.Status != http.StatusOK || strings.Contains(string(r.Body), text) {
		return fmt.Errorf("%w: the answer names %q: %d %s", harness.ErrStep, text, r.Status, r.Body)
	}
	return nil
}
