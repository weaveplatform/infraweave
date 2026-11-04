package specref

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Files in the Flows reference directory (api/reference/spacelift-flows)
// beyond the lock, mapping and coverage report every product has.
const (
	FileFlowsSpec     = "flows.openapi.yaml"
	FileFlowsProvider = "provider-rpcs.json"
	FileFlowsFlowctl  = "flowctl-rpcs.json"
	FileFlowsOverlay  = "schemas.yaml"
)

// The two Flows clients, as x-rpc names them.
const (
	clientProvider = "provider"
	clientFlowctl  = "flowctl"
)

// FlowsLock pins every upstream input of the Flows reference. The templates
// and the registry are validated against the overlay on each fetch; only the
// templates' digests are kept, since the templates state no licence.
type FlowsLock struct {
	Provider  LockRepo      `yaml:"provider"  json:"provider"`
	Flowctl   LockRepo      `yaml:"flowctl"   json:"flowctl"`
	SDK       LockPackage   `yaml:"sdk"       json:"sdk"`
	Templates LockTemplates `yaml:"templates" json:"templates"`
	Registry  LockRegistry  `yaml:"registry"  json:"registry"`
	FetchedAt string        `yaml:"fetchedAt" json:"fetchedAt,omitempty"`
}

// LockPackage pins an npm package.
type LockPackage struct {
	Package string `yaml:"package" json:"package"`
	Version string `yaml:"version" json:"version"`
	SHA256  string `yaml:"sha256"  json:"sha256"`
}

// LockTemplates pins the published flow templates: the index they were read
// from and the SHA-256 of each template's flow definition, by slug.
type LockTemplates struct {
	Index string            `yaml:"index" json:"index"`
	Flows map[string]string `yaml:"flows" json:"flows"`
}

// LockRegistry names the registry whose manifest fetch validates. Its
// content is not pinned: apps publish versions continually, and the manifest
// feeds nothing in this directory.
type LockRegistry struct {
	Manifest string `yaml:"manifest" json:"manifest"`
}

// version names the document by the client sources it was generated from.
func (l *FlowsLock) version() string {
	short := func(ref string) string {
		if len(ref) > 12 {
			return ref[:12]
		}
		return ref
	}
	return "provider-" + short(l.Provider.Ref) + ".flowctl-" + short(l.Flowctl.Ref)
}

// FlowsSources is the Flows reference directory, loaded.
type FlowsSources struct {
	Lock     *FlowsLock
	Provider *RPCSnapshot
	Flowctl  *RPCSnapshot
	Mapping  *FlowsMapping
	// Overlay is schemas.yaml: hand-written schemas for what the clients
	// pass as opaque strings or never send, such as a flow definition.
	Overlay openapi3.Schemas
}

// ReadFlowsSources loads the Flows reference directory.
func ReadFlowsSources(dir string) (*FlowsSources, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open reference dir: %w", err)
	}
	defer root.Close()
	read := func(name string) ([]byte, error) {
		b, err := root.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return b, nil
	}
	src := &FlowsSources{Lock: &FlowsLock{}, Provider: &RPCSnapshot{}, Flowctl: &RPCSnapshot{}}
	data, err := read(FileLock)
	if err != nil {
		return nil, err
	}
	if err := strictYAML(data, src.Lock); err != nil {
		return nil, fmt.Errorf("%s: %w", FileLock, err)
	}
	for name, dst := range map[string]*RPCSnapshot{FileFlowsProvider: src.Provider, FileFlowsFlowctl: src.Flowctl} {
		if data, err = read(name); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, dst); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
	}
	if data, err = read(FileMapping); err != nil {
		return nil, err
	}
	if src.Mapping, err = ParseFlowsMapping(data); err != nil {
		return nil, err
	}
	if data, err = read(FileFlowsOverlay); err != nil {
		return nil, err
	}
	if src.Overlay, err = ParseFlowsOverlay(data); err != nil {
		return nil, err
	}
	return src, nil
}

