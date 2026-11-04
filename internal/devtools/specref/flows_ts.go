package specref

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// The TypeScript reader covers what flowctl's src/api.ts and the SDK's
// index.d.ts use: interfaces, type aliases, functions with typed parameters,
// object literals and `as` casts. It is not a TypeScript parser; a
// construct it cannot read is an error, so a change in the source's style is
// noticed rather than misread.

type tsToken struct {
	kind byte // 'i' identifier, 's' string, 'n' number, 'p' punctuation
	text string
	line int
}

func tsLex(src string) ([]tsToken, error) {
	var out []tsToken
	line := 1
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("%w: line %d: unterminated comment", ErrFlowsSource, line)
			}
			line += strings.Count(src[i:i+2+end], "\n")
			i += end + 4
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("%w: line %d: unterminated string", ErrFlowsSource, line)
			}
			text := src[i+1 : j]
			out = append(out, tsToken{kind: 's', text: text, line: line})
			line += strings.Count(text, "\n")
			i = j + 1
		case c == '_' || c == '$' || unicode.IsLetter(rune(c)):
			j := i
			for j < len(src) && (src[j] == '_' || src[j] == '$' || unicode.IsLetter(rune(src[j])) ||
				unicode.IsDigit(rune(src[j]))) {
				j++
			}
			out = append(out, tsToken{kind: 'i', text: src[i:j], line: line})
			i = j
		case unicode.IsDigit(rune(c)):
			j := i
			for j < len(src) && (unicode.IsDigit(rune(src[j])) || src[j] == '.') {
				j++
			}
			out = append(out, tsToken{kind: 'n', text: src[i:j], line: line})
			i = j
		default:
			n := 1
			for _, op := range []string{"...", "=>", "?.", "??"} {
				if strings.HasPrefix(src[i:], op) {
					n = len(op)
					break
				}
			}
			out = append(out, tsToken{kind: 'p', text: src[i : i+n], line: line})
			i += n
		}
	}
	return out, nil
}

// tsParser walks a token stream. Its methods report a parse error as
// ErrFlowsSource with the line.
type tsParser struct {
	toks []tsToken
	pos  int
}

func (p *tsParser) peek() tsToken {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return tsToken{kind: 'e'}
}

func (p *tsParser) at(text string) bool {
	t := p.peek()
	return t.kind != 's' && t.text == text
}

func (p *tsParser) next() tsToken {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *tsParser) accept(text string) bool {
	if p.at(text) {
		p.pos++
		return true
	}
	return false
}

func (p *tsParser) errorf(format string, args ...any) error {
	return fmt.Errorf("%w: line %d: %s", ErrFlowsSource, p.peek().line, fmt.Sprintf(format, args...))
}

func (p *tsParser) expect(text string) error {
	if !p.accept(text) {
		return p.errorf("want %q, have %q", text, p.peek().text)
	}
	return nil
}

func (p *tsParser) ident() (string, error) {
	t := p.next()
	if t.kind != 'i' {
		p.pos--
		return "", p.errorf("want a name, have %q", t.text)
	}
	return t.text, nil
}

// typ parses a type expression: a union of postfix types. A union with null
// or undefined is the other members, nullable; a union of string literals is
// an enum.
func (p *tsParser) typ() (*Shape, error) {
	p.accept("|")
	var members []*Shape
	nullable := false
	for {
		s, err := p.postfix()
		if err != nil {
			return nil, err
		}
		if s == nil {
			nullable = true
		} else {
			members = append(members, s)
		}
		if !p.accept("|") {
			break
		}
	}
	out, err := unionShape(members)
	if err != nil {
		return nil, p.errorf("%v", err)
	}
	out.Nullable = out.Nullable || nullable
	return out, nil
}

func unionShape(members []*Shape) (*Shape, error) {
	switch len(members) {
	case 0:
		return &Shape{}, nil
	case 1:
		return members[0], nil
	}
	enum := &Shape{Type: "string"}
	for _, m := range members {
		if m.Type != "string" || len(m.Enum) != 1 {
			return nil, fmt.Errorf("a union of %s and %s is not a string enum",
				members[0].describe(), m.describe())
		}
		enum.Enum = append(enum.Enum, m.Enum[0])
	}
	return enum, nil
}

