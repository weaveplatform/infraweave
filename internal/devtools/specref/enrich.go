package specref

import (
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Folders that group requests across API areas rather than naming one.
const (
	folderAdvanced   = "Advanced"
	folderCookbook   = "Cookbook"
	folderDangerZone = "Danger Zone"
)

// pickBrunoRequests chooses, per root field, the request that documents it
// best: one in its own area folder over a cookbook recipe or the danger
// zone, and one calling only that field over a multi-field request.
func pickBrunoRequests(inv *BrunoInventory) map[string]*BrunoOperation {
	out := map[string]*BrunoOperation{}
	if inv == nil {
		return out
	}
	rank := func(op *BrunoOperation) int {
		r := 0
		if op.Folder == folderCookbook || op.Folder == folderDangerZone {
			r += 2
		}
		if len(op.RootFields) > 1 {
			r++
		}
		return r
	}
	for i := range inv.Requests {
		op := &inv.Requests[i]
		for _, f := range op.RootFields {
			if cur, ok := out[f]; !ok || rank(op) < rank(cur) {
				out[f] = op
			}
		}
	}
	return out
}

// brunoTag names the area a request belongs to: its folder, or for the
// Advanced catch-all its sub-folder. Cross-cutting folders name none.
func brunoTag(op *BrunoOperation) string {
	switch {
	case op == nil, op.Folder == folderCookbook, op.Folder == folderDangerZone:
		return ""
	case op.Folder == folderAdvanced && op.Subfolder != "":
		return op.Subfolder
	default:
		return op.Folder
	}
}

// tagFor resolves an operation's tag: the mapping override, the Bruno area,
// then the area most other operations on the same resource carry.
func (b *builder) tagFor(r *Route, bru *BrunoOperation) string {
	if r.Tag != "" {
		return r.Tag
	}
	if t := brunoTag(bru); t != "" {
		return t
	}
	if r.Resource == "" {
		return ""
	}
	counts := map[string]int{}
	for _, other := range b.plan.Routes {
		if other.Resource == r.Resource && other.Style != StyleField {
			if t := brunoTag(b.bruno[other.Field.Name]); t != "" {
				counts[t]++
			}
		}
	}
	best := ""
	for t, n := range counts {
		if n > counts[best] || n == counts[best] && t < best {
			best = t
		}
	}
	return best
}

// addTags declares every tag in use, described by its Bruno folder docs.
func (b *builder) addTags(used map[string]bool) {
	docs := map[string]string{}
	if b.src.Bruno != nil {
		for _, f := range b.src.Bruno.Folders {
			name := f.Path
			if sub, ok := strings.CutPrefix(f.Path, folderAdvanced+"/"); ok {
				name = sub
			}
			docs[name] = f.Docs
		}
	}
	names := make([]string, 0, len(used))
	for t := range used {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		b.doc.Tags = append(b.doc.Tags, &openapi3.Tag{Name: t, Description: docs[t]})
	}
}

// tfAttribute is a provider attribute mapped onto a GraphQL input field.
type tfAttribute struct {
	Resource    string
	Attribute   string
	Description string
}

// TFCoverage reports how one Terraform resource maps onto the reference.
type TFCoverage struct {
	Resource      string
	Operations    []string
	Inputs        []string          // components and paths that received attributes
	Mapped        map[string]string // attribute → component.field
	Unmapped      []string
	TerraformOnly []string // attributes with no API counterpart by design
	Blocks        []string // nested blocks, not mapped attribute-by-attribute
}

// tfIndex maps (component, field) to the provider attributes behind it.
// Path parameters use the component "path:<operationId>".
type tfIndex struct {
	byField  map[string]map[string][]tfAttribute
	coverage []TFCoverage
}

func (x *tfIndex) attributes(component, field string) []tfAttribute {
	if x == nil {
		return nil
	}
	return x.byField[component][field]
}

func (x *tfIndex) add(component, field string, a tfAttribute) {
	if x.byField[component] == nil {
		x.byField[component] = map[string][]tfAttribute{}
	}
	x.byField[component][field] = append(x.byField[component][field], a)
}

// namespace is one place an attribute can land: an input object's fields,
// a generated request object's arguments, or an operation's path.
type namespace struct {
	component string
	fields    map[string]bool
}

// indexTerraform matches each resource's attributes against the inputs of
// the mutations it calls, create before update. The provider's own field
// mapping is Go code, so this uses its naming convention (snake_case →
// camelCase, else without an _id suffix) plus the curated renames.
func indexTerraform(src *Sources, plan *Plan) *tfIndex {
	x := &tfIndex{byField: map[string]map[string][]tfAttribute{}}
	if src.Terraform == nil || src.TerraformOps == nil {
		return x
	}
	routes := map[string]*Route{}
	for _, r := range plan.Routes {
		if r.Style != StyleField {
			routes[r.Field.Name] = r
		}
	}
	for _, res := range sortedKeys(src.TerraformOps.Resources) {
		block, ok := src.Terraform.Resources[res]
		if !ok {
			continue
		}
		cov := TFCoverage{
			Resource:   res,
			Operations: src.TerraformOps.Resources[res],
			Mapped:     map[string]string{},
		}
		spaces := inputNamespaces(src.Schema, routes, cov.Operations)
		used := map[string]bool{}
		for _, attr := range sortedKeys(block.Attributes) {
			a := block.Attributes[attr]
			if attr == "id" || a.Computed && !a.Optional && !a.Required {
				continue
			}
			if terraformOnly(attr) {
				cov.TerraformOnly = append(cov.TerraformOnly, attr)
				continue
			}
			ns, field := matchField(attr, src.Mapping.Terraform[res], spaces)
			if field == "" {
				cov.Unmapped = append(cov.Unmapped, attr)
				continue
			}
			used[ns] = true
			cov.Mapped[attr] = ns + "." + field
			x.add(
				ns,
				field,
				tfAttribute{Resource: res, Attribute: attr, Description: a.Description},
			)
		}
		cov.Inputs = sortedKeys(used)
		cov.Blocks = sortedKeys(block.BlockTypes)
		x.coverage = append(x.coverage, cov)
	}
	return x
}

// terraformOnly reports attributes that steer Terraform rather than carry
// API data: write-only versions and replacement triggers.
func terraformOnly(attr string) bool {
	return strings.HasSuffix(attr, "_wo_version") || attr == "keepers"
}

// inputNamespaces lists where a resource's attributes can land, in the
// order they are tried: create-like mutations first, then updates, then the
// rest; within one mutation, nested input objects, then arguments, then
// the path.
func inputNamespaces(s *Schema, routes map[string]*Route, ops []string) []namespace {
	var ordered []*Route
	for _, pass := range []func(string) bool{
		func(c string) bool { return c == StyleCreate || c == StyleAttach },
		func(c string) bool { return c == StyleUpdate },
		func(c string) bool { return c != StyleCreate && c != StyleAttach && c != StyleUpdate },
	} {
		for _, op := range ops {
			r, ok := routes[op]
			if !ok || r.Op != OpMutation {
				continue
			}
			base, _ := stripVersion(op)
			ws := words(base)
			if pass(verbClass(ws[len(ws)-1])) {
				ordered = append(ordered, r)
			}
		}
	}
	var out []namespace
	for _, r := range ordered {
		var unbound []InputValue
		path := namespace{component: "path:" + r.OperationID, fields: map[string]bool{}}
		for _, a := range r.Field.Args {
			if r.Bound(a.Name) {
				path.fields[a.Name] = true
			} else {
				unbound = append(unbound, a)
			}
		}
		args := namespace{
			component: upperFirst(r.OperationID) + "Request",
			fields:    map[string]bool{},
		}
		inputs := len(out)
		for _, a := range unbound {
			if t := s.Type(
				a.Type.Named().Name,
			); t != nil && t.Kind == KindInputObject &&
				!a.Type.IsList() {
				fields := map[string]bool{}
				for _, f := range t.InputFields {
					fields[f.Name] = true
				}
				out = append(out, namespace{component: t.Name, fields: fields})
			}
			args.fields[a.Name] = true
		}
		// A lone input-object argument is the body itself; there is no
		// request object for its name to live in.
		if len(unbound) > 1 || len(unbound) == 1 && len(out) == inputs {
			out = append(out, args)
		}
		out = append(out, path)
	}
	return out
}

func matchField(attr string, renames map[string]string, spaces []namespace) (string, string) {
	cands := []string{snakeToCamel(attr)}
	trimmed := strings.TrimSuffix(attr, "_wo")
	if f, ok := renames[attr]; ok {
		cands = []string{f}
	} else {
		if trimmed != attr {
			cands = append(cands, snakeToCamel(trimmed))
		}
		if base, ok := strings.CutSuffix(trimmed, "_id"); ok {
			cands = append(cands, snakeToCamel(base), snakeToCamel(base)+"ID")
		}
		if base, ok := strings.CutSuffix(trimmed, "_ids"); ok {
			cands = append(cands, snakeToCamel(base)+"s")
		}
	}
	for _, ns := range spaces {
		for _, c := range cands {
			if ns.fields[c] {
				return ns.component, c
			}
		}
	}
	// A rename to a field no input has any more falls through to unmapped,
	// so the coverage report shows the stale entry.
	return "", ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