// ParseFlowsOverlay loads schemas.yaml, a map of schema name to OpenAPI 3.0
// schema, and validates it as the components of a document.
func ParseFlowsOverlay(data []byte) (openapi3.Schemas, error) {
	var schemas map[string]any
	if err := strictYAML(data, &schemas); err != nil {
		return nil, fmt.Errorf("%s: %w", FileFlowsOverlay, err)
	}
	wrapper, err := json.Marshal(map[string]any{
		"openapi":    "3.0.3",
		"info":       map[string]string{"title": FileFlowsOverlay, "version": "0"},
		"paths":      map[string]any{},
		"components": map[string]any{"schemas": schemas},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileFlowsOverlay, err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(wrapper)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", FileFlowsOverlay, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate %s: %w", FileFlowsOverlay, err)
	}
	return doc.Components.Schemas, nil
}

// flowsRPC is one RPC path with the client that calls it.
type flowsRPC struct {
	RPC
	client string
}

// flowsOperation is one REST operation and the RPC paths it answers.
type flowsOperation struct {
	id       string
	route    *FlowsRoute
	tag      string
	rpcs     []flowsRPC
	request  *Shape
	response *Shape
	override bool
}

// FlowsReport says how the RPC paths landed in the Flows reference; it is
// coverage.md.
type FlowsReport struct {
	Lock       *FlowsLock
	Clients    map[string]int
	Operations []*flowsOperation
	Excluded   map[string]string
	// Untyped lists, by operation, the response properties a client reads
	// without declaring a type.
	Untyped map[string][]string
	// Unenveloped lists the RPC paths whose response a client reads from the
	// top level of the body rather than from data.
	Unenveloped []string
}

// GenerateFlows derives the Flows reference document and coverage report.
func GenerateFlows(ctx context.Context, src *FlowsSources) (*Generated, error) {
	ops, rep, err := planFlows(src)
	if err != nil {
		return nil, err
	}
	doc, err := buildFlowsDoc(src, ops)
	if err != nil {
		return nil, err
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("encode spec: %w", err)
	}
	loaded, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		return nil, fmt.Errorf("load generated spec: %w", err)
	}
	if err := loaded.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate generated spec: %w", err)
	}
	spec, err := specYAML(raw, flowsSpecHeader)
	if err != nil {
		return nil, err
	}
	return &Generated{Spec: spec, Coverage: []byte(rep.Markdown()), Summary: rep.Summary()}, nil
}

const flowsSpecHeader = "# Generated by `specref generate --product flows` from api/reference/spacelift-flows; edit rest-mapping.yaml or schemas.yaml, not this file.\n"

// planFlows checks the mapping against the snapshots and groups the RPC
// paths into operations.
func planFlows(src *FlowsSources) ([]*flowsOperation, *FlowsReport, error) {
	m := src.Mapping
	rep := &FlowsReport{
		Lock:     src.Lock,
		Clients:  map[string]int{},
		Excluded: map[string]string{},
		Untyped:  map[string][]string{},
	}
	var all []flowsRPC
	for client, snap := range map[string]*RPCSnapshot{clientProvider: src.Provider, clientFlowctl: src.Flowctl} {
		for _, r := range snap.RPCs {
			all = append(all, flowsRPC{RPC: r, client: client})
			rep.Clients[client]++
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	var errs []error
	named := map[string]bool{}
	byID := map[string]*flowsOperation{}
	for _, r := range all {
		named[r.Path] = true
		if r.Unenveloped {
			rep.Unenveloped = append(rep.Unenveloped, r.Path)
		}
		if reason, ok := m.Exclude[r.Path]; ok {
			rep.Excluded[r.Path] = reason
			continue
		}
		e, ok := m.RPCs[r.Path]
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s (%s) is not mapped; add it to rpcs or exclude",
				ErrFlowsMapping, r.Path, r.client))
			continue
		}
		if err := addFlowsRPC(m, byID, e, r); err != nil {
			errs = append(errs, err)
		}
	}
	for p := range m.RPCs {
		if !named[p] {
			errs = append(errs, fmt.Errorf("%w: %s is mapped but no client calls it", ErrFlowsMapping, p))
		}
	}
	for p := range m.Exclude {
		if !named[p] {
			errs = append(errs, fmt.Errorf("%w: %s is excluded but no client calls it", ErrFlowsMapping, p))
		}
	}
	if len(errs) > 0 {
		sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
		return nil, nil, errors.Join(errs...)
	}
	ops := make([]*flowsOperation, 0, len(byID))
	for _, op := range byID {
		ops = append(ops, op)
		if u := untyped(op.response, ""); len(u) > 0 {
			rep.Untyped[op.id] = u
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].id < ops[j].id })
	rep.Operations = ops
	return ops, rep, nil
}

