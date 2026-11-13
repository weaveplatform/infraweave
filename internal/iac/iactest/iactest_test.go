package iactest

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// fake is a vendor whose answers a test chooses; it reads plans with
// plan.Parse unless parse is set.
type fake struct {
	kind     iac.Kind
	validate func(json.RawMessage) []kernel.FieldError
	parse    func(io.Reader) (plan.Plan, error)
}

func (f fake) Kind() iac.Kind { return f.kind }

func (f fake) ValidateSettings(s json.RawMessage) []kernel.FieldError { return f.validate(s) }

func (f fake) ParsePlan(r io.Reader) (plan.Plan, error) {
	if f.parse == nil {
		return plan.Parse(r)
	}
	return f.parse(r)
}

// recorded are Terraform's recorded plans, which the fake's plan.Parse
// answers as Recorded says it must.
func recorded(t *testing.T) []PlanCase {
	t.Helper()
	plans, err := Recorded("../plan/testdata/terraform")
	if err != nil {
		t.Fatal(err)
	}
	return plans
}

// empty answers every plan as an empty one.
func empty(io.Reader) (plan.Plan, error) { return plan.Plan{Changes: []plan.Change{}}, nil }

// asObject refuses settings that are not an object, as every vendor must.
func asObject(s json.RawMessage) []kernel.FieldError {
	var m map[string]any
	if json.Unmarshal(s, &m) != nil || m == nil {
		return []kernel.FieldError{{Reason: kernel.ReasonType, Message: "an object"}}
	}
	return nil
}

// TestUnitProblemsPasses checks that a vendor meeting the contract has no
// problems, and that Run reports none.
func TestUnitProblemsPasses(t *testing.T) {
	v := fake{kind: iac.Terraform, validate: func(s json.RawMessage) []kernel.FieldError {
		if errs := asObject(s); errs != nil {
			return errs
		}
		var m map[string]any
		_ = json.Unmarshal(s, &m)
		if _, ok := m["bad"]; ok {
			return []kernel.FieldError{{Pointer: "/bad", Reason: kernel.ReasonRule, Message: "bad"}}
		}
		return nil
	}}
	cases := []Case{{Name: "bad", Settings: `{"bad": 1}`, Refused: []string{"/bad"}}}
	plans := recorded(t)
	if got := Problems(v, cases, plans...); len(got) != 0 {
		t.Errorf("Problems = %q, want none", got)
	}
	Run(t, v, cases, plans...)
}

// TestUnitRecorded checks that Recorded answers its three scenarios in
// name order, and refuses a directory without them.
func TestUnitRecorded(t *testing.T) {
	cases := recorded(t)
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	if want := []string{"recorded all", "recorded none", "recorded outputs"}; !slices.Equal(
		names, want,
	) {
		t.Errorf("Recorded names %q, want %q", names, want)
	}
	if _, err := Recorded(t.TempDir()); err == nil {
		t.Error("Recorded of an empty directory = nil error")
	}
}

