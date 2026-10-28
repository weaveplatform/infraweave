package specref

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

var ErrMappingInvalid = errors.New("invalid rest mapping")

// Mapping is the hand-curated part of the reference: everything the naming
// rules cannot infer. It is decoded strictly so a typo in a key fails the
// build instead of silently doing nothing.
type Mapping struct {
	// RPCBudget caps how many mutations may fall back to an RPC-style
	// POST /<operation-kebab>, so a new upstream mutation the rules cannot
	// place fails generation instead of shipping without a resource shape.
	RPCBudget int `yaml:"rpcBudget"`
	// Plurals lists irregular plurals, singular name → plural name.
	Plurals map[string]string `yaml:"plurals"`
	// Singletons are account-wide objects addressed without an ID (/oidc).
	Singletons []string `yaml:"singletons"`
	// AttributeArgs are ID arguments that set a property of the target
	// rather than locate it under a parent (space).
	AttributeArgs []string `yaml:"attributeArgs"`
	// Operations override the derived route of a root field.
	Operations map[string]OperationOverride `yaml:"operations"`
	// Exclude removes root fields from the reference, with the reason.
	Exclude map[string]string `yaml:"exclude"`
	// Terraform maps a provider attribute to its GraphQL input field where
	// snake_case → camelCase does not, keyed by resource then attribute.
	Terraform map[string]map[string]string `yaml:"terraform"`
}

// OperationOverride fixes the route of one root field.
type OperationOverride struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	// Params binds path parameters to GraphQL arguments when the parameter
	// name is not <arg>Id or the argument itself. A dotted value binds a
	// field inside an input-object argument (reference.id).
	Params map[string]string `yaml:"params"`
	// Public marks a route that needs no bearer token (token exchange).
	Public bool   `yaml:"public"`
	Tag    string `yaml:"tag"`
	// Created answers 201 for an operation that creates something under a
	// verb the rules do not read as create (pluginInstall).
	Created bool `yaml:"created"`
}

// ParseMapping decodes the mapping file, rejecting unknown keys.
func ParseMapping(data []byte) (*Mapping, error) {
	var m Mapping
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %w", ErrMappingInvalid, err)
	}
	for name, o := range m.Operations {
		if o.Method == "" || o.Path == "" {
			return nil, fmt.Errorf(
				"%w: operation %s needs method and path",
				ErrMappingInvalid,
				name,
			)
		}
	}
	for name, reason := range m.Exclude {
		if reason == "" {
			return nil, fmt.Errorf("%w: exclusion of %s needs a reason", ErrMappingInvalid, name)
		}
	}
	return &m, nil
}
