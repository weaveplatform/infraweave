package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// Field keeps an absent key distinct from a present null and a present value.
// Its zero value is absent; omitzero prevents future metadata from appearing
// merely because its type is part of the contract.
type Field[T any] struct {
	value   T
	present bool
	null    bool
}

// Value constructs a present, non-null field, including false, zero and empty.
func Value[T any](v T) Field[T] { return Field[T]{value: v, present: true} }

// Null constructs a present JSON null.
func Null[T any]() Field[T] { return Field[T]{present: true, null: true} }

// Get returns the value and whether the field is present and non-null.
func (f Field[T]) Get() (T, bool) { return f.value, f.present && !f.null }

// Present reports whether the JSON object contains this key, including null.
func (f Field[T]) Present() bool { return f.present }

// IsNull reports a present null.
func (f Field[T]) IsNull() bool { return f.present && f.null }

// IsZero makes encoding/json omit absent fields, without omitting nulls.
func (f Field[T]) IsZero() bool { return !f.present }

// MarshalJSON preserves nulls and the value's JSON type.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if f.null || !f.present {
		return []byte("null"), nil
	}
	b, err := json.Marshal(f.value)
	if err != nil {
		return nil, fmt.Errorf("encode policy field: %w", err)
	}
	return b, nil
}

// UnmarshalJSON rejects unknown fields inside typed contract envelopes. Open
// provider attributes and plugin documents use Object at their named boundaries.
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	var next Field[T]
	next.present = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		next.null = true
		*f = next
		return nil
	}
	if err := decode(b, &next.value); err != nil {
		return err
	}
	*f = next
	return nil
}

func (Field[T]) fieldType() reflect.Type { return reflect.TypeFor[T]() }

var errTrailing = errors.New("policy document contains more than one JSON value")

func decode(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("decode policy document: %w", err)
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errTrailing
	}
	return nil
}

// Object is a JSON object whose keys belong to a provider, vendor or custom
// plugin, rather than infraweave's contract. Contract envelopes never use it
// in place of their named fields.
type Object map[string]json.RawMessage

// Admin preserves the observed boolean human value and empty-string machine
// value without accepting arbitrary JSON as a session's admin marker.
type Admin struct{ value json.RawMessage }

func (a Admin) MarshalJSON() ([]byte, error) {
	if len(a.value) == 0 {
		return []byte("false"), nil
	}
	return a.value, nil
}

func (a *Admin) UnmarshalJSON(b []byte) error {
	if !bytes.Equal(b, []byte("true")) && !bytes.Equal(b, []byte("false")) && !bytes.Equal(b, []byte(`""`)) {
		return errUnion
	}
	a.value = bytes.Clone(b)
	return nil
}

// RoleSet is either a list of role slugs or a map from slug to a boolean.
// Both forms are accepted by the published login decision contract.
type RoleSet struct{ value json.RawMessage }

func (r RoleSet) MarshalJSON() ([]byte, error) {
	if len(r.value) == 0 {
		return []byte("[]"), nil
	}
	return r.value, nil
}

func (r *RoleSet) UnmarshalJSON(b []byte) error {
	var list []string
	var set map[string]bool
	if len(b) == 0 || (b[0] != '[' && b[0] != '{') {
		return errUnion
	}
	if (b[0] == '[' && decode(b, &list) != nil) || (b[0] == '{' && decode(b, &set) != nil) {
		return errUnion
	}
	r.value = bytes.Clone(b)
	return nil
}

// InProgress keeps the live array representation and the contract's legacy
// empty string representation distinct.
type InProgress struct{ value json.RawMessage }

func (r InProgress) MarshalJSON() ([]byte, error) {
	if len(r.value) == 0 {
		return []byte("[]"), nil
	}
	return r.value, nil
}

func (r *InProgress) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte(`""`)) {
		r.value = bytes.Clone(b)
		return nil
	}
	var runs []InProgressRun
	if len(b) == 0 || b[0] != '[' || decode(b, &runs) != nil {
		return errUnion
	}
	r.value = bytes.Clone(b)
	return nil
}

var errUnion = errors.New("policy value does not match a contracted union")

// SensitiveMask is the recursive boolean, array or object mask emitted by the
// plan vendor. Resource attribute names are provider-owned; mask leaves are
// booleans rather than arbitrary JSON scalars.
type SensitiveMask struct{ value json.RawMessage }

func (m SensitiveMask) MarshalJSON() ([]byte, error) {
	if len(m.value) == 0 {
		return []byte("false"), nil
	}
	return m.value, nil
}

func (m *SensitiveMask) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("decode policy sensitivity mask: %w", err)
	}
	if !validMask(v) {
		return errUnion
	}
	m.value = bytes.Clone(b)
	return nil
}

func validMask(v any) bool {
	switch v := v.(type) {
	case bool:
		return true
	case []any:
		for _, child := range v {
			if !validMask(child) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, child := range v {
			if !validMask(child) {
				return false
			}
		}
		return true
	}
	return false
}
