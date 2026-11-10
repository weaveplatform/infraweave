package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// configSteps read the config elements an answer carries.
type configSteps struct{ o *orgSteps }

func (c *configSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's config lists the elements "([^"]*)"$`, c.lists)
	sc.Step(
		`^the stack "([^"]+)" read through ([AB]) with token "(\w+)" eventually attaches the contexts "([^"]*)"$`,
		c.attaches,
	)
	sc.Step(
		`^the problem refuses the field "([^"]+)" from (\d+):(\d+) to (\d+):(\d+)$`,
		c.refusesRange,
	)
	sc.Step(
		`^no answer of the scenario, log line of A or B, audit entry or event holds "([a-z0-9-]+)"$`,
		c.nowhere,
	)
}

// nowhere looks for value in every answer of the scenario, everything
// either server has logged, and every audit entry and event as text, so a
// secret is found whichever field of them would carry it. The value is
// letters, digits and dashes, so it needs no quoting inside the SQL.
func (c *configSteps) nowhere(ctx context.Context, value string) error {
	s := c.o.a.s
	for i, r := range s.sent {
		if bytes.Contains(r.body, []byte(value)) {
			return fmt.Errorf("%w: answer %d holds %q: %s", errStep, i+1, value, r.body)
		}
	}
	for i, srv := range s.w.servers {
		if bytes.Contains(srv.Stdout(), []byte(value)) {
			return fmt.Errorf("%w: server %d logged %q", errStep, i+1, value)
		}
	}
	out, err := s.query(ctx, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM audit_trail_entries e WHERE strpos(e::text, '%[1]s') > 0)
		+ (SELECT count(*) FROM organization_audit_trail_entries e WHERE strpos(e::text, '%[1]s') > 0)
		+ (SELECT count(*) FROM events e WHERE strpos(e::text, '%[1]s') > 0)`, value))
	if err != nil {
		return err
	}
	if n, err := strconv.Atoi(strings.TrimSpace(out)); err != nil || n != 0 {
		return fmt.Errorf(
			"%w: %s audit entries and events hold %q",
			errStep,
			strings.TrimSpace(out),
			value,
		)
	}
	return nil
}

// refusesRange checks the last answer, a problem, for an error at pointer
// whose range runs from one line and column up to another.
func (c *configSteps) refusesRange(pointer string, l1, c1, l2, c2 int) error {
	type position struct {
		Line   int `json:"line"`
		Column int `json:"column"`
	}
	var p struct {
		Errors []struct {
			Pointer string `json:"pointer"`
			Range   *struct {
				Start position `json:"start"`
				End   position `json:"end"`
			} `json:"range"`
		} `json:"errors"`
	}
	r := c.o.a.s.last()
	if json.Unmarshal(r.body, &p) == nil {
		for _, e := range p.Errors {
			if e.Pointer == pointer && e.Range != nil && e.Range.Start == (position{l1, c1}) &&
				e.Range.End == (position{l2, c2}) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no error at %s from %d:%d to %d:%d: %d %s",
		errStep, pointer, l1, c1, l2, c2, r.status, r.body)
}

// attaches waits for the stack slug, read through replica, to attach the
// contexts want, comma-separated, in the stack view's order, each it
// attaches automatically marked "auto:".
func (c *configSteps) attaches(ctx context.Context, slug, replica, tok, want string) error {
	w := []string{}
	if want != "" {
		w = strings.Split(want, ", ")
	}
	var got []string
	err := Eventually(
		ctx,
		"stack "+slug+" to attach "+want,
		func(ctx context.Context) (bool, error) {
			r, err := c.o.a.do(ctx, replica, tok, http.MethodGet, "/api/v1/stacks/"+slug, nil)
			if err != nil {
				return false, err
			}
			var v struct {
				AttachedContexts []struct {
					ContextID      string `json:"contextId"`
					IsAutoattached bool   `json:"isAutoattached"`
				} `json:"attachedContexts"`
			}
			if err := json.Unmarshal(r.body, &v); err != nil || r.status != http.StatusOK {
				return false, fmt.Errorf("%w: not a stack: %d %s", errStep, r.status, r.body)
			}
			got = []string{}
			for _, a := range v.AttachedContexts {
				if a.IsAutoattached {
					got = append(got, "auto:"+a.ContextID)
				} else {
					got = append(got, a.ContextID)
				}
			}
			return reflect.DeepEqual(got, w), nil
		},
	)
	if err != nil {
		return fmt.Errorf("%w (last %v)", err, got)
	}
	return nil
}

// lists checks the last answer, a stack read or created, for the IDs of its own config
// elements, comma-separated, in order.
func (c *configSteps) lists(want string) error {
	var v struct {
		Config []struct {
			ID string `json:"id"`
		} `json:"config"`
	}
	r := c.o.a.s.last()
	if err := json.Unmarshal(
		r.body,
		&v,
	); err != nil ||
		r.status != http.StatusOK && r.status != http.StatusCreated {
		return fmt.Errorf("%w: not a stack: %d %s", errStep, r.status, r.body)
	}
	got := []string{}
	for _, e := range v.Config {
		got = append(got, e.ID)
	}
	w := []string{}
	if want != "" {
		w = strings.Split(want, ", ")
	}
	if !reflect.DeepEqual(got, w) {
		return fmt.Errorf("%w: the config lists %v, want %v", errStep, got, w)
	}
	return nil
}
