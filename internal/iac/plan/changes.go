package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// DocumentVersion is the changes document's version: Write writes it, and
// Read refuses any other, so a server never misreads a document a runner
// of another release wrote.
const DocumentVersion = 1

// changesDocument is the changes document, a run's changes.json.
type changesDocument struct {
	Version int      `json:"version"`
	Changes []Change `json:"changes"`
}

// types and results are the values a changes document's metadata takes.
var (
	types = []Type{
		TypeAdd, TypeChange, TypeDelete, TypeReplaceDestroyBeforeCreate,
		TypeReplaceCreateBeforeDestroy, TypeNoop, TypeRead, TypeImport, TypeForget,
		TypeCreateAfterForgotten,
	}
	results = []Result{ResultPlanned}
)

// Write writes changes to w as the changes document, version
// DocumentVersion, with an empty list for none.
func Write(w io.Writer, changes []Change) error {
	if changes == nil {
		changes = []Change{}
	}
	if err := json.NewEncoder(w).Encode(changesDocument{
		Version: DocumentVersion, Changes: changes,
	}); err != nil {
		return fmt.Errorf("plan: write the changes: %w", err)
	}
	return nil
}

// Read reads the changes document Write wrote. It refuses a document past
// MaxSize as ErrTooLarge; one that is not the document alone, names a
// member it does not, or holds a change without an address or of a type or
// result it does not know, as ErrMalformed; and another version as
// ErrFormat.
func Read(r io.Reader) ([]Change, error) {
	raw, err := readAll(r)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc changesDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: more follows the changes document", ErrMalformed)
	}
	if doc.Version != DocumentVersion {
		return nil, fmt.Errorf("%w: changes document version %d", ErrFormat, doc.Version)
	}
	for i, c := range doc.Changes {
		if c.Address == "" || !slices.Contains(types, c.Metadata.Type) ||
			!slices.Contains(results, c.Metadata.Result) {
			return nil, fmt.Errorf("%w: change %d is %+v", ErrMalformed, i, c)
		}
	}
	if doc.Changes == nil {
		doc.Changes = []Change{}
	}
	return doc.Changes, nil
}
