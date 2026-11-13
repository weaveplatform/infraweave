package plan_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/iactest"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
)

// planned is a planned change of address's resource of type t.
func planned(address string, t plan.Type) plan.Change {
	return plan.Change{
		Address: address, Metadata: plan.Metadata{Result: plan.ResultPlanned, Type: t},
	}
}

// moved is planned's change of a resource that moved from from.
func moved(address, from string, t plan.Type) plan.Change {
	c := planned(address, t)
	c.PreviousAddress, c.Metadata.Moved = &from, true
	return c
}

// changes is a format 1.2 document of resource changes, the JSON array's
// members.
func changes(members string) string {
	return `{"format_version":"1.2","resource_changes":[` + members + `]}`
}

// parsed are documents Parse reads, with the plan each must answer.
var parsed = map[string]struct {
	doc  string
	want plan.Plan
}{
	"a resource created after its object is forgotten": {
		doc: changes(`{"address":"a.b","mode":"managed","change":{"actions":["create","forget"]}}`),
		want: plan.Plan{
			Changes: []plan.Change{planned("a.b", plan.TypeCreateAfterForgotten)},
			Delta:   plan.Delta{Add: 1, Forget: 1},
		},
	},
	"an import that also updates": {
		doc: changes(`{"address":"a.b","mode":"managed",
			"change":{"actions":["update"],"importing":{"id":"x"}}}`),
		want: plan.Plan{
			Changes: []plan.Change{planned("a.b", plan.TypeImport)},
			Delta:   plan.Delta{Change: 1, Import: 1},
		},
	},
	"an importing of null": {
		doc: changes(`{"address":"a.b","mode":"managed",
			"change":{"actions":["update"],"importing":null}}`),
		want: plan.Plan{
			Changes: []plan.Change{planned("a.b", plan.TypeChange)},
			Delta:   plan.Delta{Change: 1},
		},
	},
	"a previous address that is its own": {
		doc: changes(`{"address":"a.b","previous_address":"a.b","mode":"managed",
			"change":{"actions":["no-op"]}}`),
		want: plan.Plan{Changes: []plan.Change{planned("a.b", plan.TypeNoop)}},
	},
	"a resource moved into a module and changed": {
		doc: changes(`{"address":"module.m.a.b","previous_address":"a.b","mode":"managed",
			"change":{"actions":["update"]}}`),
		want: plan.Plan{
			Changes: []plan.Change{moved("module.m.a.b", "a.b", plan.TypeChange)},
			Delta:   plan.Delta{Change: 1, Move: 1},
		},
	},
	"data sources, which are listed but never counted": {
		doc: changes(`{"address":"data.a.b","mode":"data","change":{"actions":["read"]}},
			{"address":"data.a.c","previous_address":"data.a.d","mode":"data",
			"change":{"actions":["read"],"importing":{"id":"x"}}}`),
		want: plan.Plan{Changes: []plan.Change{
			planned("data.a.b", plan.TypeRead), moved("data.a.c", "data.a.d", plan.TypeImport),
		}},
	},
	"managed resources in modules below the root": {
		doc: `{"format_version":"1.2","prior_state":{"values":{"root_module":{
			"resources":[{"address":"a.b","mode":"managed"},{"address":"data.a.c","mode":"data"}],
			"child_modules":[{"resources":[{"address":"module.m.a.b","mode":"managed"}],
			"child_modules":[{"resources":[{"address":"module.m.module.n.a.b","mode":"managed"},
			{"address":"module.m.module.n.a.c","mode":"managed"}]}]}]}}}}`,
		want: plan.Plan{Changes: []plan.Change{}, Delta: plan.Delta{Resources: 4}},
	},
	"a prior state without values": {
		doc:  `{"format_version":"1.2","prior_state":{"format_version":"1.0"}}`,
		want: plan.Plan{Changes: []plan.Change{}},
	},
	"an output created": {
		doc: `{"format_version":"1.2","output_changes":{"a":{"actions":["no-op"]},
			"b":{"actions":["create"]}}}`,
		want: plan.Plan{Changes: []plan.Change{}, Delta: plan.Delta{Outputs: true}},
	},
	"outputs unchanged": {
		doc: `{"format_version":"1.2","output_changes":{"a":{"actions":["no-op"]},
			"b":{"actions":["no-op"]}}}`,
		want: plan.Plan{Changes: []plan.Change{}},
	},
	"a minor format version with members this build does not know": {
		doc:  `{"format_version":"1.99","checks":[],"future":{"x":1}}`,
		want: plan.Plan{Changes: []plan.Change{}},
	},
	"a format version of one segment": {
		doc:  `{"format_version":"1"}`,
		want: plan.Plan{Changes: []plan.Change{}},
	},
}

