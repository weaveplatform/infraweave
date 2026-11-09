package expr

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// adapter turns Go values into CEL values, every JSON object into one that
// iterates its keys in sorted order.
//
// ref: decision 0024 "Determinism"
type adapter struct {
	types.Adapter
}

func (a adapter) NativeToValue(v any) ref.Val {
	switch v := v.(type) {
	case map[string]any:
		return sorted(types.NewStringInterfaceMap(a, v))
	case []any:
		return types.NewDynamicList(a, v)
	}
	return a.Adapter.NativeToValue(v)
}

// orderedMap is a map whose iteration visits keys in sorted order, so a
// comprehension over it gives the same result every time. CEL maps
// otherwise iterate in Go's random map order.
type orderedMap struct {
	traits.Mapper
}

// sorted orders m's iteration; a map already ordered is returned as it is.
func sorted(m traits.Mapper) traits.Mapper {
	if o, ok := m.(*orderedMap); ok {
		return o
	}
	return &orderedMap{Mapper: m}
}

func (m *orderedMap) Iterator() traits.Iterator {
	return &keyIterator{keys: sortedKeys(m.Mapper)}
}

// sortedKeys lists m's keys: by type name first, as a map's keys may mix
// types, then by value.
func sortedKeys(m traits.Mapper) []ref.Val {
	var keys []ref.Val
	for it := m.Iterator(); it.HasNext() == types.True; {
		keys = append(keys, it.Next())
	}
	slices.SortFunc(keys, compareKeys)
	return keys
}

func compareKeys(a, b ref.Val) int {
	if c := strings.Compare(a.Type().TypeName(), b.Type().TypeName()); c != 0 {
		return c
	}
	// Every CEL map key type (bool, int, uint and string) is a Comparer.
	if c, ok := a.(traits.Comparer); ok {
		if n, ok := c.Compare(b).(types.Int); ok {
			return int(n)
		}
	}
	return 0
}

// keyIterator walks a sorted key list.
type keyIterator struct {
	keys []ref.Val
	next int
}

func (it *keyIterator) HasNext() ref.Val { return types.Bool(it.next < len(it.keys)) }

func (it *keyIterator) Next() ref.Val {
	if it.next >= len(it.keys) {
		return nil
	}
	it.next++
	return it.keys[it.next-1]
}

func (*keyIterator) ConvertToNative(t reflect.Type) (any, error) {
	return nil, fmt.Errorf("%w to %v", errIterator, t)
}

func (*keyIterator) ConvertToType(ref.Type) ref.Val {
	return types.NewErr("an iterator does not convert")
}

func (*keyIterator) Equal(ref.Val) ref.Val { return types.False }

func (*keyIterator) Type() ref.Type { return types.IteratorType }

func (it *keyIterator) Value() any { return it.keys }

// coerce shapes v as s declares it: a JSON number becomes an int where s
// asks for an integer, and a double where it asks for a number, so a value
// decoded from JSON meets the type the checker gave it.
func coerce(v any, s *schema) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			field := s.additional()
			if f, ok := s.Properties[k]; ok {
				field = f
			}
			out[k] = coerce(e, field)
		}
		return out
	case []any:
		item := s.Items
		if item == nil {
			item = &schema{}
		}
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = coerce(e, item)
		}
		return out
	case json.Number:
		if i, err := v.Int64(); err == nil && s.kind() != "number" {
			return i
		}
		f, err := v.Float64()
		if err != nil {
			return v.String()
		}
		return coerce(f, s)
	case float64:
		if s.kind() == "integer" && v == math.Trunc(v) && math.Abs(v) < 1<<63 {
			return int64(v)
		}
	case int:
		if s.kind() == "number" {
			return float64(v)
		}
	case int64:
		if s.kind() == "number" {
			return float64(v)
		}
	}
	return v
}

var (
	// errUnrenderable reports a value with no text or JSON form.
	errUnrenderable = errors.New("has no text form")
	// errNoJSON reports a double that JSON cannot hold.
	errNoJSON = errors.New("has no JSON form")
	// errIterator reports a conversion of an iterator, which is never a
	// value an expression holds.
	errIterator = errors.New("an iterator does not convert")
)

