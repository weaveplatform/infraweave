package specref

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// flowsProviderCall is the generic function terraform-provider-flows sends
// every RPC through: CallFlowsAPI[Req, Res](config, path, request).
const flowsProviderCall = "CallFlowsAPI"

// ErrFlowsSource reports client source the extractors cannot read as an RPC
// surface, such as an RPC path outside a call or a type they cannot express.
var ErrFlowsSource = errors.New("flows client source")

// providerSource is the provider's Go package: its struct types, its string
// constants and the files they came from, for messages.
type providerSource struct {
	fset    *token.FileSet
	files   []*ast.File
	structs map[string]*ast.StructType
	consts  map[string]string
}

// ReadFlowsProviderTarball extracts every RPC terraform-provider-flows makes
// from its source: each path passed to CallFlowsAPI, with the request and
// response types it is instantiated with. A "/provider/..." literal that no
// call uses fails, so a path the provider starts sending some other way is
// noticed rather than dropped.
func ReadFlowsProviderTarball(tgz []byte) (*RPCSnapshot, error) {
	src := &providerSource{
		fset:    token.NewFileSet(),
		structs: map[string]*ast.StructType{},
		consts:  map[string]string{},
	}
	err := walkTarball(tgz, func(p string, content []byte) error {
		if path.Dir(p) != "internal/provider" || !strings.HasSuffix(p, ".go") ||
			strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(src.fset, p, content, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		src.files = append(src.files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(src.files) == 0 {
		return nil, fmt.Errorf("%w: no Go files under internal/provider", ErrFlowsSource)
	}
	src.index()
	return src.rpcs()
}

func (s *providerSource) index() {
	for _, f := range s.files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if st, ok := spec.Type.(*ast.StructType); ok {
						s.structs[spec.Name.Name] = st
					}
				case *ast.ValueSpec:
					for i, name := range spec.Names {
						if i < len(spec.Values) {
							if v, ok := stringLit(spec.Values[i]); ok {
								s.consts[name.Name] = v
							}
						}
					}
				}
			}
		}
	}
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// call is one CallFlowsAPI call site.
type providerCall struct {
	path     string
	req, res ast.Expr
	pos      token.Position
}

func (s *providerSource) rpcs() (*RPCSnapshot, error) {
	var calls []providerCall
	literals := map[string]token.Position{}
	used := map[string]bool{}
	var errs []error
	for _, f := range s.files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if v, ok := stringLit(n); ok && strings.HasPrefix(v, "/provider/") {
					literals[v] = s.fset.Position(n.Pos())
				}
			case *ast.CallExpr:
				c, ok, err := s.call(n)
				if err != nil {
					errs = append(errs, err)
				}
				if ok {
					calls = append(calls, c)
					used[c.path] = true
				}
			}
			return true
		})
	}
	for lit, pos := range literals {
		if !used[lit] {
			errs = append(errs, fmt.Errorf("%w: %s: %s is not passed to %s",
				ErrFlowsSource, pos, lit, flowsProviderCall))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s.snapshot(calls)
}

// call recognises CallFlowsAPI[Req, Res](config, path, request), the path a
// literal or a package constant.
func (s *providerSource) call(n *ast.CallExpr) (providerCall, bool, error) {
	idx, ok := n.Fun.(*ast.IndexListExpr)
	if !ok || len(idx.Indices) != 2 || len(n.Args) != 3 {
		return providerCall{}, false, nil
	}
	if id, ok := idx.X.(*ast.Ident); !ok || id.Name != flowsProviderCall {
		return providerCall{}, false, nil
	}
	pos := s.fset.Position(n.Pos())
	var p string
	switch a := n.Args[1].(type) {
	case *ast.BasicLit:
		p, ok = stringLit(a)
	case *ast.Ident:
		p, ok = s.consts[a.Name]
	default:
		ok = false
	}
	if !ok {
		return providerCall{}, false, fmt.Errorf(
			"%w: %s: the path is not a string literal or constant",
			ErrFlowsSource,
			pos,
		)
	}
	return providerCall{path: p, req: idx.Indices[0], res: idx.Indices[1], pos: pos}, true, nil
}

func (s *providerSource) snapshot(calls []providerCall) (*RPCSnapshot, error) {
	snap := &RPCSnapshot{Types: map[string]*Shape{}}
	byPath := map[string]providerCall{}
	conv := &goShapes{src: s, types: snap.Types}
	for _, c := range calls {
		if prev, ok := byPath[c.path]; ok {
			if s.text(prev.req) != s.text(c.req) || s.text(prev.res) != s.text(c.res) {
				return nil, fmt.Errorf("%w: %s: %s is called with [%s, %s] here and [%s, %s] at %s",
					ErrFlowsSource, c.pos, c.path, s.text(c.req), s.text(c.res),
					s.text(prev.req), s.text(prev.res), prev.pos)
			}
			continue
		}
		byPath[c.path] = c
		req, err := conv.top(c.req)
		if err != nil {
			return nil, fmt.Errorf("%s request: %w", c.path, err)
		}
		res, err := conv.top(c.res)
		if err != nil {
			return nil, fmt.Errorf("%s response: %w", c.path, err)
		}
		snap.RPCs = append(snap.RPCs, RPC{Path: c.path, Request: req, Response: res})
	}
	if err := conv.drain(); err != nil {
		return nil, err
	}
	sort.Slice(snap.RPCs, func(i, j int) bool { return snap.RPCs[i].Path < snap.RPCs[j].Path })
	return snap, nil
}

func (s *providerSource) text(e ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, s.fset, e)
	return b.String()
}

