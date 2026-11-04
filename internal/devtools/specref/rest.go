package specref

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Route styles record which rule placed an operation, so the coverage
// report can show how much of the surface is resource-shaped and how much
// still needs design.
const (
	StyleOverride  = "override"
	StyleItem      = "item"
	StyleList      = "list"
	StyleSearch    = "search"
	StyleRead      = "read"
	StyleSubRead   = "sub-read"
	StyleSingleton = "singleton"
	StyleCreate    = "create"
	StyleUpdate    = "update"
	StyleDelete    = "delete"
	StyleAttach    = "attach"
	StyleDetach    = "detach"
	StyleAction    = "action"
	StyleField     = "field"
	StyleRPC       = "rpc"
)

const (
	OpQuery    = "query"
	OpMutation = "mutation"
)

var (
	ErrRouteCollision   = errors.New("routes collide")
	ErrRPCBudget        = errors.New("rpc fallbacks exceed rpcBudget")
	ErrStaleMapping     = errors.New("mapping names a field the schema does not have")
	ErrUnboundParameter = errors.New("path parameter not bound to an argument")
)

// PathParam is one {name} in a route path. Arg is the GraphQL argument it
// carries, or a dotted path to a field inside input-object arguments
// (input.id) when upstream hides the target's id in the input; for field
// routes the item parameter has no argument because it selects the parent
// object instead.
type PathParam struct {
	Name string
	Arg  string
}

// Route is a REST operation derived from a GraphQL root field, or from a
// field with arguments on a resource type (StyleField).
type Route struct {
	OperationID string
	Op          string // OpQuery or OpMutation
	Field       Field
	OwnerType   string // GraphQL type declaring Field: Query, Mutation or a resource type
	Method      string
	Path        string
	Params      []PathParam
	Style       string
	Resource    string
	Public      bool
	Tag         string
	Created     bool // answers 201 although the style is not create
}

// Bound reports whether a GraphQL argument travels in the path.
func (r *Route) Bound(arg string) bool {
	for _, p := range r.Params {
		if p.Arg == arg {
			return true
		}
	}
	return false
}

// HasBody reports whether unbound arguments travel in a JSON request body
// (true) or in the query string (false).
func (r *Route) HasBody() bool {
	return r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch
}

// Excluded is a root field deliberately left out of the reference.
type Excluded struct {
	Op     string
	Field  string
	Reason string
}

// Plan is the full derived REST surface.
type Plan struct {
	Routes   []*Route
	Excluded []Excluded
	// ItemTypes maps a GraphQL type to the item route that fetches it; such
	// types are embedded elsewhere as references, not inline.
	ItemTypes map[string]*Route
}

type planner struct {
	s          *Schema
	m          *Mapping
	resources  map[string]string // resource name → GraphQL type
	singletons map[string]bool
	attrArgs   map[string]bool
	plan       *Plan
	superseded map[string]string // older versioned field → its latest version
}

// BuildPlan derives the REST route of every root field.
func BuildPlan(s *Schema, m *Mapping) (*Plan, error) {
	p := &planner{
		s:          s,
		m:          m,
		resources:  map[string]string{},
		singletons: map[string]bool{},
		attrArgs:   map[string]bool{},
		plan:       &Plan{ItemTypes: map[string]*Route{}},
	}
	for _, n := range m.Singletons {
		p.singletons[n] = true
	}
	for _, n := range m.AttributeArgs {
		p.attrArgs[n] = true
	}
	if err := p.checkMapping(); err != nil {
		return nil, err
	}
	p.discoverResources()
	p.superseded = supersededVersions(s)
	for _, f := range s.Queries() {
		if err := p.add(OpQuery, f); err != nil {
			return nil, err
		}
	}
	for _, f := range s.Mutations() {
		if err := p.add(OpMutation, f); err != nil {
			return nil, err
		}
	}
	p.addFieldRoutes()
	if err := p.plan.check(m.RPCBudget); err != nil {
		return nil, err
	}
	return p.plan, nil
}