func addFlowsRPC(m *FlowsMapping, byID map[string]*flowsOperation, e FlowsRPCEntry, r flowsRPC) error {
	route, err := m.route(r.Path, r.Request)
	if err != nil {
		return err
	}
	tag := e.Tag
	if tag == "" {
		tag = m.Resources[route.Resource].Tag
	}
	op, ok := byID[e.OperationID]
	if !ok {
		byID[e.OperationID] = &flowsOperation{
			id: e.OperationID, route: route, tag: tag, rpcs: []flowsRPC{r},
			request: r.Request, response: r.Response, override: e.Path != "",
		}
		return nil
	}
	if op.route.Method != route.Method || op.route.Path != route.Path || op.tag != tag {
		return fmt.Errorf("%w: %s maps %s to %s %s (%s), but %s maps it to %s %s (%s)",
			ErrFlowsMapping, r.Path, e.OperationID, route.Method, route.Path, tag,
			op.rpcs[0].Path, op.route.Method, op.route.Path, op.tag)
	}
	if op.request, err = merged(op.request, r.Request); err != nil {
		return fmt.Errorf("%s request: %w", e.OperationID, err)
	}
	if op.response, err = merged(op.response, r.Response); err != nil {
		return fmt.Errorf("%s response: %w", e.OperationID, err)
	}
	for k, v := range route.Bind {
		if op.route.Bind[k] != v {
			op.route.Bind[k] = v
		}
	}
	op.rpcs = append(op.rpcs, r)
	return nil
}

// untyped lists the properties of a response the client reads without a
// type, by dotted path.
func untyped(s *Shape, prefix string) []string {
	if s == nil {
		return nil
	}
	var out []string
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := s.Properties[name]
		if p.Type == "" && p.Ref == "" {
			out = append(out, prefix+name)
			continue
		}
		out = append(out, untyped(p, prefix+name+".")...)
	}
	return out
}

func buildFlowsDoc(src *FlowsSources, ops []*flowsOperation) (*openapi3.T, error) {
	doc := &openapi3.T{
		OpenAPI: "3.0.3",
		Info: &openapi3.Info{
			Title:   "Spacelift Flows API reference (REST projection)",
			Version: src.Lock.version(),
			Description: "The RPC interface Spacelift Flows' public clients call, projected onto " +
				"resource-oriented REST routes. Generated by `specref generate --product flows` from " +
				"the pinned terraform-provider-flows and flowctl sources; see " +
				"docs/research/reference/02-graphql-to-rest-mapping.md. Every operation lists the " +
				"RPC paths it answers, with the client that calls each, in `x-rpc`.",
			Extensions: map[string]any{"x-sources": map[string]LockRepo{
				clientProvider: src.Lock.Provider,
				clientFlowctl:  src.Lock.Flowctl,
			}},
		},
		Paths: openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas:         openapi3.Schemas{},
			Responses:       openapi3.ResponseBodies{},
			SecuritySchemes: openapi3.SecuritySchemes{},
		},
		Security: openapi3.SecurityRequirements{{"bearerAuth": []string{}}},
	}
	addProblemComponents(doc, "The Flows RPC interface reports a failure in its envelope's error string")
	doc.Components.SecuritySchemes["bearerAuth"] = &openapi3.SecuritySchemeRef{
		Value: &openapi3.SecurityScheme{
			Type: "http", Scheme: "bearer",
			Description: "A Flows API key or OAuth access token, as the clients send it.",
		},
	}
	if err := addFlowsSchemas(doc, src); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, op := range ops {
		o, err := flowsOperationDoc(op, src.Mapping)
		if err != nil {
			return nil, err
		}
		used[op.tag] = true
		item := doc.Paths.Value(op.route.Path)
		if item == nil {
			item = &openapi3.PathItem{}
			doc.Paths.Set(op.route.Path, item)
		}
		if item.GetOperation(op.route.Method) != nil {
			return nil, fmt.Errorf("%w: two operations share %s %s",
				ErrFlowsMapping, op.route.Method, op.route.Path)
		}
		item.SetOperation(op.route.Method, o)
	}
	tags := make([]string, 0, len(used))
	for t := range used {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		doc.Tags = append(doc.Tags, &openapi3.Tag{Name: t, Description: src.Mapping.Tags[t]})
	}
	return doc, nil
}

