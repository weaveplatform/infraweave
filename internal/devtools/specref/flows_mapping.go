package specref

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ErrFlowsMapping reports a rest-mapping.yaml that does not account for the
// RPC paths exactly: an unmapped or stale path, an unknown resource or noun,
// or a route the rules cannot derive.
var ErrFlowsMapping = errors.New("flows rest mapping")

// FlowsMapping is api/reference/spacelift-flows/rest-mapping.yaml.
type FlowsMapping struct {
	Tags      map[string]string        `yaml:"tags"`
	Resources map[string]FlowsResource `yaml:"resources"`
	Nouns     map[string]string        `yaml:"nouns"`
	RPCs      map[string]FlowsRPCEntry `yaml:"rpcs"`
	Exclude   map[string]string        `yaml:"exclude"`
	// YAML names the overlay schema whose YAML a string property carries,
	// by property name.
	YAML map[string]string `yaml:"yaml"`
}

// FlowsResource is one REST resource of the Flows reference.
type FlowsResource struct {
	Collection string   `yaml:"collection"`
	ID         string   `yaml:"id"`
	Keys       []string `yaml:"keys"`
	// Parent keeps the resource's items under the parent's item, for a
	// resource named by a key unique only within its parent.
	Parent string `yaml:"parent"`
	Tag    string `yaml:"tag"`
}

// FlowsRPCEntry maps one RPC path. Method and Path together are an
// override; Resource then names the resource the route serves, for its tag
// and its item key.
type FlowsRPCEntry struct {
	OperationID string `yaml:"operationId"`
	Tag         string `yaml:"tag"`
	Method      string `yaml:"method"`
	Path        string `yaml:"path"`
	Resource    string `yaml:"resource"`
}

