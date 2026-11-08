package kernel

import "fmt"

// The field-error reasons of the API's fixed set, which clients branch on
// rather than on a message.
//
// ref: decision 0022
const (
	ReasonRequired     = "required"
	ReasonType         = "type"
	ReasonEnum         = "enum"
	ReasonFormat       = "format"
	ReasonPattern      = "pattern"
	ReasonMinLength    = "min-length"
	ReasonMaxLength    = "max-length"
	ReasonMinimum      = "minimum"
	ReasonMaximum      = "maximum"
	ReasonUnknownField = "unknown-field"
	ReasonDuplicate    = "duplicate"
	ReasonNotFound     = "not-found"
	ReasonRule         = "rule"
)

// FieldError is one field of an input that a rule refuses: a JSON Pointer
// into the input's body, a reason clients branch on and a message for
// people.
type FieldError struct {
	Pointer string
	Reason  string
	Message string
}

// Invalid is an input that rules refuse, naming every field they refuse,
// which the HTTP layer answers with 422 validation-failed and Detail as
// the problem's detail. A domain returns one from a check that has no
// store to ask, so any package below the domains can refuse a field the
// same way.
type Invalid struct {
	Detail string
	Errors []FieldError
}

func (e *Invalid) Error() string {
	if len(e.Errors) == 0 {
		return "invalid: " + e.Detail
	}
	return fmt.Sprintf("invalid: %s: %s", e.Errors[0].Pointer, e.Errors[0].Message)
}
