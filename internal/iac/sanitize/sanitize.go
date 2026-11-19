package sanitize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Mode selects the Terraform-family document transformation.
type Mode string

// The modes carried by a run's specification. NONE is an explicit opt-out.
const (
	Default Mode = "DEFAULT"
	Smart   Mode = "SMART"
	None    Mode = "NONE"
	// MaxSize is the same 64 MiB bound as the Terraform-family plan parser.
	MaxSize = 64 << 20
)

// Errors distinguish an invalid document, exhausted bound and unknown mode.
var (
	ErrMalformed = errors.New("sanitize: malformed plan")
	ErrTooLarge  = errors.New("sanitize: plan exceeds 64 MiB")
	ErrMode      = errors.New("sanitize: unknown mode")
)

// Value hashes a UTF-8 string as the final eight lowercase SHA-256 hex
// characters. Other values pass through unchanged, as sanitized() requires.
func Value(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	return hashString(s)
}

type operation struct {
	mode    Mode
	collect bool
	known   map[string]struct{}
}

func hashString(s string) string {
	digest := sha256.Sum256([]byte(s))
	return hex.EncodeToString(digest[len(digest)-4:])
}

func (op *operation) remember(value any) any {
	return stringsIn(value, func(s string) string { op.known[s] = struct{}{}; return s })
}

func (op *operation) copies(value any) any {
	return stringsIn(value, func(s string) string {
		if _, sensitive := op.known[s]; sensitive {
			return hashString(s)
		}
		return s
	})
}