// goShapes converts Go types to shapes. A request or response type is
// expanded in place, so its fields can become path and query parameters; a
// named struct it reaches becomes a named shape in types.
type goShapes struct {
	src     *providerSource
	types   map[string]*Shape
	pending []string
}

func (g *goShapes) top(e ast.Expr) (*Shape, error) {
	if id, ok := e.(*ast.Ident); ok {
		if st, ok := g.src.structs[id.Name]; ok {
			return g.object(st)
		}
	}
	return g.shape(e)
}

func (g *goShapes) drain() error {
	for len(g.pending) > 0 {
		name := g.pending[0]
		g.pending = g.pending[1:]
		s, err := g.object(g.src.structs[name])
		if err != nil {
			return fmt.Errorf("type %s: %w", name, err)
		}
		g.types[name] = s
	}
	return nil
}

func (g *goShapes) shape(e ast.Expr) (*Shape, error) {
	switch e := e.(type) {
	case *ast.Ident:
		switch e.Name {
		case "string":
			return &Shape{Type: "string"}, nil
		case "bool":
			return &Shape{Type: "boolean"}, nil
		case "int", "int32", "uint", "uint32":
			return &Shape{Type: "integer"}, nil
		case "int64", "uint64":
			return &Shape{Type: "integer", Format: "int64"}, nil
		case "float32", "float64":
			return &Shape{Type: "number"}, nil
		case "any":
			return &Shape{}, nil
		}
		if _, ok := g.src.structs[e.Name]; ok {
			if _, done := g.types[e.Name]; !done {
				g.types[e.Name] = nil
				g.pending = append(g.pending, e.Name)
			}
			return &Shape{Ref: e.Name}, nil
		}
	case *ast.StarExpr:
		s, err := g.shape(e.X)
		if err != nil {
			return nil, err
		}
		s.Nullable = true
		return s, nil
	case *ast.ArrayType:
		items, err := g.shape(e.Elt)
		if err != nil {
			return nil, err
		}
		return &Shape{Type: "array", Items: items}, nil
	case *ast.MapType:
		if k, ok := e.Key.(*ast.Ident); !ok || k.Name != "string" {
			break
		}
		values, err := g.shape(e.Value)
		if err != nil {
			return nil, err
		}
		return &Shape{Type: "object", Values: values}, nil
	case *ast.StructType:
		return g.object(e)
	case *ast.InterfaceType:
		return &Shape{}, nil
	case *ast.SelectorExpr:
		switch g.src.text(e) {
		case "time.Time":
			return &Shape{Type: "string", Format: "date-time"}, nil
		case "json.RawMessage":
			return &Shape{}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s: type %s has no JSON shape",
		ErrFlowsSource, g.src.fset.Position(e.Pos()), g.src.text(e))
}

// object follows encoding/json: the json tag names a field, "-" drops it,
// and a field without omitempty is always sent, so it is required.
func (g *goShapes) object(st *ast.StructType) (*Shape, error) {
	out := objectShape()
	for _, f := range st.Fields.List {
		name, omit, skip := jsonField(f)
		if skip {
			continue
		}
		s, err := g.shape(f.Type)
		if err != nil {
			return nil, err
		}
		out.Properties[name] = s
		if !omit {
			out.require(name)
		}
	}
	return out, nil
}

func jsonField(f *ast.Field) (name string, omitempty, skip bool) {
	if len(f.Names) != 1 || !f.Names[0].IsExported() {
		return "", false, true
	}
	name = f.Names[0].Name
	if f.Tag == nil {
		return name, false, false
	}
	raw, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return name, false, false
	}
	tag, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return name, false, false
	}
	if tag == "-" {
		return "", false, true
	}
	n, opts, _ := strings.Cut(tag, ",")
	if n != "" {
		name = n
	}
	return name, strings.Contains(","+opts+",", ",omitempty,"), false
}
