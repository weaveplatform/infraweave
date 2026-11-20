package policies

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerManagement(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the policy inheritance flags are "([^"]*)"$`, func(want string) error {
		var out struct {
			Edges []struct {
				Inherited bool `json:"inherited"`
			} `json:"edges"`
		}
		if err := json.Unmarshal(w.Last().Body, &out); err != nil {
			return fmt.Errorf("decode inheritance flags: %w", err)
		}
		got := []string{}
		for _, edge := range out.Edges {
			got = append(got, fmt.Sprint(edge.Inherited))
		}
		if strings.Join(got, ", ") != want {
			return fmt.Errorf("%w: inherited flags %v, want %s", harness.ErrStep, got, want)
		}
		return nil
	})
	sc.Step(`^the policy search field "([^"]*)" is '(.*)'$`, func(name, want string) error {
		var out struct {
			Fields []map[string]any `json:"fields"`
		}
		var expected map[string]any
		if err := json.Unmarshal(w.Last().Body, &out); err != nil {
			return fmt.Errorf("decode search fields: %w", err)
		}
		if err := json.Unmarshal([]byte(w.Resolve(want)), &expected); err != nil {
			return fmt.Errorf("decode expected field: %w", err)
		}
		for _, field := range out.Fields {
			if field["name"] == name && reflect.DeepEqual(field, expected) {
				return nil
			}
		}
		return fmt.Errorf("%w: field %s does not match %s: %s", harness.ErrStep, name, want, w.Last().Body)
	})
	sc.Step(`^the policy page IDs are "([^"]*)"$`, func(want string) error {
		var out struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Edges []struct {
				Node struct {
					ID string `json:"id"`
				} `json:"node"`
			} `json:"edges"`
		}
		if err := json.Unmarshal(w.Last().Body, &out); err != nil || w.Last().Status != 200 {
			return fmt.Errorf("%w: policy page is not a successful response: %s", harness.ErrStep, w.Last().Body)
		}
		got := []string{}
		for _, item := range out.Items {
			got = append(got, item.ID)
		}
		for _, edge := range out.Edges {
			got = append(got, edge.Node.ID)
		}
		expected := []string{}
		if want != "" {
			expected = strings.Split(w.Resolve(want), ", ")
		}
		if !reflect.DeepEqual(got, expected) {
			return fmt.Errorf("%w: policy IDs %v, want %v", harness.ErrStep, got, expected)
		}
		return nil
	})
	sc.Step(`^the policy refusal locates the body source$`, func() error {
		var problem struct {
			Errors []struct {
				Pointer string `json:"pointer"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
				Range   *struct {
					Start sourcePosition `json:"start"`
					End   sourcePosition `json:"end"`
				} `json:"range"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(w.Last().Body, &problem); err != nil {
			return fmt.Errorf("decode policy refusal: %w", err)
		}
		for _, field := range problem.Errors {
			if field.Pointer != "/body" || field.Reason != "rule" || field.Message == "" || field.Range == nil {
				continue
			}
			start, end := field.Range.Start, field.Range.End
			if start.Line > 0 && start.Column > 0 && (end.Line > start.Line || (end.Line == start.Line && end.Column > start.Column)) {
				return nil
			}
		}
		return fmt.Errorf("%w: policy refusal lacks a source field/range: %s", harness.ErrStep, w.Last().Body)
	})
}

type sourcePosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