func (p *tsParser) postfix() (*Shape, error) {
	s, err := p.primary()
	if err != nil {
		return nil, err
	}
	for p.at("[") && p.pos+1 < len(p.toks) && p.toks[p.pos+1].text == "]" {
		p.pos += 2
		s = &Shape{Type: "array", Items: s}
	}
	return s, nil
}

// primary returns nil for null and undefined, which only make a union
// nullable.
func (p *tsParser) primary() (*Shape, error) {
	t := p.peek()
	switch {
	case t.kind == 's':
		p.next()
		return &Shape{Type: "string", Enum: []string{t.text}}, nil
	case t.kind == 'n':
		p.next()
		return &Shape{Type: "number"}, nil
	case t.text == "{":
		return p.objectType()
	case t.text == "(":
		p.next()
		s, err := p.typ()
		if err != nil {
			return nil, err
		}
		return s, p.expect(")")
	case t.kind != 'i':
		return nil, p.errorf("want a type, have %q", t.text)
	}
	p.next()
	switch t.text {
	case "string":
		return &Shape{Type: "string"}, nil
	case "number":
		return &Shape{Type: "number"}, nil
	case "boolean":
		return &Shape{Type: "boolean"}, nil
	case "true", "false":
		return &Shape{Type: "boolean"}, nil
	case "any", "unknown":
		return &Shape{}, nil
	case "null", "undefined":
		return nil, nil
	case "Array":
		args, err := p.typeArgs(1)
		if err != nil {
			return nil, err
		}
		return &Shape{Type: "array", Items: args[0]}, nil
	case "Record":
		args, err := p.typeArgs(2)
		if err != nil {
			return nil, err
		}
		if args[0].Type != "string" {
			return nil, p.errorf("Record keys must be string")
		}
		return &Shape{Type: "object", Values: args[1]}, nil
	}
	if p.at("<") {
		return nil, p.errorf("generic type %s is not supported", t.text)
	}
	return &Shape{Ref: t.text}, nil
}