func (p *planner) checkMapping() error {
	roots := map[string]bool{}
	for _, f := range p.s.Queries() {
		roots[f.Name] = true
	}
	for _, f := range p.s.Mutations() {
		roots[f.Name] = true
	}
	var stale []string
	for name := range p.m.Operations {
		if !roots[name] {
			stale = append(stale, "operations."+name)
		}
		if _, ok := p.m.Exclude[name]; ok {
			stale = append(stale, "operations."+name+" is also excluded")
		}
	}
	for name := range p.m.Exclude {
		if !roots[name] {
			stale = append(stale, "exclude."+name)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("%w: %s", ErrStaleMapping, strings.Join(stale, ", "))
	}
	return nil
}

// discoverResources finds the addressable resources: anything a root query
// fetches by id, plus anything a Create/Add mutation makes.
func (p *planner) discoverResources() {
	for _, f := range p.s.Queries() {
		k := p.namedKind(f.Type)
		if !f.IsDeprecated && !f.Type.IsList() && argNamed(f, "id") != nil &&
			(k == KindObject || k == KindInterface || k == KindUnion) {
			p.resources[f.Name] = f.Type.Named().Name
		}
	}
	for _, f := range p.s.Mutations() {
		base, _ := stripVersion(f.Name)
		ws := words(base)
		if len(ws) < 2 || verbClass(ws[len(ws)-1]) != StyleCreate {
			continue
		}
		prefix := camel(ws[:len(ws)-1])
		if _, ok := p.resources[prefix]; !ok && !p.singletons[prefix] {
			p.resources[prefix] = f.Type.Named().Name
		}
	}
}

func (p *planner) namedKind(t TypeRef) string {
	if nt := p.s.Type(t.Named().Name); nt != nil {
		return nt.Kind
	}
	return ""
}

func verbClass(verb string) string {
	switch verb {
	case "Create", "Add":
		return StyleCreate
	case "Update":
		return StyleUpdate
	case "Delete", "Remove":
		return StyleDelete
	case "Attach":
		return StyleAttach
	case "Detach":
		return StyleDetach
	default:
		return StyleAction
	}
}

// supersededVersions maps each versioned root field to the newest live
// version of the same operation: moduleUpdate and moduleUpdateV2 would share
// a route, and the REST surface only needs the current one.
func supersededVersions(s *Schema) map[string]string {
	out := map[string]string{}
	for _, fields := range [][]Field{s.Queries(), s.Mutations()} {
		byBase := map[string][]Field{}
		for _, f := range fields {
			if f.IsDeprecated {
				continue
			}
			base, _ := stripVersion(f.Name)
			byBase[base] = append(byBase[base], f)
		}
		for _, group := range byBase {
			if len(group) < 2 {
				continue
			}
			sort.Slice(
				group,
				func(i, j int) bool { return versionRank(group[i].Name) < versionRank(group[j].Name) },
			)
			latest := group[len(group)-1].Name
			for _, f := range group[:len(group)-1] {
				out[f.Name] = latest
			}
		}
	}
	return out
}

// versionRank orders contextCreate (1) before contextCreateV2 (2).
func versionRank(name string) int {
	_, v := stripVersion(name)
	if v == "" {
		return 1
	}
	n := 0
	for _, c := range v[1:] {
		n = n*10 + int(c-'0')
	}
	return n
}

func argNamed(f Field, name string) *InputValue {
	for i := range f.Args {
		if f.Args[i].Name == name {
			return &f.Args[i]
		}
	}
	return nil
}

func inputField(t *Type, name string) *InputValue {
	for i := range t.InputFields {
		if t.InputFields[i].Name == name {
			return &t.InputFields[i]
		}
	}
	return nil
}

// argPath resolves a path parameter binding: an argument (id) or a field
// inside input-object arguments (input.customInput.id). Lists are never
// entered, because one path segment has to name exactly one value.
func (s *Schema) argPath(f Field, path string) *InputValue {
	parts := strings.Split(path, ".")
	v := argNamed(f, parts[0])
	for _, name := range parts[1:] {
		if v == nil || v.Type.IsList() {
			return nil
		}
		t := s.Type(v.Type.Named().Name)
		if t == nil || t.Kind != KindInputObject {
			return nil
		}
		v = inputField(t, name)
	}
	return v
}

// maxIDDepth bounds the search for an id inside inputs; the deepest real
// case is bitbucketCloudIntegrationUpdateV2(input: {customInput: {id}}).
const maxIDDepth = 3

// nestedID finds the one non-null id field inside f's input-object
// arguments, shallowest first. managedUserUpdate(input: {id}) updates the
// managed user that input.id names. Two candidates at the same depth are
// ambiguous and yield none.
func (s *Schema) nestedID(f Field) string {
	type node struct {
		path string
		v    InputValue
	}
	level := make([]node, 0, len(f.Args))
	for _, a := range f.Args {
		level = append(level, node{a.Name, a})
	}
	for range maxIDDepth {
		var found []string
		var next []node
		for _, n := range level {
			t := s.Type(n.v.Type.Named().Name)
			if n.v.Type.IsList() || t == nil || t.Kind != KindInputObject {
				continue
			}
			for _, fld := range t.InputFields {
				path := n.path + "." + fld.Name
				if fld.Name == "id" && fld.Type.NonNull() {
					found = append(found, path)
				}
				next = append(next, node{path, fld})
			}
		}
		if len(found) > 0 {
			if len(found) == 1 {
				return found[0]
			}
			return ""
		}
		level = next
	}
	return ""
}

func (p *planner) add(op string, f Field) error {
	if reason, ok := p.m.Exclude[f.Name]; ok {
		p.plan.Excluded = append(p.plan.Excluded, Excluded{Op: op, Field: f.Name, Reason: reason})
		return nil
	}
	if o, ok := p.m.Operations[f.Name]; ok {
		r, err := p.override(op, f, o)
		if err != nil {
			return err
		}
		p.plan.Routes = append(p.plan.Routes, r)
		return nil
	}
	if f.IsDeprecated {
		reason := "deprecated upstream"
		if f.DeprecationReason != "" {
			reason += ": " + f.DeprecationReason
		}
		p.plan.Excluded = append(p.plan.Excluded, Excluded{Op: op, Field: f.Name, Reason: reason})
		return nil
	}
	if latest, ok := p.superseded[f.Name]; ok {
		p.plan.Excluded = append(
			p.plan.Excluded,
			Excluded{Op: op, Field: f.Name, Reason: "superseded by " + latest},
		)
		return nil
	}
	var r *Route
	if op == OpQuery {
		r = p.query(f)
	} else {
		r = p.mutation(f)
	}
	r.Op, r.Field, r.OperationID = op, f, f.Name
	r.OwnerType = p.rootTypeName(op)
	p.plan.Routes = append(p.plan.Routes, r)
	if r.Style == StyleItem {
		if _, ok := p.plan.ItemTypes[r.Field.Type.Named().Name]; !ok {
			p.plan.ItemTypes[r.Field.Type.Named().Name] = r
		}
	}
	return nil
}

func (p *planner) rootTypeName(op string) string {
	if op == OpQuery {
		return p.s.QueryType.Name
	}
	return p.s.MutationType.Name
}

var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

func (p *planner) override(op string, f Field, o OperationOverride) (*Route, error) {
	r := &Route{
		OperationID: f.Name,
		Op:          op,
		Field:       f,
		OwnerType:   p.rootTypeName(op),
		Method: strings.ToUpper(
			o.Method,
		),
		Path:    o.Path,
		Style:   StyleOverride,
		Public:  o.Public,
		Tag:     o.Tag,
		Created: o.Created,
	}
	for _, m := range pathParam.FindAllStringSubmatch(o.Path, -1) {
		name := m[1]
		arg := o.Params[name]
		if arg == "" {
			for _, cand := range []string{name, strings.TrimSuffix(name, "Id"), strings.TrimSuffix(name, "Id") + "ID", strings.TrimSuffix(name, "Id") + "Id"} {
				if argNamed(f, cand) != nil {
					arg = cand
					break
				}
			}
		}
		if arg == "" || p.s.argPath(f, arg) == nil {
			return nil, fmt.Errorf("%w: %s {%s}", ErrUnboundParameter, f.Name, name)
		}
		r.Params = append(r.Params, PathParam{Name: name, Arg: arg})
	}
	return r, nil
}

// splitResource finds the longest known resource or singleton that name
// starts with, returning it and the PascalCase remainder.
func (p *planner) splitResource(name string) (string, string, bool) {
	if _, ok := p.resources[name]; ok || p.singletons[name] {
		return name, "", true
	}
	best := ""
	for _, set := range []func(func(string)){p.eachResource, p.eachSingleton} {
		set(func(k string) {
			if len(k) > len(best) && hasWordPrefix(name, k) {
				best = k
			}
		})
	}
	if best == "" {
		return "", name, false
	}
	return best, name[len(best):], true
}

func (p *planner) eachResource(fn func(string)) {
	for k := range p.resources {
		fn(k)
	}
}

func (p *planner) eachSingleton(fn func(string)) {
	for k := range p.singletons {
		fn(k)
	}
}

// findParent picks the first non-null ID argument naming another resource:
// runCancel(stack: ID!, run: ID!) nests under /stacks/{stackId}.
func (p *planner) findParent(f Field, self, skip string) (string, string) {
	for _, a := range f.Args {
		if a.Name == skip || p.attrArgs[a.Name] || !a.Type.NonNull() ||
			a.Type.Named().Name != "ID" {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(a.Name, "ID"), "Id")
		if _, ok := p.resources[base]; ok && base != self {
			return base, a.Name
		}
	}
	return "", ""
}

// itemArg finds the argument identifying the resource itself: id, the
// resource name, or a word-suffix of it (terraformProviderVersion is
// located by its version argument, stackScheduledRun by scheduledRun).
func itemArg(f Field, r, local string) string {
	cands := []string{"id", r, local, r + "Id", r + "ID", local + "Id", local + "ID"}
	ws := words(r)
	for i := 1; i < len(ws); i++ {
		suffix := camel(ws[i:])
		cands = append(cands, suffix, suffix+"Id", suffix+"ID")
	}
	for _, cand := range cands {
		// A nullable id still locates the item: upstream reads its absence
		// as "the account's default integration", which REST addresses by
		// an explicit id instead.
		if a := argNamed(
			f,
			cand,
		); a != nil && a.Type.Named().Name == "ID" &&
			(a.Type.NonNull() || cand == "id") {
			return cand
		}
	}
	return ""
}

func localName(r, parent string) string {
	if parent != "" && hasWordPrefix(r, parent) {
		return lowerFirst(r[len(parent):])
	}
	return r
}

// paths builds the collection path of local under an optional parent, and
// the parameters that path carries.
func (p *planner) paths(parent, parentArg, local string) (string, []PathParam) {
	var prefix string
	var params []PathParam
	if parent != "" {
		prefix = "/" + kebab(plural(parent, p.m.Plurals)) + "/{" + parent + "Id}"
		params = append(params, PathParam{Name: parent + "Id", Arg: parentArg})
	}
	return prefix + "/" + kebab(plural(local, p.m.Plurals)), params
}

func (p *planner) query(f Field) *Route {
	if _, ok := p.resources[f.Name]; ok && argNamed(f, "id") != nil {
		r := f.Name
		parent, parentArg := p.findParent(f, r, "id")
		local := localName(r, parent)
		coll, params := p.paths(parent, parentArg, local)
		return &Route{
			Method:   http.MethodGet,
			Path:     coll + "/{" + local + "Id}",
			Params:   append(params, PathParam{Name: local + "Id", Arg: "id"}),
			Style:    StyleItem,
			Resource: r,
		}
	}
	if m := searchName.FindStringSubmatch(f.Name); m != nil {
		parent, parentArg := p.findParent(f, "", "")
		coll := lowerFirst(m[1])
		if parent != "" && hasWordPrefix(coll, parent) {
			coll = lowerFirst(coll[len(parent):])
		}
		var prefix string
		var params []PathParam
		if parent != "" {
			prefix = "/" + kebab(plural(parent, p.m.Plurals)) + "/{" + parent + "Id}"
			params = []PathParam{{Name: parent + "Id", Arg: parentArg}}
		}
		path := prefix + "/" + kebab(coll) + "/search"
		if m[2] != "" {
			path += "/suggestions"
		}
		return &Route{Method: http.MethodPost, Path: path, Params: params, Style: StyleSearch}
	}
	if f.Type.IsList() {
		for r := range p.resources {
			if plural(r, p.m.Plurals) == f.Name {
				coll, _ := p.paths("", "", r)
				return &Route{Method: http.MethodGet, Path: coll, Style: StyleList, Resource: r}
			}
		}
	}
	if r, extra, ok := p.splitResource(f.Name); ok {
		if p.singletons[r] {
			path := "/" + kebab(r)
			if extra != "" {
				path += "/" + kebab(extra)
			}
			return &Route{Method: http.MethodGet, Path: path, Style: StyleSingleton, Resource: r}
		}
		if item := itemArg(f, r, r); item != "" && extra != "" {
			coll, _ := p.paths("", "", r)
			return &Route{
				Method: http.MethodGet, Path: coll + "/{" + r + "Id}/" + kebab(extra),
				Params: []PathParam{{Name: r + "Id", Arg: item}}, Style: StyleSubRead, Resource: r,
			}
		}
	}
	return &Route{Method: http.MethodGet, Path: "/" + kebab(f.Name), Style: StyleRead}
}

var searchName = regexp.MustCompile(`^search([A-Z]\w*?)(Suggestions)?$`)

func (p *planner) mutation(f Field) *Route {
	rpc := &Route{Method: http.MethodPost, Path: "/" + kebab(f.Name), Style: StyleRPC}
	base, _ := stripVersion(f.Name)
	ws := words(base)
	if len(ws) < 2 {
		return rpc
	}
	verb := ws[len(ws)-1]
	class := verbClass(verb)
	r, extra, ok := p.splitResource(camel(ws[:len(ws)-1]))
	if !ok {
		return rpc
	}
	if extra != "" && class != StyleAction {
		class = StyleAction
	}
	if p.singletons[r] {
		path := "/" + kebab(r)
		switch class {
		case StyleCreate:
			return &Route{Method: http.MethodPost, Path: path, Style: StyleSingleton, Resource: r}
		case StyleUpdate:
			return &Route{Method: http.MethodPut, Path: path, Style: StyleSingleton, Resource: r}
		case StyleDelete:
			return &Route{Method: http.MethodDelete, Path: path, Style: StyleSingleton, Resource: r}
		}
		return &Route{
			Method:   http.MethodPost,
			Path:     path + "/" + kebab(extra+verb),
			Style:    StyleSingleton,
			Resource: r,
		}
	}

	item := itemArg(f, r, r)
	parent, parentArg := p.findParent(f, r, item)
	local := localName(r, parent)
	if class == StyleCreate && item == "id" && parent == "" {
		// blueprintDeploymentCreate(id: ID!) creates under the blueprint the
		// id names: the id locates the parent, not the new resource.
		if pr, _, ok := p.splitResource(r); ok && pr != r {
			parent, parentArg, item = pr, "id", ""
			local = localName(r, parent)
		}
	}
	coll, params := p.paths(parent, parentArg, local)
	itemPath := coll + "/{" + local + "Id}"
	itemParams := append(
		append([]PathParam(nil), params...),
		PathParam{Name: local + "Id", Arg: item},
	)
	route := func(method, path string, params []PathParam, style string) *Route {
		return &Route{Method: method, Path: path, Params: params, Style: style, Resource: r}
	}
	switch class {
	case StyleCreate:
		if item == "" {
			return route(http.MethodPost, coll, params, StyleCreate)
		}
	case StyleUpdate:
		if item == "" {
			if nested := p.s.nestedID(f); nested != "" {
				item = nested
				itemParams[len(itemParams)-1].Arg = nested
			}
		}
		if item != "" {
			return route(http.MethodPut, itemPath, itemParams, StyleUpdate)
		}
	case StyleDelete:
		if item != "" {
			return route(http.MethodDelete, itemPath, itemParams, StyleDelete)
		}
	case StyleAttach:
		if item != "" && parent != "" {
			return route(http.MethodPut, itemPath, itemParams, StyleAttach)
		}
	case StyleDetach:
		if item != "" {
			return route(
				http.MethodDelete,
				"/"+kebab(r)+"-attachments/{attachmentId}",
				[]PathParam{{Name: "attachmentId", Arg: item}},
				StyleDetach,
			)
		}
	default:
		action := "/" + kebab(extra+verb)
		if item != "" {
			return route(http.MethodPost, itemPath+action, itemParams, StyleAction)
		}
		if parent != "" {
			return route(http.MethodPost, coll+action, params, StyleAction)
		}
	}
	return rpc
}

// addFieldRoutes exposes fields of resource types that take arguments, or
// that list other resources, as sub-resource reads under the item route:
// Stack.runs(before:) → GET /stacks/{stackId}/runs?before=.
func (p *planner) addFieldRoutes() {
	used := map[string]bool{}
	for _, r := range p.plan.Routes {
		used[r.OperationID] = true
	}
	types := make([]string, 0, len(p.plan.ItemTypes))
	for t := range p.plan.ItemTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, tn := range types {
		item := p.plan.ItemTypes[tn]
		t := p.s.Type(tn)
		if t == nil || t.Kind != KindObject {
			continue
		}
		for _, fld := range t.Fields {
			if fld.IsDeprecated || !p.isFieldRoute(fld) {
				continue
			}
			id := item.Resource + upperFirst(fld.Name)
			for used[id] {
				id += "Field"
			}
			used[id] = true
			params := make([]PathParam, len(item.Params))
			copy(params, item.Params)
			// The item parameter selects the parent object; it is not an
			// argument of the field.
			params[len(params)-1].Arg = ""
			p.plan.Routes = append(p.plan.Routes, &Route{
				OperationID: id, Op: OpQuery, Field: fld, OwnerType: tn,
				Method: http.MethodGet, Path: item.Path + "/" + kebab(fld.Name), Params: params,
				Style: StyleField, Resource: item.Resource,
			})
		}
	}
}

// isFieldRoute reports whether a field of a resource type becomes its own
// route instead of a property.
func (p *planner) isFieldRoute(f Field) bool {
	if len(f.Args) > 0 {
		return true
	}
	_, isItem := p.plan.ItemTypes[f.Type.Named().Name]
	return f.Type.IsList() && isItem
}

// check rejects colliding routes and an exceeded RPC budget.
func (pl *Plan) check(budget int) error {
	seen := map[string]string{}
	var collisions []string
	rpc := 0
	for _, r := range pl.Routes {
		key := r.Method + " " + pathParam.ReplaceAllString(r.Path, "{}")
		if prev, ok := seen[key]; ok {
			collisions = append(
				collisions,
				fmt.Sprintf("%s %s: %s and %s", r.Method, r.Path, prev, r.OperationID),
			)
		}
		seen[key] = r.OperationID
		if r.Style == StyleRPC {
			rpc++
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return fmt.Errorf(
			"%w (add operations overrides):\n  %s",
			ErrRouteCollision,
			strings.Join(collisions, "\n  "),
		)
	}
	if rpc > budget {
		return fmt.Errorf("%w: %d > %d", ErrRPCBudget, rpc, budget)
	}
	return nil
}
