package plan_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
)

// TestUnitReadOutputs checks that each output of `output -json` keeps its
// value as JSON and whether it is sensitive, that no outputs read as
// none, and that a document that is not an object of outputs with values,
// or is past MaxSize, is refused.
func TestUnitReadOutputs(t *testing.T) {
	got, err := plan.ReadOutputs(strings.NewReader(`{
		"name": {"sensitive": false, "type": "string", "value": "web"},
		"password": {"sensitive": true, "type": "string", "value": "hunter2"},
		"ports": {"sensitive": false, "type": ["list", "number"], "value": [80, 443]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]plan.Output{
		"name":     {Value: []byte(`"web"`)},
		"password": {Value: []byte(`"hunter2"`), Sensitive: true},
		"ports":    {Value: []byte(`[80, 443]`)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadOutputs = %+v", got)
	}
	if got, err := plan.ReadOutputs(strings.NewReader(`{}`)); err != nil || len(got) != 0 {
		t.Errorf("no outputs: %v, %v", got, err)
	}
	for _, doc := range []string{`null`, `[]`, `{"a": 1}`, `{"a": {"sensitive": false}}`, `{`} {
		if _, err := plan.ReadOutputs(strings.NewReader(doc)); !errors.Is(err, plan.ErrMalformed) {
			t.Errorf("%s: %v", doc, err)
		}
	}
	big := `{"a": {"value": "` + strings.Repeat("x", plan.MaxSize) + `"}}`
	if _, err := plan.ReadOutputs(strings.NewReader(big)); !errors.Is(err, plan.ErrTooLarge) {
		t.Errorf("past MaxSize: %v", err)
	}
}

// TestUnitDescriptions checks that the descriptions of a plan's root
// module outputs are read by name, an output without one absent, that a
// plan with no configuration has none, and that a document that is not
// JSON, or is past MaxSize, is refused.
func TestUnitDescriptions(t *testing.T) {
	got, err := plan.Descriptions(strings.NewReader(`{
		"format_version": "1.2",
		"configuration": {"root_module": {"outputs": {
			"name": {"description": "The server's name", "expression": {}},
			"ports": {"expression": {}}
		}}}
	}`))
	if err != nil || !reflect.DeepEqual(got, map[string]string{"name": "The server's name"}) {
		t.Errorf("Descriptions = %v, %v", got, err)
	}
	if got, err := plan.Descriptions(strings.NewReader(`{}`)); err != nil || len(got) != 0 {
		t.Errorf("no configuration: %v, %v", got, err)
	}
	if _, err := plan.Descriptions(strings.NewReader(`[`)); !errors.Is(err, plan.ErrMalformed) {
		t.Errorf("not JSON: %v", err)
	}
	big := strings.Repeat(" ", plan.MaxSize+1)
	if _, err := plan.Descriptions(strings.NewReader(big)); !errors.Is(err, plan.ErrTooLarge) {
		t.Errorf("past MaxSize: %v", err)
	}
}