// text renders v as a template inserts it: a string as it is; an int,
// uint, double, bool or duration as CEL's string() gives it; a timestamp
// in RFC 3339; bytes in base64; a list or map as compact JSON with sorted
// keys; null, and an optional with no value, as nothing.
//
// ref: decision 0024 "Two forms"
func text(v ref.Val) (string, error) {
	switch v := v.(type) {
	case types.String:
		return string(v), nil
	case types.Null:
		return "", nil
	case types.Bytes:
		return base64.StdEncoding.EncodeToString(v), nil
	case types.Timestamp:
		return v.Format(time.RFC3339Nano), nil
	case *types.Optional:
		if !v.HasValue() {
			return "", nil
		}
		return text(v.GetValue())
	case types.Int, types.Uint, types.Double, types.Bool, types.Duration:
		s, ok := v.ConvertToType(types.StringType).(types.String)
		if !ok {
			return "", fmt.Errorf("%s %w", v.Type().TypeName(), errUnrenderable)
		}
		return string(s), nil
	case ref.Type:
		return v.TypeName(), nil
	}
	switch v.(type) {
	case traits.Lister, traits.Mapper:
		var b strings.Builder
		err := encodeJSON(&b, v)
		return b.String(), err
	}
	return "", fmt.Errorf("%s %w", v.Type().TypeName(), errUnrenderable)
}

// encodeJSON writes v as compact JSON, a map's keys sorted. A timestamp,
// duration or bytes is the string its text is, and a map key that is not
// a string is its text.
func encodeJSON(b *strings.Builder, v ref.Val) error {
	switch v := v.(type) {
	case types.Null:
		b.WriteString("null")
	case types.Bool:
		b.WriteString(strconv.FormatBool(bool(v)))
	case types.Int:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case types.Uint:
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case types.Double:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%v %w", f, errNoJSON)
		}
		enc, _ := json.Marshal(f)
		b.Write(enc)
	case types.String:
		writeJSONString(b, string(v))
	case *types.Optional:
		if !v.HasValue() {
			b.WriteString("null")
			return nil
		}
		return encodeJSON(b, v.GetValue())
	case traits.Lister:
		b.WriteByte('[')
		for i := range int(v.Size().(types.Int)) {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encodeJSON(b, v.Get(types.Int(i))); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case traits.Mapper:
		return encodeObject(b, v)
	default:
		s, err := text(v)
		if err != nil {
			return err
		}
		writeJSONString(b, s)
	}
	return nil
}

// encodeObject writes m with its keys, as text, in sorted order.
func encodeObject(b *strings.Builder, m traits.Mapper) error {
	type entry struct {
		key string
		val ref.Val
	}
	var entries []entry
	for it := m.Iterator(); it.HasNext() == types.True; {
		k := it.Next()
		key, err := text(k)
		if err != nil {
			return err
		}
		entries = append(entries, entry{key, m.Get(k)})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	b.WriteByte('{')
	for i, e := range entries {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(b, e.key)
		b.WriteByte(':')
		if err := encodeJSON(b, e.val); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func writeJSONString(b *strings.Builder, s string) {
	enc, _ := json.Marshal(s)
	b.Write(enc)
}

// native turns a result into Go: nil, bool, int64, uint64, float64,
// string, []byte, time.Time, time.Duration, []any or map[string]any. A
// map key that is not a string becomes its text, an optional its value or
// nil, and a type its name.
func native(v ref.Val) (any, error) {
	switch v := v.(type) {
	case types.Null:
		return nil, nil //nolint:nilnil // null is a value
	case types.Bool:
		return bool(v), nil
	case types.Int:
		return int64(v), nil
	case types.Uint:
		return uint64(v), nil
	case types.Double:
		return float64(v), nil
	case types.String:
		return string(v), nil
	case types.Bytes:
		return []byte(v), nil
	case types.Timestamp:
		return v.Time, nil
	case types.Duration:
		return v.Duration, nil
	case *types.Optional:
		if !v.HasValue() {
			return nil, nil //nolint:nilnil // an empty optional is null
		}
		return native(v.GetValue())
	case ref.Type:
		return v.TypeName(), nil
	case traits.Lister:
		out := make([]any, 0, int(v.Size().(types.Int)))
		for it := v.Iterator(); it.HasNext() == types.True; {
			e, err := native(it.Next())
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	case traits.Mapper:
		out := make(map[string]any, int(v.Size().(types.Int)))
		for it := v.Iterator(); it.HasNext() == types.True; {
			k := it.Next()
			key, err := text(k)
			if err != nil {
				return nil, err
			}
			if out[key], err = native(v.Get(k)); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s %w", v.Type().TypeName(), errUnrenderable)
}
