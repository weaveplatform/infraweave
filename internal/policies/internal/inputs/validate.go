package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// MaxDocumentBytes bounds decoding before a caller allocates a JSON tree.
const MaxDocumentBytes = 64 << 20

// ErrInput deliberately excludes validation values, which may contain secrets.
var ErrInput = errors.New("policy input does not match its typed contract")

var validators = sync.OnceValues(compileInputs)

// Validate checks the same required fields, unions and open vendor objects the
// server publishes. JSON numbers retain their precision, including timestamps.
func Validate(kind string, raw json.RawMessage) error {
	if len(raw) > MaxDocumentBytes {
		return fmt.Errorf("%w: document exceeds byte limit", ErrInput)
	}
	schemas, err := validators()
	if err != nil {
		return err
	}
	schema, ok := schemas[kind]
	if !ok || kind == "ACCESS" {
		return fmt.Errorf("%w: unsupported type", ErrInput)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: invalid JSON", ErrInput)
	}
	if schema.Validate(value) != nil {
		return ErrInput
	}
	return nil
}

func compileInputs() (map[string]*jsonschema.Schema, error) {
	c, err := Contract()
	if err != nil {
		return nil, fmt.Errorf("policy input contract: %w", err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encode policy input contract: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode policy input contract: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const address = "urn:infraweave:policy-input-contract"
	if err := compiler.AddResource(address, value); err != nil {
		return nil, fmt.Errorf("register policy input contract: %w", err)
	}
	out := map[string]*jsonschema.Schema{}
	for kind, pair := range c.PolicyTypes {
		schema, err := compiler.Compile(address + pair.Input.Ref)
		if err != nil {
			return nil, fmt.Errorf("compile %s input contract: %w", kind, err)
		}
		out[kind] = schema
	}
	return out, nil
}