// ParseFlowsMapping decodes the mapping strictly and checks that every
// resource, noun and tag it names exists.
func ParseFlowsMapping(data []byte) (*FlowsMapping, error) {
	m := &FlowsMapping{}
	if err := strictYAML(data, m); err != nil {
		return nil, fmt.Errorf("%s: %w", FileMapping, err)
	}
	var errs []error
	for _, tag := range sortedKeys(m.Tags) {
		if !flowsTag(tag) {
			errs = append(
				errs,
				fmt.Errorf(
					"%w: tag %q does not start with %q",
					ErrFlowsMapping,
					tag,
					FlowsTagPrefix,
				),
			)
		}
	}
	for name, r := range m.Resources {
		if r.Collection == "" || r.ID == "" {
			errs = append(
				errs,
				fmt.Errorf("%w: resource %s needs collection and id", ErrFlowsMapping, name),
			)
		}
		if _, ok := m.Tags[r.Tag]; !ok {
			errs = append(
				errs,
				fmt.Errorf("%w: resource %s has tag %q, which tags does not describe",
					ErrFlowsMapping, name, r.Tag),
			)
		}
		if _, ok := m.Resources[r.Parent]; r.Parent != "" && !ok {
			errs = append(
				errs,
				fmt.Errorf(
					"%w: resource %s has unknown parent %s",
					ErrFlowsMapping,
					name,
					r.Parent,
				),
			)
		}
	}
	for noun, r := range m.Nouns {
		if _, ok := m.Resources[r]; !ok {
			errs = append(
				errs,
				fmt.Errorf("%w: noun %s names unknown resource %s", ErrFlowsMapping, noun, r),
			)
		}
	}
	for p, e := range m.RPCs {
		switch {
		case e.OperationID == "":
			errs = append(errs, fmt.Errorf("%w: %s has no operationId", ErrFlowsMapping, p))
		case (e.Method == "") != (e.Path == ""):
			errs = append(
				errs,
				fmt.Errorf("%w: %s needs both method and path to override", ErrFlowsMapping, p),
			)
		case e.Path != "" && e.Resource == "":
			errs = append(
				errs,
				fmt.Errorf("%w: override %s needs its resource", ErrFlowsMapping, p),
			)
		}
		if _, ok := m.Resources[e.Resource]; e.Resource != "" && !ok {
			errs = append(
				errs,
				fmt.Errorf("%w: %s names unknown resource %s", ErrFlowsMapping, p, e.Resource),
			)
		}
		if _, ok := m.Tags[e.Tag]; e.Tag != "" && !ok {
			errs = append(
				errs,
				fmt.Errorf(
					"%w: %s has tag %q, which tags does not describe",
					ErrFlowsMapping,
					p,
					e.Tag,
				),
			)
		}
		if _, ok := m.Exclude[p]; ok {
			errs = append(
				errs,
				fmt.Errorf("%w: %s is both mapped and excluded", ErrFlowsMapping, p),
			)
		}
	}
	for p, reason := range m.Exclude {
		if strings.TrimSpace(reason) == "" {
			errs = append(errs, fmt.Errorf("%w: excluded %s needs a reason", ErrFlowsMapping, p))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return m, nil
}

// FlowsRoute is the REST operation an RPC path projects onto.
type FlowsRoute struct {
	Method, Path string
	Resource     string
	// Bind maps each path parameter to the request property it carries.
	Bind map[string]string
	// Code is 201 for a create or start; otherwise zero, and the response
	// decides: 204 when the RPC answers nothing, 200 when it does.
	Code int
}

// verbs are the leading words of an RPC path's last segment, longest first
// so attach_to_ wins over a hypothetical attach.
var flowsVerbs = []string{
	"attach_to", "detach_from", "create", "get", "read", "update", "delete", "list",
	"start", "cancel", "confirm",
}

// route projects one RPC with its request shape.
func (m *FlowsMapping) route(rpcPath string, req *Shape) (*FlowsRoute, error) {
	e := m.RPCs[rpcPath]
	if e.Path != "" {
		r := &FlowsRoute{Method: strings.ToUpper(e.Method), Path: e.Path, Resource: e.Resource}
		return r, m.bind(r, req, e.Resource)
	}
	parts := strings.Split(strings.TrimPrefix(rpcPath, "/"), "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf(
			"%w: %s has no verb; give it method and path",
			ErrFlowsMapping,
			rpcPath,
		)
	}
	family, segment := parts[len(parts)-2], parts[len(parts)-1]
	verb, noun := splitVerb(segment)
	if verb == "" {
		return nil, fmt.Errorf("%w: %s starts with no known verb; give it method and path",
			ErrFlowsMapping, rpcPath)
	}
	batch := false
	if verb == "start" {
		noun, batch = strings.CutSuffix(noun, "_batch")
	}
	r := &FlowsRoute{}
	var err error
	switch verb {
	case "attach_to", "detach_from":
		r, err = m.attachRoute(verb, family, noun)
	default:
		if noun == "" {
			noun = family
		}
		r.Resource, err = m.resource(noun)
		if err == nil {
			err = m.verbRoute(r, verb, req, batch)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rpcPath, err)
	}
	r.Path = "/flows" + r.Path
	return r, m.bind(r, req, r.Resource)
}

func splitVerb(segment string) (verb, noun string) {
	for _, v := range flowsVerbs {
		if segment == v {
			return v, ""
		}
		if rest, ok := strings.CutPrefix(segment, v+"_"); ok {
			return v, rest
		}
	}
	return "", ""
}

func (m *FlowsMapping) resource(noun string) (string, error) {
	if r, ok := m.Nouns[noun]; ok {
		return r, nil
	}
	if r, ok := m.Nouns[strings.TrimSuffix(noun, "s")]; ok {
		return r, nil
	}
	return "", fmt.Errorf("%w: no resource for the noun %s; add it to nouns", ErrFlowsMapping, noun)
}

func (m *FlowsMapping) verbRoute(r *FlowsRoute, verb string, req *Shape, batch bool) error {
	switch verb {
	case "create", "start", "list":
		parent, err := m.parentFromKeys(r.Resource, req)
		if err != nil {
			return err
		}
		r.Path = m.collectionPath(r.Resource, parent)
		r.Method, r.Code = http.MethodPost, http.StatusCreated
		if verb == "list" {
			r.Method, r.Code = http.MethodGet, http.StatusOK
		}
		if batch {
			r.Path += "/batch"
			r.Code = http.StatusOK
		}
	case "get", "read", "update", "delete":
		r.Path = m.itemPath(r.Resource)
		r.Method = map[string]string{
			"get": http.MethodGet, "read": http.MethodGet,
			"update": http.MethodPatch, "delete": http.MethodDelete,
		}[verb]
	case "cancel", "confirm":
		r.Path = m.itemPath(r.Resource) + "/" + verb
		r.Method = http.MethodPost
	}
	return nil
}

func (m *FlowsMapping) attachRoute(verb, family, parentNoun string) (*FlowsRoute, error) {
	child, err := m.resource(family)
	if err != nil {
		return nil, err
	}
	parent, err := m.resource(parentNoun)
	if err != nil {
		return nil, err
	}
	method := http.MethodPut
	if verb == "detach_from" {
		method = http.MethodDelete
	}
	c := m.Resources[child]
	return &FlowsRoute{
		Method:   method,
		Resource: child,
		Path:     m.collectionPath(child, parent) + "/{" + c.ID + "}",
	}, nil
}

// parentFromKeys is the one other resource the request names by key, which
// a collection nests under; none means a top-level collection.
func (m *FlowsMapping) parentFromKeys(resource string, req *Shape) (string, error) {
	var found []string
	for name, r := range m.Resources {
		if name == resource {
			continue
		}
		for _, k := range append([]string{r.ID}, r.Keys...) {
			if _, ok := req.Properties[k]; ok {
				found = append(found, name)
				break
			}
		}
	}
	sort.Strings(found)
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf(
		"%w: the request names %s, so the parent is ambiguous; give it method and path",
		ErrFlowsMapping,
		strings.Join(found, " and "),
	)
}

// collectionPath nests a collection under its parent's item, dropping the
// parent's name from the front of the collection's (test-scenario-runs under
// test-scenarios is runs).
func (m *FlowsMapping) collectionPath(resource, parent string) string {
	c := m.Resources[resource]
	if parent == "" {
		return "/" + c.Collection
	}
	p := m.Resources[parent]
	seg := c.Collection
	if rest, ok := strings.CutPrefix(seg, strings.TrimSuffix(p.Collection, "s")+"-"); ok {
		seg = rest
	}
	return m.itemPath(parent) + "/" + seg
}

func (m *FlowsMapping) itemPath(resource string) string {
	r := m.Resources[resource]
	if r.Parent != "" {
		return m.collectionPath(resource, r.Parent) + "/{" + r.ID + "}"
	}
	return "/" + r.Collection + "/{" + r.ID + "}"
}

// bind finds, for each path parameter, the request property carrying it:
// the parameter's own name, a key of the resource it identifies, or id for
// the route's own resource.
func (m *FlowsMapping) bind(r *FlowsRoute, req *Shape, resource string) error {
	r.Bind = map[string]string{}
	for _, match := range pathParam.FindAllStringSubmatch(r.Path, -1) {
		param := match[1]
		candidates := []string{param}
		for _, res := range m.Resources {
			if res.ID == param {
				candidates = append(candidates, res.Keys...)
			}
		}
		if m.Resources[resource].ID == param {
			candidates = append(candidates, "id")
		}
		bound := ""
		for _, c := range candidates {
			if _, ok := req.Properties[c]; ok {
				bound = c
				break
			}
		}
		if bound == "" {
			return fmt.Errorf("%w: %s %s: no request property carries {%s}",
				ErrFlowsMapping, r.Method, r.Path, param)
		}
		r.Bind[param] = bound
	}
	return nil
}
