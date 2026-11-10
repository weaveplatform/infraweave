package expr

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The kinds of completion item.
const (
	KindVariable = "variable"
	KindField    = "field"
	KindFunction = "function"
	KindMacro    = "macro"
)

// Completion is one item an editor may insert at a cursor: its label, its
// kind, a detail (a variable's or field's type, or a function's
// signatures) and the range of the source its label replaces.
type Completion struct {
	Label       string
	Kind        string
	Detail      string
	Description string
	Range       kernel.Range
}

// ErrCursor is a cursor that lies outside the source it completes, a
// request the registry cannot complete at all.
var ErrCursor = fmt.Errorf("%w: the cursor lies outside the source", ErrRequest)

// trailing is the dotted path a cursor ends: the identifiers before the
// last dot, and the one being typed after it, which may be empty.
var trailing = regexp.MustCompile(
	`([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\.)?([A-Za-z_][A-Za-z0-9_]*)?$`,
)

// Complete lists what req's source may continue with at the cursor at:
// after a dot, a variable's fields, which its schema names or, for a map,
// which sample holds, a namespace's functions, and the functions called
// on a value; otherwise the variables, functions and macros. Each item
// starts with what is typed before the cursor. A template's cursor
// outside a ${{ … }} has none; a cursor outside the source is ErrCursor.
//
// ref: decision 0024 "Editor intelligence"
func (r *Registry) Complete(
	req Request,
	at kernel.Position,
	sample map[string]any,
) ([]Completion, error) {
	vars, err := r.declared(req)
	if err != nil {
		return nil, err
	}
	if req.Form != FormExpression && req.Form != FormTemplate {
		return nil, fmt.Errorf(
			"%w: form %q is neither expression nor template",
			ErrRequest,
			req.Form,
		)
	}
	before, ok := upTo(req.Source, at)
	if !ok {
		return nil, fmt.Errorf("%w: %d:%d", ErrCursor, at.Line, at.Column)
	}
	if req.Form == FormTemplate {
		i := strings.LastIndex(before, open)
		if i < 0 || strings.Contains(before[i:], closing) || i > 0 && before[i-1] == '$' {
			return []Completion{}, nil
		}
		before = before[i+len(open):]
	}
	m := trailing.FindStringSubmatch(before)
	path, typed := strings.TrimSuffix(m[1], "."), m[2]
	// A dot after something other than a name, as in [1].a, calls a
	// function on a value.
	called := path == "" && strings.HasSuffix(before[:len(before)-len(m[0])], ".")
	from := kernel.Position{Line: at.Line, Column: at.Column - utf8.RuneCountInString(typed)}
	span := kernel.Range{Start: from, End: at}
	var out []Completion
	add := func(label, kind, detail, desc string) {
		if strings.HasPrefix(label, typed) {
			out = append(out, Completion{
				Label: label, Kind: kind, Detail: detail, Description: desc, Range: span,
			})
		}
	}
	funcs := r.Functions()
	if path == "" && !called {
		for _, v := range vars {
			s, err := parseSchema(v.Schema)
			if err != nil {
				return nil, fmt.Errorf("%w: variable %s's %w", ErrRequest, v.Name, err)
			}
			add(v.Name, KindVariable, typeOf(s), "")
		}
		add(Now, KindVariable, "google.protobuf.Timestamp", "The evaluation's fixed time.")
		for _, f := range funcs {
			if global(f) {
				add(f.Name, kindOf(f), strings.Join(f.Signatures, "\n"), f.Description)
			}
		}
		return out, nil
	}
	if fields, ok := fieldsOf(vars, sample, path); ok {
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			add(name, KindField, fields[name], "")
		}
	}
	declared := slices.ContainsFunc(vars, func(v Variable) bool {
		return v.Name == strings.SplitN(path, ".", 2)[0]
	})
	namespace := !declared && path != "" && slices.ContainsFunc(funcs, func(f Function) bool {
		return strings.HasPrefix(f.Name, path+".")
	})
	for _, f := range funcs {
		switch {
		case namespace && strings.HasPrefix(f.Name, path+"."):
			add(strings.TrimPrefix(f.Name, path+"."), kindOf(f), strings.Join(f.Signatures, "\n"),
				f.Description)
		case !namespace && member(f):
			add(f.Name, kindOf(f), strings.Join(f.Signatures, "\n"), f.Description)
		}
	}
	return out, nil
}

// upTo is the source before the position at, which may be just past its
// end; false when at lies outside it.
func upTo(src string, at kernel.Position) (string, bool) {
	pos := kernel.Position{Line: 1, Column: 1}
	for i, r := range src {
		if pos == at {
			return src[:i], true
		}
		pos = advance(pos, string(r))
	}
	return src, pos == at
}

// member reports whether f is called on a value, as v.f(…).
func member(f Function) bool {
	return !strings.Contains(f.Name, ".") && slices.ContainsFunc(f.Signatures, func(s string) bool {
		return strings.Contains(s, "."+f.Name+"(")
	})
}

// global reports whether f is called by its name alone, as f(…).
func global(f Function) bool {
	return !strings.Contains(f.Name, ".") && slices.ContainsFunc(f.Signatures, func(s string) bool {
		return strings.HasPrefix(s, f.Name+"(")
	})
}

func kindOf(f Function) string {
	if f.Macro {
		return KindMacro
	}
	return KindFunction
}

// fieldsOf are the fields of the value path names, with their types: an
// object's from its schema, a map's from the keys sample holds at path.
// False when path names no object or map.
func fieldsOf(vars []Variable, sample map[string]any, path string) (map[string]string, bool) {
	names := strings.Split(path, ".")
	i := slices.IndexFunc(vars, func(v Variable) bool { return v.Name == names[0] })
	if i < 0 {
		return nil, false
	}
	s, err := parseSchema(vars[i].Schema)
	if err != nil {
		return nil, false
	}
	value := sample[names[0]]
	for _, n := range names[1:] {
		next, ok := s.Properties[n]
		if !ok {
			if !s.open() {
				return nil, false
			}
			next = s.additional()
		}
		s = next
		m, _ := value.(map[string]any)
		value = m[n]
	}
	if s.kind() != "object" && len(s.Properties) == 0 {
		return nil, false
	}
	out := map[string]string{}
	for n, p := range s.Properties {
		out[n] = typeOf(p)
	}
	if m, ok := value.(map[string]any); ok && (len(s.Properties) == 0 || s.open()) {
		extra := typeOf(s.additional())
		for n := range m {
			if _, ok := out[n]; !ok {
				out[n] = extra
			}
		}
	}
	return out, true
}

// typeOf is the CEL type a schema declares, as an item's detail shows it.
func typeOf(s *schema) string {
	switch s.kind() {
	case "string":
		return "string"
	case "integer":
		return "int"
	case "number":
		return "double"
	case "boolean":
		return "bool"
	case "null":
		return "null"
	case "array":
		if s.Items == nil {
			return "list(dyn)"
		}
		return "list(" + typeOf(s.Items) + ")"
	case "object":
		if len(s.Properties) > 0 && !s.open() {
			return "object"
		}
		if s.open() {
			return "map(string, " + typeOf(s.additional()) + ")"
		}
		return "map(string, dyn)"
	}
	return "dyn"
}
