package plan_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
)

// TestUnitReadOutputs checks that each output of `output -json` keeps its
// value as JSON and whether it is sensitive, a sensitive one with no
// value reading with none, that no outputs read as none, and that a
// document that is not an object of outputs, each that is not sensitive
// with a value, or is past MaxSize, is refused.
func TestUnitReadOutputs(t *testing.T) {
	got, err := plan.ReadOutputs(strings.NewReader(`{
		"name": {"sensitive": false, "type": "string", "value": "web"},
		"password": {"sensitive": true, "type": "string", "value": "hunter2"},
		"ports": {"sensitive": false, "type": ["list", "number"], "value": [80, 443]},
		"token": {"sensitive": true, "type": "string"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]plan.Output{
		"name":     {Value: []byte(`"web"`)},
		"password": {Value: []byte(`"hunter2"`), Sensitive: true},
		"ports":    {Value: []byte(`[80, 443]`)},
		"token":    {Sensitive: true},
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

// TestUnitWithoutSensitiveValues checks that each sensitive output loses
// its value and keeps every other field, that every other output is kept
// as written, HTML's characters among them, that ReadOutputs reads the
// result, and that a document that is not an object of outputs, an
// output whose sensitive is not a boolean, one past MaxSize, and a read
// or write that fails are refused.
func TestUnitWithoutSensitiveValues(t *testing.T) {
	var w bytes.Buffer
	err := plan.WithoutSensitiveValues(&w, strings.NewReader(`{
		"html": {"sensitive": false, "type": "string", "value": "<b>&amp;</b>"},
		"password": {"sensitive": true, "type": "string", "value": "hunter2"},
		"ports": {"type": ["list", "number"], "value": [80, 443]},
		"none": {"sensitive": true, "type": "string", "value": null}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(w.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		"html":     {"sensitive": false, "type": "string", "value": "<b>&amp;</b>"},
		"password": {"sensitive": true, "type": "string"},
		"ports":    {"type": []any{"list", "number"}, "value": []any{80.0, 443.0}},
		"none":     {"sensitive": true, "type": "string"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WithoutSensitiveValues = %s", w.Bytes())
	}
	if bytes.Contains(w.Bytes(), []byte("hunter2")) || !bytes.Contains(w.Bytes(), []byte("<b>")) {
		t.Errorf("WithoutSensitiveValues = %s", w.Bytes())
	}
	outs, err := plan.ReadOutputs(&w)
	if err != nil || outs["password"].Value != nil || !outs["password"].Sensitive {
		t.Errorf("ReadOutputs = %+v, %v", outs, err)
	}
	for _, doc := range []string{`null`, `[]`, `{"a": 1}`, `{"a": {"sensitive": "yes"}}`, `{`} {
		err := plan.WithoutSensitiveValues(io.Discard, strings.NewReader(doc))
		if !errors.Is(err, plan.ErrMalformed) {
			t.Errorf("%s: %v", doc, err)
		}
	}
	big := `{"a": {"value": "` + strings.Repeat("x", plan.MaxSize) + `"}}`
	if err := plan.WithoutSensitiveValues(io.Discard, strings.NewReader(big)); !errors.Is(err,
		plan.ErrTooLarge) {
		t.Errorf("past MaxSize: %v", err)
	}
	if err := plan.WithoutSensitiveValues(iotest{}, strings.NewReader(`{}`)); !errors.Is(err,
		errWrite) {
		t.Errorf("a failing write: %v", err)
	}
	if err := plan.WithoutSensitiveValues(io.Discard, iotest{}); !errors.Is(err, errRead) {
		t.Errorf("a failing read: %v", err)
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
