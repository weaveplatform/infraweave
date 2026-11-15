package plan

import (
	"encoding/json"
	"fmt"
	"io"
)

// Output is one root module output after an apply: its value as JSON,
// whether it is sensitive, and its description in the planned
// configuration, empty when it has none.
type Output struct {
	Value       json.RawMessage
	Sensitive   bool
	Description string
}

// ReadOutputs reads r, the JSON of `terraform output -json` or `tofu
// output -json`, into the root module's outputs by name. A sensitive
// output may have no value, as WithoutSensitiveValues writes it, and
// reads with a nil Value. It refuses a document past MaxSize as
// ErrTooLarge, and one that is not an object of outputs, each that is
// not sensitive with a value, as ErrMalformed.
func ReadOutputs(r io.Reader) (map[string]Output, error) {
	raw, err := readAll(r)
	if err != nil {
		return nil, err
	}
	var doc map[string]struct {
		Sensitive bool            `json:"sensitive"`
		Value     json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("%w: the outputs are not an object", ErrMalformed)
	}
	out := make(map[string]Output, len(doc))
	for name, o := range doc {
		if o.Value == nil && !o.Sensitive {
			return nil, fmt.Errorf("%w: the output %q has no value", ErrMalformed, name)
		}
		out[name] = Output{Value: o.Value, Sensitive: o.Sensitive}
	}
	return out, nil
}

// WithoutSensitiveValues writes to w the JSON of `output -json` that r
// reads, each sensitive output without its value and every other field
// kept, so a value the run may not keep never leaves it. It refuses r as
// ReadOutputs does, but for an output that has no value.
func WithoutSensitiveValues(w io.Writer, r io.Reader) error {
	raw, err := readAll(r)
	if err != nil {
		return err
	}
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if doc == nil {
		return fmt.Errorf("%w: the outputs are not an object", ErrMalformed)
	}
	for name, o := range doc {
		var sensitive bool
		if s, ok := o["sensitive"]; ok {
			if err := json.Unmarshal(s, &sensitive); err != nil {
				return fmt.Errorf("%w: the output %q: %w", ErrMalformed, name, err)
			}
		}
		if sensitive {
			delete(o, "value")
		}
	}
	// HTML escaping would rewrite a string value's <, > and &, which the
	// stack's outputs then answer as written.
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	return e.Encode(doc) //nolint:wrapcheck // the caller names the document
}

// Descriptions reads r, a plan's JSON as Parse reads it, for the
// descriptions of its configuration's root module outputs, by name; an
// output with none is absent. It refuses a document as Parse does, but
// for the format version, which a plan Parse read was already checked
// for.
func Descriptions(r io.Reader) (map[string]string, error) {
	raw, err := readAll(r)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Configuration struct {
			//nolint:tagliatelle // Terraform's spelling
			RootModule struct {
				Outputs map[string]struct {
					Description string `json:"description"`
				} `json:"outputs"`
			} `json:"root_module"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	out := map[string]string{}
	for name, o := range doc.Configuration.RootModule.Outputs {
		if o.Description != "" {
			out[name] = o.Description
		}
	}
	return out, nil
}
