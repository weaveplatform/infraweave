package settings

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Checker checks one vendor's settings, collecting a FieldError for each
// field it refuses. Pointers are relative to the settings object, which
// the caller places under the stack input's vendorConfig.
type Checker struct {
	fields map[string]json.RawMessage
	errs   []kernel.FieldError
}

// New starts checking settings. Settings that are not a JSON object are
// refused as a whole, at the empty pointer, and no field check refuses
// anything after.
func New(settings json.RawMessage) *Checker {
	c := &Checker{}
	if err := json.Unmarshal(settings, &c.fields); err != nil || c.fields == nil {
		c.errs = append(c.errs, kernel.FieldError{
			Reason: kernel.ReasonType, Message: "The vendor's settings are an object.",
		})
	}
	return c
}

// Errors are the field errors the checks found, in the order they ran.
func (c *Checker) Errors() []kernel.FieldError { return c.errs }

// Bool refuses a value of name that is neither a boolean nor null.
func (c *Checker) Bool(name string) {
	var b *bool
	c.decode(name, &b, "a boolean")
}

// Version refuses a value of name that is not null and does not parse as
// a version constraint.
func (c *Checker) Version(name string) {
	var s *string
	if !c.decode(name, &s, "a string") || s == nil {
		return
	}
	if _, err := version.ParseConstraints(*s); err != nil {
		c.refuse(name, kernel.ReasonFormat, fmt.Sprintf("The %s field is %v.", name, err))
	}
}

// Enum refuses a value of name that is not null and not one of values.
func (c *Checker) Enum(name string, values ...string) {
	var s *string
	if !c.decode(name, &s, "a string") || s == nil || slices.Contains(values, *s) {
		return
	}
	c.refuse(
		name,
		kernel.ReasonEnum,
		fmt.Sprintf("The %s field is one of %s.", name, strings.Join(values, ", ")),
	)
}

// Workspace refuses a value of name that is not null and that a URL path
// would have to escape. Terraform and OpenTofu reach a workspace's state
// through a file path or URL that carries its name, so their workspace
// command refuses such a name, and a stack naming one would fail only at
// its first run.
func (c *Checker) Workspace(name string) {
	var s *string
	if !c.decode(name, &s, "a string") || s == nil || *s == url.PathEscape(*s) {
		return
	}
	c.refuse(name, kernel.ReasonPattern, fmt.Sprintf(
		"The %s field is a workspace name that a URL path need not escape, without / or spaces.",
		name,
	))
}

// decode decodes name's value into v, refusing a value of the wrong type,
// and reports whether the field is present and decoded.
func (c *Checker) decode(name string, v any, kind string) bool {
	raw, ok := c.fields[name]
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, v); err != nil {
		c.refuse(name, kernel.ReasonType, fmt.Sprintf("The %s field is %s or null.", name, kind))
		return false
	}
	return true
}

func (c *Checker) refuse(name, reason, message string) {
	c.errs = append(
		c.errs,
		kernel.FieldError{Pointer: "/" + name, Reason: reason, Message: message},
	)
}