// Plan writes the transformed plan or Terragrunt units document to w.
// Validation finishes before any write, including in NONE mode. Invalid
// input never writes the original as a fallback; NONE preserves valid bytes.
func Plan(w io.Writer, r io.Reader, mode Mode) error {
	if mode != Default && mode != Smart && mode != None {
		return ErrMode
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	if len(raw) > MaxSize {
		return ErrTooLarge
	}
	if !json.Valid(raw) {
		return ErrMalformed
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if doc == nil {
		return ErrMalformed
	}
	op := &operation{mode: mode, known: map[string]struct{}{}}
	transform := func() error {
		if units, exists := doc["units"]; exists {
			if units == nil {
				return fmt.Errorf("%w: units must be an array", ErrMalformed)
			}
			return objects(units, func(unit map[string]any) error {
				if _, ok := unit["path"].(string); !ok {
					return fmt.Errorf("%w: unit path must be a string", ErrMalformed)
				}
				p, err := mapping(unit["plan"])
				if err != nil || p == nil {
					return fmt.Errorf("%w: unit plan must be an object", ErrMalformed)
				}
				if err := terraform(p, op); err != nil {
					return err
				}
				if checks, exists := p["checks"]; exists {
					if _, ok := checks.([]any); !ok {
						return fmt.Errorf("%w: unit checks must be an array", ErrMalformed)
					}
					if mode != None && !op.collect {
						p["checks"] = []any{}
					}
				}
				return nil
			})
		}
		return terraform(doc, op)
	}
	// Collect before changing any values, including across Terragrunt units:
	// an old secret can occur earlier in an unmarked before/output copy.
	if mode == Smart {
		op.collect = true
		if err := transform(); err != nil {
			return err
		}
		op.collect = false
	}
	if err := transform(); err != nil {
		return err
	}

	if mode == None {
		if _, err := io.Copy(w, bytes.NewReader(raw)); err != nil {
			return fmt.Errorf("write plan: %w", err)
		}
		return nil
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("write sanitized plan: %w", err)
	}
	return nil
}

func terraform(doc map[string]any, op *operation) error {
	version, ok := doc["format_version"].(string)
	if !ok || !strings.HasPrefix(version, "1.") {
		return fmt.Errorf("%w: expected plan format 1.x", ErrMalformed)
	}
	for _, key := range []string{"resource_changes", "resource_drift"} {
		if err := objects(doc[key], func(resource map[string]any) error {
			c, err := mapping(resource["change"])
			if err != nil || c == nil {
				return fmt.Errorf("%w: resource change must be an object", ErrMalformed)
			}
			return change(c, op)
		}); err != nil {
			return err
		}
	}
	outputs, err := mapping(doc["output_changes"])
	if err != nil {
		return err
	}
	for _, v := range outputs {
		c, err := mapping(v)
		if err != nil || c == nil {
			return fmt.Errorf("%w: output change must be an object", ErrMalformed)
		}
		if err := change(c, op); err != nil {
			return err
		}
	}
	planned, err := mapping(doc["planned_values"])
	if err != nil {
		return err
	}
	if err := values(planned, op); err != nil {
		return err
	}
	prior, err := mapping(doc["prior_state"])
	if err != nil {
		return err
	}
	state, err := mapping(prior["values"])
	if err != nil {
		return err
	}
	if err := values(state, op); err != nil {
		return err
	}
	configuration, err := mapping(doc["configuration"])
	if err != nil {
		return err
	}
	root, err := mapping(configuration["root_module"])
	if err != nil {
		return err
	}
	declarations, err := mapping(root["variables"])
	if err != nil {
		return err
	}
	variables, err := mapping(doc["variables"])
	if err != nil {
		return err
	}
	for name, variable := range variables {
		v, err := mapping(variable)
		if err != nil || v == nil {
			return fmt.Errorf("%w: variable must be an object", ErrMalformed)
		}
		declaration, err := mapping(declarations[name])
		if err != nil {
			return err
		}
		if err := booleanField(declaration, "sensitive"); err != nil {
			return err
		}
		if err := field(v, "value", declaration["sensitive"], op); err != nil {
			return err
		}
	}
	return constants(configuration, op)
}

func mapping(value any) (map[string]any, error) {
	if value == nil {
		return nil, nil //nolint:nilnil // absent optional sections are valid; required objects are checked by callers
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: expected object", ErrMalformed)
	}
	return m, nil
}

func booleanField(m map[string]any, key string) error {
	value, exists := m[key]
	if !exists {
		return nil
	}
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("%w: %s must be a boolean", ErrMalformed, key)
	}
	return nil
}

func objects(value any, visit func(map[string]any) error) error {
	if value == nil {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%w: expected array", ErrMalformed)
	}
	for _, value := range list {
		m, err := mapping(value)
		if err != nil || m == nil {
			return fmt.Errorf("%w: expected array of objects", ErrMalformed)
		}
		if err := visit(m); err != nil {
			return err
		}
	}
	return nil
}

func change(c map[string]any, op *operation) error {
	for _, key := range []string{"before", "after"} {
		if err := field(c, key, c[key+"_sensitive"], op); err != nil {
			return err
		}
	}
	return nil
}

func values(v map[string]any, op *operation) error {
	outputs, err := mapping(v["outputs"])
	if err != nil {
		return err
	}
	for _, value := range outputs {
		o, err := mapping(value)
		if err != nil || o == nil {
			return fmt.Errorf("%w: output must be an object", ErrMalformed)
		}
		if err := booleanField(o, "sensitive"); err != nil {
			return err
		}
		if err := field(o, "value", o["sensitive"], op); err != nil {
			return err
		}
	}
	m, err := mapping(v["root_module"])
	if err != nil {
		return err
	}
	return module(m, op)
}

func module(m map[string]any, op *operation) error {
	if err := objects(m["resources"], func(resource map[string]any) error {
		return field(resource, "values", resource["sensitive_values"], op)
	}); err != nil {
		return err
	}
	return objects(m["child_modules"], func(child map[string]any) error { return module(child, op) })
}

func field(m map[string]any, key string, marker any, op *operation) error {
	var apply func(any) any
	if op.collect {
		apply = op.remember
	}
	value, err := marked(m[key], marker, apply)
	if err != nil {
		return err
	}
	if _, exists := m[key]; exists {
		if op.mode == Default {
			value = hashTree(value)
		}
		if op.mode == Smart && !op.collect {
			value = op.copies(value)
		}
		m[key] = value
	}
	return nil
}

func marked(value, marker any, apply func(any) any) (any, error) {
	switch m := marker.(type) {
	case nil:
	case bool:
		if m && apply != nil {
			return apply(value), nil
		}
	case map[string]any:
		v, err := mapping(value)
		if err != nil {
			return nil, err
		}
		for key, child := range m {
			got, err := marked(v[key], child, apply)
			if err != nil {
				return nil, err
			}
			if _, exists := v[key]; exists {
				v[key] = got
			}
		}
	case []any:
		v, ok := value.([]any)
		if !ok || len(v) != len(m) {
			return nil, fmt.Errorf("%w: sensitivity array does not match values", ErrMalformed)
		}
		for i, child := range m {
			got, err := marked(v[i], child, apply)
			if err != nil {
				return nil, err
			}
			v[i] = got
		}
	default:
		return nil, fmt.Errorf("%w: invalid sensitivity marker", ErrMalformed)
	}
	return value, nil
}

func hashTree(value any) any { return stringsIn(value, hashString) }

func stringsIn(value any, apply func(string) string) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			v[key] = stringsIn(child, apply)
		}
	case []any:
		for i, child := range v {
			v[i] = stringsIn(child, apply)
		}
	case string:
		return apply(v)
	}
	return value
}

func constants(value any, op *operation, path ...string) error {
	switch v := value.(type) {
	case map[string]any:
		moduleContext := len(path) == 1 && path[0] == "root_module"
		if len(path) >= 3 && path[len(path)-1] == "module" && path[len(path)-3] == "module_calls" {
			moduleContext = true
		}
		declaration := len(path) >= 3 && path[len(path)-2] == "variables" && (path[len(path)-3] == "root_module" || path[len(path)-3] == "module")
		for key, child := range v {
			if declaration && key == "default" {
				continue
			}
			if key == "variables" && moduleContext {
				declarations, err := mapping(child)
				if err != nil {
					return err
				}
				for _, value := range declarations {
					declaration, err := mapping(value)
					if err != nil || declaration == nil {
						return fmt.Errorf("%w: variable declaration must be an object", ErrMalformed)
					}
					if err := booleanField(declaration, "sensitive"); err != nil {
						return err
					}
					if err := field(declaration, "default", declaration["sensitive"], op); err != nil {
						return err
					}
				}
			}
			if key == "constant_value" {
				if op.mode != None && !op.collect {
					v[key] = hashTree(child)
				}
				continue
			}
			if err := constants(child, op, append(path, key)...); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := constants(child, op, append(path, "[]")...); err != nil {
				return err
			}
		}
	}
	return nil
}