// TestUnitProblemsPlanFindings checks one vendor breaking each clause of
// ParsePlan's contract.
func TestUnitProblemsPlanFindings(t *testing.T) {
	var calls atomic.Int64
	for name, c := range map[string]struct {
		v     fake
		plans []PlanCase
		want  []string
	}{
		"refuses nothing": {
			v: fake{kind: iac.Terraform, validate: asObject, parse: empty},
			want: []string{
				"a plan of another major format: ParsePlan = <nil>, want " + plan.ErrFormat.Error(),
				"a plan past MaxSize: ParsePlan = <nil>, want " + plan.ErrTooLarge.Error(),
				"a plan that is not JSON: ParsePlan = <nil>, want " + plan.ErrMalformed.Error(),
			},
		},
		"misreads a plan": {
			v: fake{kind: iac.Terraform, validate: asObject},
			plans: []PlanCase{{
				Name: "c", Document: []byte(`{"format_version":"1.0"}`),
				Want: plan.Plan{Delta: plan.Delta{Outputs: true}},
			}},
			want: []string{
				"c: ParsePlan = {Changes:[] Delta:{Add:0 Change:0 Delete:0 Import:0 Move:0 " +
					"Forget:0 Resources:0 Outputs:false}}, <nil>, want {Changes:[] Delta:{Add:0 " +
					"Change:0 Delete:0 Import:0 Move:0 Forget:0 Resources:0 Outputs:true}}",
			},
		},
		"keeps state between plans": {
			v: fake{kind: iac.Terraform, validate: asObject, parse: func(
				r io.Reader,
			) (plan.Plan, error) {
				if calls.Add(1) > int64(len(planContract)) {
					return empty(r)
				}
				return plan.Parse(r)
			}},
			want: []string{
				"a plan of another major format: a concurrent call answered otherwise than a " +
					"call alone",
				"a plan past MaxSize: ParsePlan = <nil>, want " + plan.ErrTooLarge.Error(),
				"a plan that is not JSON: a concurrent call answered otherwise than a call alone",
			},
		},
		"refuses with another error": {
			v: fake{kind: iac.Terraform, validate: asObject, parse: func(io.Reader) (
				plan.Plan, error,
			) {
				return plan.Plan{}, errOther
			}},
			plans: []PlanCase{{Name: "c", Document: []byte(`{`), Err: plan.ErrMalformed}},
			want: []string{
				"a plan of another major format: ParsePlan = other, want " +
					plan.ErrFormat.Error(),
				"a plan past MaxSize: ParsePlan = other, want " + plan.ErrTooLarge.Error(),
				"a plan that is not JSON: ParsePlan = other, want " + plan.ErrMalformed.Error(),
				"an empty plan: ParsePlan = {Changes:[] Delta:{Add:0 Change:0 Delete:0 " +
					"Import:0 Move:0 Forget:0 Resources:0 Outputs:false}}, other, want " +
					"{Changes:[] Delta:{Add:0 Change:0 Delete:0 Import:0 Move:0 Forget:0 " +
					"Resources:0 Outputs:false}}",
				"c: ParsePlan = other, want " + plan.ErrMalformed.Error(),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			want := slices.Sorted(slices.Values(c.want))
			if got := Problems(c.v, nil, c.plans...); !slices.Equal(got, want) {
				t.Errorf("Problems =\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// errOther is an error no plan case names.
var errOther = errors.New("other")

// TestUnitProblemsFindings checks one vendor breaking each clause.
func TestUnitProblemsFindings(t *testing.T) {
	var calls atomic.Int64
	for name, c := range map[string]struct {
		v     fake
		cases []Case
		want  []string
	}{
		"unregistered": {
			v:    fake{kind: "pulumi", validate: asObject},
			want: []string{`kind "pulumi" is not registered`},
		},
		"accepts everything": {
			v: fake{kind: iac.OpenTofu, validate: func(json.RawMessage) []kernel.FieldError { return nil }},
			want: []string{
				`a string: refused [], want [""]`,
				`an array: refused [], want [""]`,
				`not JSON: refused [], want [""]`,
			},
		},
		"a bad refusal": {
			v: fake{kind: iac.OpenTofu, validate: func(s json.RawMessage) []kernel.FieldError {
				if errs := asObject(s); errs != nil {
					return errs
				}
				return []kernel.FieldError{
					{Pointer: "/a", Reason: "wrong", Message: "m"},
					{Pointer: "/a", Reason: kernel.ReasonRule},
					{Pointer: "/gone", Reason: kernel.ReasonRule, Message: "m"},
				}
			}},
			cases: []Case{{Name: "c", Settings: `{"a": 1}`, Refused: []string{"/a", "/a", "/gone"}}},
			want: []string{
				`c: /a has no message`,
				`c: /a's reason "wrong" is not one clients branch on`,
				`c: /gone names no field of the settings`,
				`no settings: /a has no message`,
				`no settings: /a's reason "wrong" is not one clients branch on`,
				`no settings: /gone names no field of the settings`,
				`no settings: refused ["/a" "/a" "/gone"], want []`,
			},
		},
		"changes its input": {
			v: fake{kind: iac.Terraform, validate: func(s json.RawMessage) []kernel.FieldError {
				errs := asObject(s)
				if len(s) > 0 {
					s[0] = ' '
				}
				return errs
			}},
			want: []string{
				"a string: ValidateSettings changed its input",
				"an array: ValidateSettings changed its input",
				"no settings: ValidateSettings changed its input",
				"not JSON: ValidateSettings changed its input",
			},
		},
		"keeps state between calls": {
			v: fake{kind: iac.Terraform, validate: func(s json.RawMessage) []kernel.FieldError {
				if errs := asObject(s); errs != nil {
					return errs
				}
				if calls.Add(1) > 1 {
					return []kernel.FieldError{{Reason: kernel.ReasonRule, Message: "again"}}
				}
				return nil
			}},
			want: []string{"no settings: a concurrent call answered otherwise than a call alone"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Problems(c.v, c.cases); !slices.Equal(got, c.want) {
				t.Errorf("Problems =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestUnitResolves(t *testing.T) {
	const doc = `{"a": {"b/c": [1, {"d~e": 2}]}, "n": null}`
	for pointer, want := range map[string]bool{
		"":               true,
		"/a":             true,
		"/n":             true,
		"/a/b~1c/0":      true,
		"/a/b~1c/1/d~0e": true,
		"a":              false,
		"/x":             false,
		"/a/b~1c/2":      false,
		"/a/b~1c/-1":     false,
		"/a/b~1c/x":      false,
		"/a/b~1c/0/z":    false,
	} {
		if got := resolves(doc, pointer); got != want {
			t.Errorf("resolves(%q) = %v, want %v", pointer, got, want)
		}
	}
	if resolves(`{`, "/a") {
		t.Error("resolves in a document that is not JSON = true")
	}
}
