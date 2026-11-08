package outbox

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrKind reports a kind with no schema, a schema that does not compile,
// a kind and version registered twice, or an event of a kind and version
// no schema was registered for.
var ErrKind = errors.New("invalid event kind")

// ErrDocument reports an event document that fails its kind's schema: a
// programming error in the service that raised it.
var ErrDocument = errors.New("event document fails its schema")

// Kinds holds each event kind's JSON Schema per schema version. Within a
// version a schema only gains optional fields, so a document valid once
// stays valid.
//
// ref: decision 0015
type Kinds struct {
	mu      sync.RWMutex
	schemas map[kindVersion]*jsonschema.Schema
}

type kindVersion struct {
	kind    string
	version int
}

// NewKinds returns an empty registry.
func NewKinds() *Kinds { return &Kinds{schemas: map[kindVersion]*jsonschema.Schema{}} }

// Register adds kind's schema for version, a JSON Schema document; one
// without $schema is read as draft 2020-12.
func (k *Kinds) Register(kind string, version int, schema []byte) error {
	if kind == "" || version < 1 || len(schema) == 0 {
		return fmt.Errorf("%w: %q version %d needs a name, a version from 1 and a schema",
			ErrKind, kind, version)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("%w: %s version %d: %w", ErrKind, kind, version, err)
	}
	url := fmt.Sprintf("urn:infraweave:event:%s:%d", kind, version)
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(url, doc); err != nil {
		return fmt.Errorf("%w: %s version %d: %w", ErrKind, kind, version, err)
	}
	compiled, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("%w: %s version %d: %w", ErrKind, kind, version, err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	key := kindVersion{kind, version}
	if _, dup := k.schemas[key]; dup {
		return fmt.Errorf("%w: %s version %d registered twice", ErrKind, kind, version)
	}
	k.schemas[key] = compiled
	return nil
}

// Validate checks document against kind's schema for version.
func (k *Kinds) Validate(kind string, version int, document []byte) error {
	k.mu.RLock()
	s, ok := k.schemas[kindVersion{kind, version}]
	k.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: no schema for %s version %d", ErrKind, kind, version)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(document))
	if err != nil {
		return fmt.Errorf("%w: %s: not JSON: %w", ErrDocument, kind, err)
	}
	if err := s.Validate(inst); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrDocument, kind, err)
	}
	return nil
}
