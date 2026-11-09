package expr

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// TestUnitExprCache checks that the LRU keeps the values used most
// recently, does not keep a value it failed to make, and is safe to share.
func TestUnitExprCache(t *testing.T) {
	c := newLRU[int, string](2)
	made := 0
	get := func(k int) string {
		v, err := c.get(k, func() (string, error) { made++; return strings.Repeat("v", k), nil })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	get(1)
	get(2)
	get(1)
	get(3)
	if made != 3 || get(1) != "v" || made != 3 {
		t.Errorf("1 was evicted though used last: made %d", made)
	}
	get(2)
	if made != 4 {
		t.Errorf("2 was kept though least used: made %d", made)
	}
	boom := errors.New("boom")
	if _, err := c.get(9, func() (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Errorf("a failed make: %v", err)
	}
	if _, ok := c.items[9]; ok {
		t.Error("a failed make was kept")
	}

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			if v, _ := c.get(
				i%4,
				func() (string, error) { return strings.Repeat("v", i%4), nil },
			); v !=
				strings.Repeat(
					"v",
					i%4,
				) {
				t.Errorf("a concurrent get of %d = %q", i%4, v)
			}
		})
	}
	wg.Wait()
	if len(c.items) > 2 || c.order.Len() != len(c.items) {
		t.Errorf("the cache holds %d items, %d in order", len(c.items), c.order.Len())
	}
}

// TestUnitExprValues covers the conversions no expression reaches:
// iterator and provider plumbing, mixed map keys, and values with no text.
func TestUnitExprValues(t *testing.T) {
	it := &keyIterator{keys: []ref.Val{types.String("a")}}
	if it.Next() != types.String("a") || it.Next() != nil || it.HasNext() != types.False {
		t.Error("the key iterator walked wrongly")
	}
	if _, err := it.ConvertToNative(reflect.TypeFor[string]()); err == nil ||
		!types.IsError(it.ConvertToType(types.StringType)) || it.Equal(types.True) != types.False ||
		it.Type() != types.IteratorType || it.Value() == nil {
		t.Error("the key iterator's value methods")
	}

	mixed := types.NewRefValMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
		types.String("b"): types.Int(1), types.Int(2): types.Int(2), types.Int(1): types.Int(3),
		types.Bool(true): types.Int(4), types.Uint(7): types.Int(5),
	})
	var keys []string
	for it := sorted(mixed).Iterator(); it.HasNext() == types.True; {
		s, _ := text(it.Next())
		keys = append(keys, s)
	}
	if strings.Join(keys, ",") != "true,1,2,b,7" {
		t.Errorf("mixed keys in order %v", keys)
	}
	if o := sorted(mixed); sorted(o) != o {
		t.Error("sorting an ordered map wrapped it again")
	}

	got, err := native(types.NewRefValMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
		types.Uint(1):     types.Bytes("x"),
		types.String("d"): types.Duration{Duration: time.Second},
		types.String("o"): types.OptionalOf(
			types.Double(1.5),
		),
		types.String("n"): types.OptionalNone,
		types.String("t"): types.IntType,
	}))
	want := map[string]any{"1": []byte("x"), "d": time.Second, "o": 1.5, "n": nil, "t": "int"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("native = %#v, %v", got, err)
	}
	bad := types.NewErr("no text")
	if _, err := native(bad); !errors.Is(err, errUnrenderable) {
		t.Errorf("native of an error: %v", err)
	}
	if _, err := native(
		types.NewDynamicList(types.DefaultTypeAdapter, []ref.Val{bad}),
	); err == nil {
		t.Error("native of a list holding an error")
	}
	if _, err := native(types.NewRefValMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
		types.String("k"): bad,
	})); err == nil {
		t.Error("native of a map holding an error")
	}
	if _, err := text(bad); !errors.Is(err, errUnrenderable) {
		t.Errorf("text of an error: %v", err)
	}
	var b strings.Builder
	if err := encodeJSON(
		&b,
		types.NewDynamicList(types.DefaultTypeAdapter, []ref.Val{bad}),
	); err == nil {
		t.Error("JSON of a list holding an error")
	}
	if err := encodeJSON(&b, types.NewRefValMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
		types.String("k"): bad,
	})); err == nil {
		t.Error("JSON of a map holding an error")
	}
	if got := coerce(
		map[string]any{"n": jsonNumber("x")},
		&schema{},
	); got.(map[string]any)["n"] != "x" {
		t.Errorf("a malformed json.Number: %#v", got)
	}
	if addCost(^uint64(0)-1, 5) != ^uint64(0) {
		t.Error("cost does not saturate")
	}
}

// TestUnitExprProvider checks the provider's object types, which only the
// checker's field lookups otherwise reach.
func TestUnitExprProvider(t *testing.T) {
	p, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseSchema(
		[]byte(`{"properties": {"b": {"type": "string"}, "a": {"type": "integer"}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if typ := p.celType("v", s); typ.TypeName() != "v" {
		t.Errorf("type %v", typ)
	}
	if names, ok := p.FindStructFieldNames("v"); !ok || strings.Join(names, ",") != "a,b" {
		t.Errorf("field names %v, %v", names, ok)
	}
	if names, ok := p.FindStructFieldNames("google.protobuf.Duration"); !ok || len(names) == 0 {
		t.Errorf("a well-known type's field names %v, %v", names, ok)
	}
	if _, ok := p.FindStructFieldType("v", "c"); ok {
		t.Error("an undeclared field was found")
	}
	if _, ok := p.FindStructFieldType("nope", "c"); ok {
		t.Error("a field of an unknown type was found")
	}
	if v := p.NewValue(
		"v",
		nil,
	); !types.IsError(v) ||
		!strings.Contains(v.(*types.Err).Error(), "cannot be constructed") {
		t.Errorf("constructing a declared object: %v", v)
	}
	if v := p.NewValue("nope", nil); !types.IsError(v) {
		t.Errorf("constructing an unknown type: %v", v)
	}
	if _, ok := p.FindStructType("nope"); ok {
		t.Error("an unknown type was found")
	}
}

// TestUnitExprConfig checks that Validate names each limit that is not
// positive.
func TestUnitExprConfig(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	err := Config{Timeout: -time.Second, MaxSource: -1}.Validate()
	for _, key := range []string{"cost_limit", "timeout", "max_source", "max_result"} {
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "expressions."+key) {
			t.Errorf("%s is not named: %v", key, err)
		}
	}
}

func jsonNumber(s string) any { return json.Number(s) }