// addFlowsSchemas adds the named shapes of both clients and the overlay's
// schemas as components; a name two of them define differently fails.
func addFlowsSchemas(doc *openapi3.T, src *FlowsSources) error {
	shapes := map[string]*Shape{}
	for _, snap := range []*RPCSnapshot{src.Provider, src.Flowctl} {
		for name, s := range snap.Types {
			if prev, ok := shapes[name]; ok {
				a, _ := json.Marshal(prev)
				b, _ := json.Marshal(s)
				if string(a) != string(b) {
					return fmt.Errorf("%w: both clients declare a different %s", ErrFlowsSource, name)
				}
			}
			shapes[name] = s
		}
	}
	for name, s := range shapes {
		doc.Components.Schemas[name] = s.schema()
	}
	for name, s := range src.Overlay {
		if _, ok := doc.Components.Schemas[name]; ok {
			return fmt.Errorf("%w: %s names both a client type and an overlay schema", ErrFlowsMapping, name)
		}
		doc.Components.Schemas[name] = s
	}
	for prop, name := range src.Mapping.YAML {
		if _, ok := src.Overlay[name]; !ok {
			return fmt.Errorf("%w: yaml gives %s the schema %s, which %s lacks",
				ErrFlowsMapping, prop, name, FileFlowsOverlay)
		}
	}
	return nil
}

func flowsOperationDoc(op *flowsOperation, m *FlowsMapping) (*openapi3.Operation, error) {
	rpcs := make([]map[string]string, 0, len(op.rpcs))
	for _, r := range op.rpcs {
		rpcs = append(rpcs, map[string]string{"path": r.Path, "client": r.client})
	}
	o := &openapi3.Operation{
		OperationID: op.id,
		Summary:     summaryWords(op.id),
		Tags:        []string{op.tag},
		Responses:   openapi3.NewResponses(),
		Extensions:  map[string]any{"x-rpc": rpcs},
	}
	bound := map[string]bool{}
	params := make([]string, 0, len(op.route.Bind))
	for p := range op.route.Bind {
		params = append(params, p)
	}
	sort.Strings(params)
	for _, p := range params {
		field := op.route.Bind[p]
		bound[field] = true
		param := openapi3.NewPathParameter(p).WithSchema(openapi3.NewStringSchema())
		if field != p {
			param.Extensions = map[string]any{"x-rpc-field": field}
		}
		o.Parameters = append(o.Parameters, &openapi3.ParameterRef{Value: param})
	}
	rest := annotateYAML(op.request.without(bound), m)
	switch op.route.Method {
	case http.MethodGet, http.MethodDelete:
		names := make([]string, 0, len(rest.Properties))
		for name := range rest.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			s := rest.Properties[name]
			if !s.isScalar() {
				return nil, fmt.Errorf("%w: %s %s: %s (%s) cannot be a query parameter",
					ErrFlowsMapping, op.route.Method, op.route.Path, name, s.describe())
			}
			q := openapi3.NewQueryParameter(name).WithSchema(s.schema().Value)
			q.Required = contains(rest.Required, name)
			o.Parameters = append(o.Parameters, &openapi3.ParameterRef{Value: q})
		}
	default:
		if len(rest.Properties) > 0 {
			o.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
				WithRequired(true).WithJSONSchemaRef(rest.schema())}
		}
	}
	flowsResponses(o, op, annotateYAML(op.response, m))
	return o, nil
}

// annotateYAML marks the string properties that carry YAML of an overlay
// schema, such as a flow's definition, on a copy: the shapes are shared with
// the snapshots.
func annotateYAML(s *Shape, m *FlowsMapping) *Shape {
	if s == nil || len(s.Properties) == 0 {
		return s
	}
	out := *s
	out.Properties = map[string]*Shape{}
	for name, p := range s.Properties {
		if schema, ok := m.YAML[name]; ok && p.Type == "string" {
			c := *p
			c.YAMLSchema = schema
			p = &c
		}
		out.Properties[name] = p
	}
	return &out
}

