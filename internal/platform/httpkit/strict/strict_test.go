package strict_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestUnitMerge runs RFC 7396's appendix A examples, and checks that
// neither argument changes.
func TestUnitMerge(t *testing.T) {
	for _, tc := range []struct{ target, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`["a","b"]`, `["c","d"]`, `["c","d"]`},
		{`{"a":"b"}`, `["c"]`, `["c"]`},
		{`{"a":"foo"}`, `null`, `null`},
		{`{"a":"foo"}`, `"bar"`, `"bar"`},
		{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
	} {
		target, patch := decode(t, tc.target), decode(t, tc.patch)
		got := strict.Merge(target, patch)
		if !reflect.DeepEqual(got, decode(t, tc.want)) {
			t.Errorf("Merge(%s, %s) = %v, want %s", tc.target, tc.patch, got, tc.want)
		}
		if !reflect.DeepEqual(target, decode(t, tc.target)) {
			t.Errorf("Merge changed its target %s", tc.target)
		}
	}
}

type input struct {
	Name   string   `json:"name"`
	Labels []string `json:"labels,omitempty"`
	Branch *string  `json:"branch,omitempty"`
}

func TestUnitPatch(t *testing.T) {
	branch := "main"
	view := input{Name: "a", Labels: []string{"x", "y"}, Branch: &branch}
	got, err := strict.Patch(view, map[string]any{"labels": []any{"z"}, "branch": nil})
	if err != nil || !reflect.DeepEqual(got, input{Name: "a", Labels: []string{"z"}}) {
		t.Errorf("Patch = %+v, %v", got, err)
	}
	if _, err := strict.Patch(view, map[string]any{"name": 1}); err == nil {
		t.Error("a patch of the wrong type decoded")
	}
	if _, err := strict.Patch(func() {}, nil); err == nil {
		t.Error("a view that is no JSON encoded")
	}
}

func TestUnitApply(t *testing.T) {
	read := func(version int64) func() (input, int64, error) {
		return func() (input, int64, error) { return input{Name: "a"}, version, nil }
	}
	var wrote []kernel.Expect
	write := func(in input, e kernel.Expect) (input, error) {
		wrote = append(wrote, e)
		return in, nil
	}
	got, err := strict.Apply(
		strict.IfMatch(new(`"4"`)),
		map[string]any{"name": "b"},
		read(4),
		write,
	)
	if err != nil || got.Name != "b" || len(wrote) != 1 || wrote[0].Check(4) != nil ||
		wrote[0].Check(5) == nil {
		t.Errorf("Apply = %+v, %v; wrote %+v", got, err, wrote)
	}
	if _, err := strict.Apply(
		strict.IfMatch(new(`"3"`)),
		nil,
		read(4),
		write,
	); !errors.Is(
		err,
		kernel.ErrStale,
	) {
		t.Errorf("a stale If-Match = %v", err)
	}
	failed := errors.New("no")
	if _, err := strict.Apply(
		kernel.Expect{},
		nil,
		func() (input, int64, error) { return input{}, 0, failed },
		write,
	); !errors.Is(err, failed) {
		t.Errorf("a failed read = %v", err)
	}
	if _, err := strict.Apply(
		kernel.Expect{},
		map[string]any{"name": 1},
		read(1),
		write,
	); err == nil {
		t.Error("a patch that does not decode was written")
	}

	races := 0
	losing := func(in input, _ kernel.Expect) (input, error) {
		races++
		return in, kernel.ErrStale
	}
	if _, err := strict.Apply(
		kernel.Expect{},
		nil,
		read(1),
		losing,
	); !errors.Is(err, kernel.ErrStale) ||
		races != 3 {
		t.Errorf("an unconditional patch that keeps losing = %v after %d writes", err, races)
	}
	races = 0
	if _, err := strict.Apply(
		kernel.ExpectAny(),
		nil,
		read(1),
		losing,
	); !errors.Is(err, kernel.ErrStale) ||
		races != 1 {
		t.Errorf("a conditional patch that loses = %v after %d writes", err, races)
	}
}

func TestUnitETagIfMatch(t *testing.T) {
	if got := strict.ETag(42); got != `"42"` {
		t.Errorf("ETag = %s", got)
	}
	for _, tc := range []struct {
		header      *string
		accepts     []int64
		refuses     []int64
		conditional bool
	}{
		{nil, []int64{1}, nil, false},
		{new(" "), []int64{1}, nil, false},
		{new("*"), []int64{1, 9}, nil, true},
		{new(`"3"`), []int64{3}, []int64{4}, true},
		{new(`"3", "5"`), []int64{3, 5}, []int64{4}, true},
		{new(`W/"3"`), nil, []int64{3}, true},
		{new(`"x", 3, "`), nil, []int64{3}, true},
	} {
		e := strict.IfMatch(tc.header)
		for _, v := range tc.accepts {
			if e.Check(v) != nil {
				t.Errorf("If-Match %v refuses %d", tc.header, v)
			}
		}
		for _, v := range tc.refuses {
			if !errors.Is(e.Check(v), kernel.ErrStale) {
				t.Errorf("If-Match %v accepts %d", tc.header, v)
			}
		}
		if e.Conditional() != tc.conditional {
			t.Errorf("If-Match %v conditional = %v", tc.header, e.Conditional())
		}
	}
}
