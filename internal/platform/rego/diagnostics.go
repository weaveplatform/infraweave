package rego

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/open-policy-agent/opa/v1/ast"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Diagnostic preserves OPA's source point and byte span for editor adapters.
type Diagnostic struct {
	File                        string
	Row, Column, Offset, Length int
	Message                     string
}

// SourceRange locates the submitted module's byte span using one-based Unicode
// positions. Internal modules have no corresponding text in the user's body.
func (d Diagnostic) SourceRange(body string) *kernel.Range {
	if d.File != "policy.rego" || d.Offset < 0 || d.Offset > len(body) || d.Length < 0 {
		return nil
	}
	start := d.Offset
	if start < len(body) && !utf8.RuneStart(body[start]) {
		return nil
	}
	end := start + min(d.Length, len(body)-start)
	for end < len(body) && !utf8.RuneStart(body[end]) {
		end++
	}
	return &kernel.Range{Start: sourcePosition(body, start), End: sourcePosition(body, end)}
}

func sourcePosition(body string, offset int) kernel.Position {
	prefix := body[:offset]
	last := strings.LastIndexByte(prefix, '\n')
	return kernel.Position{Line: strings.Count(prefix, "\n") + 1, Column: utf8.RuneCountInString(prefix[last+1:]) + 1}
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
		ref, ok := imp.Path.Value.(ast.Ref)
		allowed := ok && allowedImport(ref)
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

// Import paths are AST refs: OPA quotes keyword segments when rendering them,
// so a textual dot-prefix check would reject supported imports like `in`.
func allowedImport(ref ast.Ref) bool {
	if len(ref) == 0 {
		return false
	}
	root, ok := ref[0].Value.(ast.Var)
	if !ok {
		return false
	}
	switch root {
	case "input", "data":
		return true
	case "rego":
		return len(ref) == 2 && ref[1].Value == ast.String("v1")
	case "future":
		if len(ref) < 2 || ref[1].Value != ast.String("keywords") {
			return false
		}
		if len(ref) == 2 {
			return true
		}
		if len(ref) == 3 {
			keyword, ok := ref[2].Value.(ast.String)
			return ok && slices.Contains(languageKeywords(), string(keyword))
		}
	}
	return false
}

func headName(head *ast.Head) string {
	ref := head.Ref()
	if len(ref) == 0 {
		return ""
	}
	return ref[0].Value.String()
}
