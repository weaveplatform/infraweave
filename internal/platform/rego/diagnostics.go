package rego

import (
	"errors"
	"fmt"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
)

// Diagnostic preserves OPA's source point and byte span for editor adapters.
type Diagnostic struct {
	File                        string
	Row, Column, Offset, Length int
	Message                     string
}

// CompileError is a save-time refusal with OPA's own diagnostics and location.
type CompileError struct {
	Diagnostics []Diagnostic
	cause       error
}

func (e *CompileError) Error() string { return fmt.Sprintf("compile policy: %v", e.cause) }
func (e *CompileError) Unwrap() error { return e.cause }

func diagnostic(location *ast.Location, message string) Diagnostic {
	d := Diagnostic{Message: message}
	if location != nil {
		d.File = location.File
		d.Row = location.Row
		d.Column = location.Col
		d.Offset = location.Offset
		d.Length = len(location.Text)
	}
	return d
}

func compileError(err error) *CompileError {
	out := &CompileError{cause: err}
	var errs ast.Errors
	if errors.As(err, &errs) {
		for _, item := range errs {
			out.Diagnostics = append(out.Diagnostics, diagnostic(item.Location, item.Message))
		}
	}
	return out
}

func refusal(location *ast.Location, message string) error {
	return &CompileError{cause: fmt.Errorf("%w: %s", ErrPolicy, message), Diagnostics: []Diagnostic{diagnostic(location, message)}}
}

func validateModule(module *ast.Module) error {
	if module.Package.Path.String() != "data.infraweave" {
		return refusal(module.Package.Location, "policy package must be infraweave")
	}
	for _, imp := range module.Imports {
		path := imp.Path.String()
		allowed := path == "rego.v1" || path == "future.keywords" || strings.HasPrefix(path, "future.keywords.") || path == "input" || strings.HasPrefix(path, "input.") ||
			path == "data" ||
			strings.HasPrefix(path, "data.")
		if !allowed {
			return refusal(imp.Location, "policy import is not allowed: "+path)
		}
	}
	for _, rule := range module.Rules {
		if headName(rule.Head) == "result" || headName(rule.Head) == "sanitized" {
			return refusal(rule.Location, "policy rule name is reserved: "+headName(rule.Head))
		}
	}
	return nil
}

func headName(head *ast.Head) string {
	ref := head.Ref()
	if len(ref) == 0 {
		return ""
	}
	return ref[0].Value.String()
}