// refused are documents Parse refuses, with the error it must refuse each
// with.
var refused = map[string]struct {
	doc  string
	want error
}{
	"not JSON":                 {`{`, plan.ErrMalformed},
	"more than one JSON value": {`{"format_version":"1.2"} {}`, plan.ErrMalformed},
	"a member of the wrong type": {
		`{"format_version":"1.2","resource_changes":{}}`,
		plan.ErrMalformed,
	},
	"a change without an address": {
		changes(`{"mode":"managed","change":{"actions":["create"]}}`), plan.ErrMalformed,
	},
	"no format version":             {`{}`, plan.ErrFormat},
	"null":                          {`null`, plan.ErrFormat},
	"another major format version":  {`{"format_version":"2.0"}`, plan.ErrFormat},
	"a major version of two digits": {`{"format_version":"10.0"}`, plan.ErrFormat},
	"an action list it does not know": {
		changes(`{"address":"a.b","mode":"managed","change":{"actions":["create","update"]}}`),
		plan.ErrFormat,
	},
	"no actions": {
		changes(`{"address":"a.b","mode":"managed","change":{"actions":[]}}`), plan.ErrFormat,
	},
}

// TestUnitPlanChanges checks each tool's recorded plans against the
// plans their scenarios must parse to.
func TestUnitPlanChanges(t *testing.T) {
	for _, tool := range []string{"terraform", "tofu"} {
		cases, err := iactest.Recorded(filepath.Join("testdata", tool))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			got, err := plan.Parse(bytes.NewReader(c.Document))
			if err != nil || !reflect.DeepEqual(got, c.Want) {
				t.Errorf("%s %s: Parse = %+v, %v, want %+v", tool, c.Name, got, err, c.Want)
			}
			roundTrip(t, c.Document)
		}
	}
}

func TestUnitParse(t *testing.T) {
	for name, c := range parsed {
		got, err := plan.Parse(strings.NewReader(c.doc))
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Parse = %+v, %v, want %+v", name, got, err, c.want)
		}
		roundTrip(t, []byte(c.doc))
	}
	for name, c := range refused {
		if _, err := plan.Parse(strings.NewReader(c.doc)); !errors.Is(err, c.want) {
			t.Errorf("%s: Parse = %v, want %v", name, err, c.want)
		}
		roundTrip(t, []byte(c.doc))
	}
	if _, err := plan.Parse(iotest{}); !errors.Is(err, errRead) {
		t.Errorf("Parse of a failing reader = %v, want %v", err, errRead)
	}
}

// TestUnitMaxSize checks that Parse reads a document of MaxSize bytes, and
// that Parse and Read refuse one byte more.
func TestUnitMaxSize(t *testing.T) {
	head, tail := `{"format_version":"1.2","pad":"`, `"}`
	at := head + strings.Repeat("a", plan.MaxSize-len(head)-len(tail)) + tail
	if _, err := plan.Parse(strings.NewReader(at)); err != nil {
		t.Errorf("Parse of MaxSize bytes = %v", err)
	}
	past := io.MultiReader(strings.NewReader(at), strings.NewReader(" "))
	if _, err := plan.Parse(past); !errors.Is(err, plan.ErrTooLarge) {
		t.Errorf("Parse of MaxSize+1 bytes = %v, want %v", err, plan.ErrTooLarge)
	}
	past = io.MultiReader(strings.NewReader(at), strings.NewReader(" "))
	if _, err := plan.Read(past); !errors.Is(err, plan.ErrTooLarge) {
		t.Errorf("Read of MaxSize+1 bytes = %v, want %v", err, plan.ErrTooLarge)
	}
}

func TestUnitWrite(t *testing.T) {
	var b bytes.Buffer
	if err := plan.Write(&b, nil); err != nil || b.String() != `{"version":1,"changes":[]}`+"\n" {
		t.Errorf("Write of no changes = %v, wrote %s", err, b.String())
	}
	b.Reset()
	changes := []plan.Change{
		moved("a.new", "a.old", plan.TypeNoop), planned("a.b", plan.TypeAdd),
	}
	const want = `{"version":1,"changes":[` +
		`{"address":"a.new","previousAddress":"a.old",` +
		`"metadata":{"result":"PLANNED","type":"NOOP","moved":true}},` +
		`{"address":"a.b","metadata":{"result":"PLANNED","type":"ADD","moved":false}}]}` + "\n"
	if err := plan.Write(&b, changes); err != nil || b.String() != want {
		t.Errorf("Write = %v, wrote\n%s\nwant\n%s", err, b.String(), want)
	}
	if err := plan.Write(iotest{}, changes); !errors.Is(err, errWrite) {
		t.Errorf("Write to a failing writer = %v, want %v", err, errWrite)
	}
}

