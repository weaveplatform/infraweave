package api

import (
	"errors"
	"testing"
)

func TestUnitParseSubject(t *testing.T) {
	for in, want := range map[string]Subject{
		"account":            {Kind: SubjectAccount},
		"any":                {Kind: SubjectAny},
		"organization":       {Kind: SubjectOrganization},
		"list":               {Kind: SubjectList},
		"path:stackId:stack": {Kind: SubjectPath, Param: "stackId", Entity: "stack"},
		"body:input.space":   {Kind: SubjectBody, Field: "input.space", Entity: EntitySpace},
		"body:stackId:stack": {Kind: SubjectBody, Field: "stackId", Entity: "stack"},
		"query:space":        {Kind: SubjectQuery, Param: "space", Entity: EntitySpace},
		"query:id:stack":     {Kind: SubjectQuery, Param: "id", Entity: "stack"},
	} {
		got, err := ParseSubject(in)
		if err != nil || got != want {
			t.Errorf("ParseSubject(%q) = %+v, %v; want %+v", in, got, err, want)
		}
		if got.Names() != (want.Kind == SubjectPath || want.Kind == SubjectBody ||
			want.Kind == SubjectQuery) {
			t.Errorf("%q Names = %t", in, got.Names())
		}
	}
	for _, in := range []string{
		"", "space", "account:x", "path:stackId", "path::stack", "path:a:b:c", "body:", "body:a:b:c",
		"query", "query:", "query:a:b:c",
	} {
		if _, err := ParseSubject(in); !errors.Is(err, ErrSubject) {
			t.Errorf("ParseSubject(%q) = %v, want ErrSubject", in, err)
		}
	}
}
