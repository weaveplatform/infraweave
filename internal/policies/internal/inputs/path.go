package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
)

// PathStep is a resource instance key or replacement path step: a string or
// integer, retaining numeric indices as JSON numbers.
type PathStep struct {
	name    string
	index   int64
	numeric bool
}

var errPathStep = errors.New("policy path step must be a string or integer")

func (p PathStep) MarshalJSON() ([]byte, error) {
	if p.numeric {
		return json.Marshal(p.index) //nolint:wrapcheck // primitive integer encoding cannot fail
	}
	return json.Marshal(p.name) //nolint:wrapcheck // primitive string encoding cannot fail
}

func (p *PathStep) UnmarshalJSON(raw []byte) error {
	var next PathStep
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errPathStep
	}
	if err := json.Unmarshal(raw, &next.name); err == nil {
		*p = next
		return nil
	}
	if err := json.Unmarshal(raw, &next.index); err != nil {
		return errPathStep
	}
	next.numeric = true
	*p = next
	return nil
}