// documents are changes documents Read reads, with the changes each must
// answer.
var documents = map[string][]plan.Change{
	`{"version":1,"changes":[]}`:   {},
	`{"version":1}`:                {},
	`{"version":1,"changes":null}`: {},
	` {"version":1,"changes":[{"address":"a.b","metadata":{"result":"PLANNED","type":"ADD"}}]}` +
		"\n": {planned("a.b", plan.TypeAdd)},
}

// unread are changes documents Read refuses, with the error it must refuse
// each with.
var unread = map[string]error{
	`{`:                             plan.ErrMalformed,
	`{"version":1,"changes":[]} {}`: plan.ErrMalformed,
	`{"version":1,"changes":[]} x`:  plan.ErrMalformed,
	`{"version":1,"extra":true}`:    plan.ErrMalformed,
	`{"version":1,"changes":[{"address":"a.b","metadata":{"result":"PLANNED","type":"ADD",` +
		`"x":1}}]}`: plan.ErrMalformed,
	`{"version":1,"changes":[{"metadata":{"result":"PLANNED","type":"ADD"}}]}`:                     plan.ErrMalformed,
	`{"version":1,"changes":[{"address":"a.b","metadata":{"result":"PLANNED","type":"UPGRADE"}}]}`: plan.ErrMalformed,
	`{"version":1,"changes":[{"address":"a.b","metadata":{"result":"APPLIED","type":"ADD"}}]}`:     plan.ErrMalformed,
	`{"version":2,"changes":[]}`: plan.ErrFormat,
	`{"changes":[]}`:             plan.ErrFormat,
	`null`:                       plan.ErrFormat,
}

func TestUnitRead(t *testing.T) {
	for doc, want := range documents {
		got, err := plan.Read(strings.NewReader(doc))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Read(%s) = %+v, %v, want %+v", doc, got, err, want)
		}
		roundTrip(t, []byte(doc))
	}
	for doc, want := range unread {
		if _, err := plan.Read(strings.NewReader(doc)); !errors.Is(err, want) {
			t.Errorf("Read(%s) = %v, want %v", doc, err, want)
		}
		roundTrip(t, []byte(doc))
	}
	if _, err := plan.Read(iotest{}); !errors.Is(err, errRead) {
		t.Errorf("Read of a failing reader = %v, want %v", err, errRead)
	}
}

// roundTrip checks what every document must satisfy: Parse and Read each
// answer it or refuse it with one of the package's errors, a plan Parse
// answers counts no more imports or moves than it has changes, and its
// changes written as a changes document read back as themselves.
func roundTrip(t *testing.T, doc []byte) {
	t.Helper()
	if _, err := plan.Read(bytes.NewReader(doc)); err != nil && !refusal(err) {
		t.Fatalf("Read: %v, not one of the package's errors", err)
	}
	p, err := plan.Parse(bytes.NewReader(doc))
	if err != nil {
		if !refusal(err) {
			t.Fatalf("Parse: %v, not one of the package's errors", err)
		}
		return
	}
	if d := p.Delta; d.Import > len(p.Changes) || d.Move > len(p.Changes) ||
		min(d.Add, d.Change, d.Delete, d.Forget, d.Resources) < 0 {
		t.Fatalf("Parse answered %d changes and the delta %+v", len(p.Changes), d)
	}
	var b bytes.Buffer
	if err := plan.Write(&b, p.Changes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, err := plan.Read(&b); err != nil || !reflect.DeepEqual(got, p.Changes) {
		t.Fatalf("Read of Write's document = %+v, %v, want %+v", got, err, p.Changes)
	}
}

// refusal reports whether err is one of the errors Parse and Read refuse a
// document with.
func refusal(err error) bool {
	return errors.Is(err, plan.ErrTooLarge) || errors.Is(err, plan.ErrMalformed) ||
		errors.Is(err, plan.ErrFormat)
}

// FuzzPlan holds Parse and Read to roundTrip on any document. Its seeds are
// the recorded plans and every document the tests above name.
func FuzzPlan(f *testing.F) {
	recorded, err := filepath.Glob(filepath.Join("testdata", "*", "*.json"))
	if err != nil || len(recorded) == 0 {
		f.Fatalf("the recorded plans: %v, %d of them", err, len(recorded))
	}
	for _, path := range recorded {
		doc, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(doc)
	}
	for _, c := range parsed {
		f.Add([]byte(c.doc))
	}
	for _, c := range refused {
		f.Add([]byte(c.doc))
	}
	for doc := range documents {
		f.Add([]byte(doc))
	}
	for doc := range unread {
		f.Add([]byte(doc))
	}
	f.Fuzz(roundTrip)
}

// The errors iotest's reads and writes fail with.
var (
	errRead  = errors.New("read")
	errWrite = errors.New("write")
)

// iotest is a reader and writer whose every call fails.
type iotest struct{}

func (iotest) Read([]byte) (int, error)  { return 0, errRead }
func (iotest) Write([]byte) (int, error) { return 0, errWrite }