func (p *tsParser) typeArgs(n int) ([]*Shape, error) {
	if err := p.expect("<"); err != nil {
		return nil, err
	}
	var out []*Shape
	for i := range n {
		if i > 0 {
			if err := p.expect(","); err != nil {
				return nil, err
			}
		}
		s, err := p.typ()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, p.expect(">")
}

// objectType parses { name?: T; ... }; members end in ";" or ",".
func (p *tsParser) objectType() (*Shape, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	out := objectShape()
	for !p.accept("}") {
		p.accept("readonly")
		name, err := p.memberName()
		if err != nil {
			return nil, err
		}
		optional := p.accept("?")
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		s, err := p.typ()
		if err != nil {
			return nil, err
		}
		out.Properties[name] = s
		if !optional {
			out.require(name)
		}
		if !p.accept(";") {
			p.accept(",")
		}
	}
	return out, nil
}

func (p *tsParser) memberName() (string, error) {
	t := p.next()
	if t.kind == 'i' || t.kind == 's' {
		return t.text, nil
	}
	p.pos--
	return "", p.errorf("want a member name, have %q", t.text)
}

// skipBalanced skips from an opening bracket to its match.
func (p *tsParser) skipBalanced() error {
	open := p.next().text
	closeFor := map[string]string{"{": "}", "(": ")", "[": "]", "<": ">"}[open]
	depth := 1
	for depth > 0 {
		t := p.next()
		switch {
		case t.kind == 'e':
			return p.errorf("unbalanced %q", open)
		case t.kind != 's' && t.text == open:
			depth++
		case t.kind != 's' && t.text == closeFor:
			depth--
		}
	}
	return nil
}

// ReadFlowctlAPI extracts every RPC flowctl's src/api.ts makes: each
// apiFetch("/cli/...", body) call, the body typed from the enclosing
// function's parameters, and the response from the properties the function
// reads off the result, typed by their `as` casts. A property read without a
// cast is any. A "/cli/..." string outside an apiFetch call fails.
func ReadFlowctlAPI(src []byte) (*RPCSnapshot, error) {
	toks, err := tsLex(string(src))
	if err != nil {
		return nil, err
	}
	snap := &RPCSnapshot{Types: map[string]*Shape{}}
	byPath := map[string]*RPC{}
	p := &tsParser{toks: toks}
	for p.peek().kind != 'e' {
		start := p.pos
		p.accept("export")
		switch {
		case p.accept("interface"):
			name, err := p.ident()
			if err != nil {
				return nil, err
			}
			s, err := p.objectType()
			if err != nil {
				return nil, err
			}
			snap.Types[name] = s
		case p.accept("type"):
			name, err := p.ident()
			if err != nil {
				return nil, err
			}
			if err := p.expect("="); err != nil {
				return nil, err
			}
			s, err := p.typ()
			if err != nil {
				return nil, err
			}
			p.accept(";")
			snap.Types[name] = s
		case p.at("async") || p.at("function"):
			p.accept("async")
			p.next()
			fn, err := p.function()
			if err != nil {
				return nil, err
			}
			for _, rpc := range fn {
				if err := addRPC(byPath, rpc); err != nil {
					return nil, err
				}
			}
		default:
			p.pos = start
			if t := p.next(); t.kind == 's' && strings.HasPrefix(t.text, "/cli/") {
				return nil, fmt.Errorf("%w: line %d: %s is not passed to apiFetch",
					ErrFlowsSource, t.line, t.text)
			}
		}
	}
	for _, rpc := range byPath {
		snap.RPCs = append(snap.RPCs, *rpc)
	}
	sort.Slice(snap.RPCs, func(i, j int) bool { return snap.RPCs[i].Path < snap.RPCs[j].Path })
	if err := resolveSpreads(snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// addRPC records a call, merging the bodies of functions that call the same
// path: publishVersion and updateVersion both call /cli/apps/update_version.
func addRPC(byPath map[string]*RPC, rpc RPC) error {
	prev, ok := byPath[rpc.Path]
	if !ok {
		byPath[rpc.Path] = &rpc
		return nil
	}
	req, err := merged(prev.Request, rpc.Request)
	if err != nil {
		return fmt.Errorf("%s: %w", rpc.Path, err)
	}
	res, err := merged(prev.Response, rpc.Response)
	if err != nil {
		return fmt.Errorf("%s: %w", rpc.Path, err)
	}
	prev.Request, prev.Response = req, res
	prev.Unenveloped = prev.Unenveloped || rpc.Unenveloped
	return nil
}

// spreadPrefix marks a body property spread from a parameter, ...options,
// until resolveSpreads replaces it with the fields of the parameter's type.
const spreadPrefix = "..."

func resolveSpreads(snap *RPCSnapshot) error {
	for i := range snap.RPCs {
		req := snap.RPCs[i].Request
		for name, s := range req.Properties {
			if !strings.HasPrefix(name, spreadPrefix) {
				continue
			}
			t, ok := snap.Types[s.Ref]
			if s.Ref == "" || !ok || t.Type != "object" {
				return fmt.Errorf("%w: %s spreads %s, which is not an interface",
					ErrFlowsSource, snap.RPCs[i].Path, strings.TrimPrefix(name, spreadPrefix))
			}
			delete(req.Properties, name)
			for f, fs := range t.Properties {
				req.Properties[f] = fs
			}
			for _, f := range t.Required {
				req.require(f)
			}
		}
	}
	return nil
}

// function parses name(params) [: T] { body } and returns the RPCs its body
// makes.
func (p *tsParser) function() ([]RPC, error) {
	if _, err := p.ident(); err != nil {
		return nil, err
	}
	params, err := p.params()
	if err != nil {
		return nil, err
	}
	// A return type is skipped, not read: the response comes from the
	// reads off apiFetch's result.
	if p.accept(":") {
		for !p.at("{") && p.peek().kind != 'e' {
			if p.at("<") || p.at("(") || p.at("[") {
				if err := p.skipBalanced(); err != nil {
					return nil, err
				}
				continue
			}
			p.next()
		}
	}
	if !p.at("{") {
		return nil, p.errorf("want a function body, have %q", p.peek().text)
	}
	start := p.pos
	if err := p.skipBalanced(); err != nil {
		return nil, err
	}
	body := &tsParser{toks: p.toks[start+1 : p.pos-1]}
	return body.calls(params)
}

func (p *tsParser) params() (map[string]*Shape, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	out := map[string]*Shape{}
	for !p.accept(")") {
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		s := &Shape{}
		if p.accept(":") {
			if s, err = p.typ(); err != nil {
				return nil, err
			}
		}
		if p.accept("=") {
			if p.at("{") || p.at("[") || p.at("(") {
				if err := p.skipBalanced(); err != nil {
					return nil, err
				}
			} else {
				p.next()
			}
		}
		out[name] = s
		p.accept(",")
	}
	return out, nil
}

// calls finds apiFetch calls in a function body.
func (p *tsParser) calls(params map[string]*Shape) ([]RPC, error) {
	var out []RPC
	for p.peek().kind != 'e' {
		t := p.next()
		if t.kind == 's' && strings.HasPrefix(t.text, "/cli/") {
			return nil, fmt.Errorf("%w: line %d: %s is not passed to apiFetch",
				ErrFlowsSource, t.line, t.text)
		}
		if t.text != "apiFetch" || !p.at("(") {
			continue
		}
		result := ""
		if p.pos >= 4 && p.toks[p.pos-2].text == "await" && p.toks[p.pos-3].text == "=" {
			result = p.toks[p.pos-4].text
		}
		p.next()
		pathTok := p.next()
		if pathTok.kind != 's' {
			return nil, p.errorf("apiFetch's path is not a string literal")
		}
		if err := p.expect(","); err != nil {
			return nil, err
		}
		req, err := p.objectLiteral(params)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		rpc := RPC{Path: pathTok.text, Request: req, Response: objectShape()}
		if result != "" {
			rest := &tsParser{toks: p.toks[p.pos:]}
			if err := rest.reads(result, &rpc); err != nil {
				return nil, err
			}
		}
		out = append(out, rpc)
	}
	return out, nil
}

// objectLiteral types { key, key: value, ...spread } from the parameters:
// a shorthand or a parameter value takes the parameter's type, a literal
// its own, and a nested literal is an object.
func (p *tsParser) objectLiteral(params map[string]*Shape) (*Shape, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	out := objectShape()
	for !p.accept("}") {
		if p.accept("...") {
			name, err := p.ident()
			if err != nil {
				return nil, err
			}
			out.Properties[spreadPrefix+name] = params[name]
			p.accept(",")
			continue
		}
		key, err := p.memberName()
		if err != nil {
			return nil, err
		}
		var s *Shape
		if p.accept(":") {
			if s, err = p.value(params); err != nil {
				return nil, err
			}
		} else if s = params[key]; s == nil {
			return nil, p.errorf("%s is not a parameter", key)
		}
		out.Properties[key] = s
		if !s.Nullable {
			out.require(key)
		}
		p.accept(",")
	}
	return out, nil
}

func (p *tsParser) value(params map[string]*Shape) (*Shape, error) {
	t := p.peek()
	switch {
	case t.text == "{":
		return p.objectLiteral(params)
	case t.kind == 's':
		p.next()
		return &Shape{Type: "string"}, nil
	case t.kind == 'n':
		p.next()
		return &Shape{Type: "number"}, nil
	case t.text == "true" || t.text == "false":
		p.next()
		return &Shape{Type: "boolean"}, nil
	case t.kind == 'i' && params[t.text] != nil:
		p.next()
		return params[t.text], nil
	}
	return nil, p.errorf("cannot type the value %q", t.text)
}

// reads records the properties read off result: result.data.a.b as T makes
// the response { a: { b: T } }, and a read that does not start at data is
// from the top level of the body.
func (p *tsParser) reads(result string, rpc *RPC) error {
	for p.peek().kind != 'e' {
		if p.next().text != result || (!p.at(".") && !p.at("?.")) {
			continue
		}
		var chain []string
		for p.accept(".") || p.accept("?.") {
			name, err := p.ident()
			if err != nil {
				return err
			}
			chain = append(chain, name)
		}
		leaf := &Shape{}
		if p.accept("as") {
			s, err := p.typ()
			if err != nil {
				return err
			}
			leaf = s
		}
		if chain[0] == "data" {
			chain = chain[1:]
		} else {
			rpc.Unenveloped = true
		}
		if len(chain) == 0 {
			if leaf.Type != "object" {
				return p.errorf("the response data is cast to %s, not an object", leaf.describe())
			}
			rpc.Response = leaf
			continue
		}
		at := rpc.Response
		for i, name := range chain {
			if i == len(chain)-1 {
				at.Properties[name] = leaf
				break
			}
			child, ok := at.Properties[name]
			if !ok || child.Properties == nil {
				child = objectShape()
				at.Properties[name] = child
			}
			at = child
		}
	}
	return nil
}

// SDKIdentifier is one declaration of the app SDK's index.d.ts, with the
// names of its members: identifiers only, since the SDK states no licence.
type SDKIdentifier struct {
	Name     string      `json:"id"`
	Kind     string      `json:"kind"`
	Exported bool        `json:"exported,omitempty"`
	Members  []SDKMember `json:"members,omitempty"`
}

// SDKMember is a property or method of an interface or of a declared
// object constant.
type SDKMember struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional,omitempty"`
}

// ReadSDKIdentifiers lists the interfaces, type aliases, constants and
// functions a declaration file declares, with their member names, and marks
// those its export lists name.
func ReadSDKIdentifiers(src []byte) ([]SDKIdentifier, error) {
	toks, err := tsLex(string(src))
	if err != nil {
		return nil, err
	}
	p := &tsParser{toks: toks}
	var out []SDKIdentifier
	exported := map[string]bool{}
	for p.peek().kind != 'e' {
		t := p.next()
		if t.text == "export" && (p.at("{") || (p.at("type") && p.pos+1 < len(toks) &&
			toks[p.pos+1].text == "{")) {
			p.accept("type")
			names, err := p.exportList()
			if err != nil {
				return nil, err
			}
			for _, n := range names {
				exported[n] = true
			}
			continue
		}
		kind := map[string]string{
			"interface": "interface", "type": "type", "const": "const", "function": "function",
		}[t.text]
		if kind == "" || t.kind != 'i' {
			continue
		}
		if prev := p.pos - 2; kind != "interface" && kind != "type" &&
			(prev < 0 || toks[prev].text != "declare") {
			continue
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		id := SDKIdentifier{Name: name, Kind: kind}
		switch kind {
		case "interface":
			for !p.at("{") && p.peek().kind != 'e' {
				p.next()
			}
			id.Members, err = p.members()
		case "const":
			if p.accept(":") && p.at("{") {
				id.Members, err = p.members()
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if kind != "interface" {
			p.skipStatement()
		}
		out = append(out, id)
	}
	for i := range out {
		out[i].Exported = exported[out[i].Name]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// skipStatement skips to the ";" ending a declaration, past any brackets,
// so the members of a type alias's object type are not read as declarations.
func (p *tsParser) skipStatement() {
	depth := 0
	for {
		t := p.next()
		if t.kind == 'e' || (t.kind == 'p' && t.text == ";" && depth == 0) {
			return
		}
		if t.kind != 'p' {
			continue
		}
		switch t.text {
		case "{", "(", "[":
			depth++
		case "}", ")", "]":
			depth--
		}
	}
}

func (p *tsParser) exportList() ([]string, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var out []string
	for !p.accept("}") {
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		out = append(out, name)
		p.accept(",")
	}
	return out, nil
}

// members lists the member names of a { ... } type body: at its top level, a
// name (or quoted name) followed by ?, :, ( or < after the opening brace or
// a separator.
func (p *tsParser) members() ([]SDKMember, error) {
	start := p.pos
	if err := p.skipBalanced(); err != nil {
		return nil, err
	}
	body := p.toks[start+1 : p.pos-1]
	var out []SDKMember
	depth := 0
	atStart := true
	for i := 0; i < len(body); i++ {
		t := body[i]
		if t.kind == 'p' {
			switch t.text {
			case "{", "(", "[", "<":
				depth++
			case "}", ")", "]", ">":
				depth--
			case ";", ",":
				if depth == 0 {
					atStart = true
					continue
				}
			}
		}
		if depth != 0 || !atStart {
			continue
		}
		atStart = false
		if t.text == "readonly" && i+1 < len(body) && body[i+1].kind == 'i' {
			i++
			t = body[i]
		}
		if (t.kind != 'i' && t.kind != 's') || i+1 >= len(body) {
			continue
		}
		m := SDKMember{Name: t.text}
		after := body[i+1].text
		if after == "?" {
			m.Optional = true
			if i+2 < len(body) {
				after = body[i+2].text
			}
		}
		if after == ":" || after == "(" || after == "<" {
			out = append(out, m)
		}
	}
	return out, nil
}