func flowsResponses(o *openapi3.Operation, op *flowsOperation, response *Shape) {
	code := op.route.Code
	switch {
	case response.isEmpty() && code != http.StatusCreated:
		o.Responses.Set(fmt.Sprint(http.StatusNoContent),
			&openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("Done; nothing to return.")})
	default:
		desc := "The result."
		if code == 0 {
			code = http.StatusOK
		}
		if code == http.StatusCreated {
			desc = "Created."
		}
		o.Responses.Set(fmt.Sprint(code), &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription(desc).
			WithContent(openapi3.Content{jsonMedia: openapi3.NewMediaType().WithSchemaRef(response.schema())})})
	}
	if len(op.route.Bind) > 0 {
		o.Responses.Set("404", &openapi3.ResponseRef{Ref: "#/components/responses/NotFound"})
	}
	o.Responses.Set("default", &openapi3.ResponseRef{Ref: "#/components/responses/" + problemSchema})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// summaryWords turns an operationId into a sentence: createFlow is
// "Create flow".
func summaryWords(id string) string {
	ws := words(id)
	ws[0] = upperFirst(ws[0])
	for i := 1; i < len(ws); i++ {
		ws[i] = strings.ToLower(ws[i])
	}
	return strings.Join(ws, " ")
}

// Summary is the line specref generate prints for the Flows reference.
func (r *FlowsReport) Summary() string {
	total := 0
	for _, n := range r.Clients {
		total += n
	}
	return fmt.Sprintf("%d RPC paths (%d provider, %d flowctl): %d operations, %d excluded",
		total, r.Clients[clientProvider], r.Clients[clientFlowctl], len(r.Operations), len(r.Excluded))
}

// Markdown renders coverage.md.
func (r *FlowsReport) Markdown() string {
	var b strings.Builder
	b.WriteString("# Spacelift Flows reference coverage\n\n")
	b.WriteString("Generated by `specref generate --product flows`; do not edit.\n\n")
	fmt.Fprintf(&b, "- terraform-provider-flows: `%s` at `%s`\n", r.Lock.Provider.Repository, r.Lock.Provider.Ref)
	fmt.Fprintf(&b, "- flowctl: `%s` at `%s`\n", r.Lock.Flowctl.Repository, r.Lock.Flowctl.Ref)
	fmt.Fprintf(&b, "- %s\n\n", r.Summary())
	b.WriteString("## Operations\n\n")
	b.WriteString("| Operation | Route | Tag | RPC paths |\n|---|---|---|---|\n")
	for _, op := range r.Operations {
		paths := make([]string, 0, len(op.rpcs))
		for _, rpc := range op.rpcs {
			paths = append(paths, fmt.Sprintf("`%s` (%s)", rpc.Path, rpc.client))
		}
		route := fmt.Sprintf("`%s %s`", op.route.Method, op.route.Path)
		if op.override {
			route += " (override)"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", op.id, route, op.tag, strings.Join(paths, "<br>"))
	}
	b.WriteString("\n## Excluded\n\n")
	if len(r.Excluded) == 0 {
		b.WriteString("None: every RPC path is an operation.\n")
	} else {
		b.WriteString("| RPC path | Reason |\n|---|---|\n")
		for _, p := range sortedKeys(r.Excluded) {
			fmt.Fprintf(&b, "| `%s` | %s |\n", p, r.Excluded[p])
		}
	}
	b.WriteString("\n## Responses outside the envelope\n\n")
	if len(r.Unenveloped) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("The client reads these responses from the top level of the body, not from `data`.\n\n")
		for _, p := range r.Unenveloped {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	b.WriteString("\n## Untyped response properties\n\n")
	if len(r.Untyped) == 0 {
		b.WriteString("None.\n")
		return b.String()
	}
	b.WriteString("The client reads these without declaring a type, so the reference types them as any value.\n\n")
	b.WriteString("| Operation | Properties |\n|---|---|\n")
	for _, id := range sortedKeys(r.Untyped) {
		fmt.Fprintf(&b, "| `%s` | %s |\n", id, codeList(r.Untyped[id]))
	}
	return b.String()
}
