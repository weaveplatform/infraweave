package inputs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Capture sanitizes a copy using the same typed fields as the published
// contract. Sensitive leaves lose their values while object keys, JSON types,
// array lengths, absent/null distinctions and structural fields survive.
// Samples are separate: their exact evaluated document never calls Capture.
func Capture(kind string, raw json.RawMessage) (json.RawMessage, error) {
	if err := Validate(kind, raw); err != nil {
		return nil, err
	}
	pair, ok := contractPairs()[kind]
	if !ok {
		return nil, ErrInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: invalid capture JSON", ErrInput)
	}
	value = captureTyped(value, pair[0])
	out, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: encode capture JSON", ErrInput)
	}
	return out, nil
}

func captureTyped(value any, model reflect.Type) any {
	if value == nil {
		return nil
	}
	if field, ok := reflect.Zero(model).Interface().(field); ok {
		return captureTyped(value, field.fieldType())
	}
	if model.Kind() == reflect.Pointer {
		return captureTyped(value, model.Elem())
	}
	switch model.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return value
		}
		for i := range model.NumField() {
			field := model.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			current, present := object[name]
			if !present {
				continue
			}
			if field.Tag.Get("capture") == "sensitive" {
				object[name] = captureBlank(current)
			} else {
				object[name] = captureTyped(current, field.Type)
			}
		}
	case reflect.Slice:
		if items, ok := value.([]any); ok {
			for i, current := range items {
				items[i] = captureTyped(current, model.Elem())
			}
		}
	case reflect.Map:
		if object, ok := value.(map[string]any); ok {
			for key, current := range object {
				object[key] = captureTyped(current, model.Elem())
			}
		}
	}
	return value
}

func captureBlank(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			v[key] = captureBlank(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = captureBlank(child)
		}
		return v
	case string:
		return "[redacted]"
	case json.Number:
		return json.Number("0")
	case bool:
		return false
	default:
		return nil
	}
}
