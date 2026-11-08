package iactest

import (
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// fake is a vendor whose answers a test chooses.
type fake struct {
	kind     iac.Kind
	validate func(json.RawMessage) []kernel.FieldError
}

func (f fake) Kind() iac.Kind { return f.kind }

func (f fake) ValidateSettings(s json.RawMessage) []kernel.FieldError { return f.validate(s) }

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
	if got := Problems(v, cases); len(got) != 0 {
		t.Errorf("Problems = %q, want none", got)
	}
	Run(t, v, cases)
}

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
