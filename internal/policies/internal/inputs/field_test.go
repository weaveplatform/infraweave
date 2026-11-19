package inputs

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUnitField(t *testing.T) {
	empty := Field[bool]{}
	if empty.Present() || empty.IsNull() || !empty.IsZero() {
		t.Fatal("zero field is not absent")
	}
	if _, ok := empty.Get(); ok {
		t.Fatal("absent field has a value")
	}
	null := Null[string]()
	if !null.Present() || !null.IsNull() || null.IsZero() {
		t.Fatal("null lost presence")
	}
	if _, ok := null.Get(); ok {
		t.Fatal("null has a value")
	}
	value := Value(false)
	if v, ok := value.Get(); !ok || v || !value.Present() || value.IsNull() || value.IsZero() {
		t.Fatal("false lost presence")
	}
	for _, tc := range []struct {
		in   any
		want string
	}{{empty, "null"}, {null, "null"}, {value, "false"}} {
		got, err := json.Marshal(tc.in)
		if err != nil || string(got) != tc.want {
			t.Fatalf("marshal = %s %v, want %s", got, err, tc.want)
		}
	}
	bad := Value(json.RawMessage("broken"))
	if _, err := bad.MarshalJSON(); err == nil {
		t.Fatal("bad JSON encoded")
	}
	var out Field[string]
	for _, data := range []string{`"ok"`, `null`, `"new"`} {
		if err := out.UnmarshalJSON([]byte(data)); err != nil {
			t.Fatal(err)
		}
		b, err := out.MarshalJSON()
		if err != nil || string(b) != data {
			t.Fatalf("round trip %s: %s %v", data, b, err)
		}
	}
	if err := out.UnmarshalJSON([]byte(`true`)); err == nil {
		t.Fatal("wrong type accepted")
	}
	var destination string
	for _, data := range []string{`broken`, `"one" "two"`, `"one" broken`} {
		if err := decode([]byte(data), &destination); err == nil {
			t.Errorf("decode accepted %s", data)
		}
	}
	if _, err := schemaOf(reflect.TypeFor[int](), map[string]*Schema{}); err == nil {
		t.Fatal("unsupported Go type silently accepted")
	}
	type bare struct{ Value string }
	if _, err := schemaOf(reflect.TypeFor[bare](), map[string]*Schema{}); err == nil {
		t.Fatal("bare field silently accepted")
	}
	type nested struct{ Value Field[int] }
	if _, err := schemaOf(reflect.TypeFor[nested](), map[string]*Schema{}); err == nil {
		t.Fatal("unsupported nested Go type silently accepted")
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[[]int](), reflect.TypeFor[map[string]int]()} {
		if _, err := schemaOf(typ, map[string]*Schema{}); err == nil {
			t.Fatal("unsupported collection type accepted")
		}
	}
}

func TestUnitContractUnions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		newValue  func() any
		good, bad []string
	}{
		{"mask", func() any { return new(SensitiveMask) }, []string{`true`, `false`, `{}`, `[]`, `{"nested":[true,{"key":false}]}`}, []string{`"secret"`, `1`, `null`, `[true,1]`, `{"key":"x"}`, `broken`}},
		{"admin", func() any { return new(Admin) }, []string{`true`, `false`, `""`}, []string{`"admin"`, `1`, `null`, `{}`}},
		{"roles", func() any { return new(RoleSet) }, []string{`[]`, `["reader"]`, `{"reader":true}`}, []string{`"reader"`, `[1]`, `{"reader":"yes"}`, `null`, ``}},
		{"runs", func() any { return new(InProgress) }, []string{`""`, `[]`, `[{"id":"run"}]`}, []string{`{}`, `[1]`, `[{"unmodeled":true}]`, `null`, ``}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial, err := json.Marshal(tc.newValue())
			if err != nil || len(initial) == 0 {
				t.Fatal(err)
			}
			for _, data := range tc.good {
				out := tc.newValue()
				if err := json.Unmarshal([]byte(data), out); err != nil {
					t.Errorf("%s: %v", data, err)
					continue
				}
				encoded, err := json.Marshal(out)
				if err != nil || string(encoded) != data {
					t.Errorf("%s round trip: %s %v", data, encoded, err)
				}
			}
			for _, data := range tc.bad {
				out := tc.newValue()
				if u, ok := out.(json.Unmarshaler); !ok || u.UnmarshalJSON([]byte(data)) == nil {
					t.Errorf("accepts %q", data)
				}
			}
		})
	}
}
