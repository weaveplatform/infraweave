package specref

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Convention names recorded in x-infraweave-divergence, one per transform
// the API conventions make to a scaffolded operation. Parity and reviewers
// use them to tell a convention from a curation.
//
// ref: decision 0003
const (
	ConventionPagination  = "cursor-pagination"
	ConventionIdempotency = "idempotency-key"
	ConventionRateLimit   = "rate-limit"
)

// Convention names of the update routes' semantics: if-match on each read
// that carries an ETag and each write that takes If-Match, merge-patch on
// an update PUT and the PATCH beside it.
//
// ref: decision 0022
const (
	ConventionIfMatch    = "if-match"
	ConventionMergePatch = "merge-patch"
)

// Cost classes for x-infraweave-cost: each caller has one token bucket per
// class, and expensive's budget is the smaller.
//
// ref: decision 0003
const (
	CostStandard  = "standard"
	CostExpensive = "expensive"
)

const componentsPrefix = "#/components/"

var (
	ErrUnknownTag       = errors.New("specref: no reference operation carries the tag")
	ErrMissingComponent = errors.New(
		"specref: scaffolded fragment references a component nobody defines",
	)
	ErrParamClash     = errors.New("specref: operation already has a parameter a convention adds")
	ErrFragmentExists = errors.New(
		"specref: fragment exists; scaffold with force to overwrite it",
	)
	ErrPatchClash = errors.New("specref: an update route already has a PATCH")
)

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Defined is what a service's other fragments already hold. Scaffolding
// references these instead of copying them, so the bundler's duplicate rules
// hold and shared shapes keep one definition.
type Defined struct {
	Paths map[string]bool
	// Components holds "kind/name", for example "schemas/Stack".
	Components map[string]bool
}

// ScaffoldOptions selects one reference tag to copy into a fragment.
type ScaffoldOptions struct {
	// Reference is the reference spec (spacelift.openapi.yaml).
	Reference []byte
	// Tag is the reference tag whose operations are copied.
	Tag string
	// Existing is what the service's other fragments define.
	Existing Defined
	// Actions are the action catalogue's rows, from which each operation's
	// action and subject are written where they decide them.
	Actions map[string]ActionRow
}

// Scaffolded is a fragment and what the author must know about it.
type Scaffolded struct {
	YAML       []byte
	Operations int
	Components int
	// Shared lists tagged paths another fragment already defines; they are
	// left out.
	Shared []string
	// Foreign lists "METHOD /path (Tag)" for operations under another tag
	// that come along because they share a path with a tagged operation. A
	// path lives in exactly one fragment.
	Foreign []string
	// TagDescription is the reference's description of the tag, for the
	// service's info.yaml.
	TagDescription string
}

// ymap is a decoded YAML mapping.
type ymap = map[string]any

// Scaffold copies every path holding an operation tagged opts.Tag, plus the
// transitive closure of the components those paths reference, into a
// fragment (paths and components only). Each operation gets the API
// conventions: cursor pagination on collection GETs, an optional
// Idempotency-Key on POST and PATCH, rate-limit responses and headers, and a
// cost class, and its authorization action and subject where the catalogue
// decides them. Each update PUT gets a PATCH beside it, and its item's
// reads and writes their ETag and If-Match. Every input schema is strict,
// refusing members it does not name. Provenance extensions (x-graphql,
// x-terraform, x-bruno) are kept.
//
// ref: decisions 0003, 0007 and 0022
func Scaffold(o ScaffoldOptions) (*Scaffolded, error) {
	var ref ymap
	if err := yaml.Unmarshal(o.Reference, &ref); err != nil {
		return nil, fmt.Errorf("specref: parse reference: %w", err)
	}
	paths, _ := ref["paths"].(ymap)
	refComps, _ := ref["components"].(ymap)
	out := &Scaffolded{TagDescription: tagDescription(ref, o.Tag)}
	picked := ymap{}
	added := ymap{} // components the conventions create, by "kind/name"
	for _, p := range sortedKeys(paths) {
		item, _ := paths[p].(ymap)
		if !hasTag(item, o.Tag) {
			continue
		}
		if o.Existing.Paths[p] {
			out.Shared = append(out.Shared, p)
			continue
		}
		for _, m := range methods {
			op, ok := item[m].(ymap)
			if !ok {
				continue
			}
			if ts := tags(op); !slices.Contains(ts, o.Tag) {
				owner := strings.Join(ts, ", ")
				if owner == "" {
					owner = "untagged"
				}
				out.Foreign = append(
					out.Foreign,
					fmt.Sprintf("%s %s (%s)", strings.ToUpper(m), p, owner),
				)
			}
			if err := applyConventions(m, p, op, refComps, added); err != nil {
				return nil, err
			}
			authorize(m, p, op, o.Actions)
			out.Operations++
		}
		twinned, err := twinUpdate(p, item)
		if err != nil {
			return nil, err
		}
		if twinned {
			out.Operations++
		}
		picked[p] = item
	}
	if len(picked) == 0 && len(out.Shared) == 0 {
		return nil, fmt.Errorf("%w %q", ErrUnknownTag, o.Tag)
	}
	comps, err := closure(picked, refComps, added, o.Existing.Components)
	if err != nil {
		return nil, err
	}
	strictInputs(picked, comps)
	frag := ymap{"paths": picked}
	if len(comps) > 0 {
		frag["components"] = comps
	}
	for _, kind := range comps {
		out.Components += len(kind.(ymap))
	}
	version := ""
	if info, ok := ref["info"].(ymap); ok {
		version, _ = info["version"].(string)
	}
	var buf bytes.Buffer
	fmt.Fprintf(
		&buf,
		"# Scaffolded by `specref scaffold --tag %s` from the Spacelift reference (%s).\n"+
			"# This fragment is infraweave's own: curate it freely. Scaffolding again with --force overwrites it.\n",
		o.Tag,
		version,
	)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(frag); err != nil {
		return nil, fmt.Errorf("specref: encode fragment: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("specref: encode fragment: %w", err)
	}
	out.YAML = buf.Bytes()
	return out, nil
}

// applyConventions rewrites one operation to the API conventions and
// records each change in x-infraweave-divergence.
//
// ref: decision 0003
func applyConventions(method, path string, op, refComps, added ymap) error {
	responses, _ := op["responses"].(ymap)
	if responses == nil {
		responses = ymap{}
		op["responses"] = responses
	}
	var applied []string
	if method == "get" {
		page, err := paginate(op, responses, refComps, added)
		if err != nil {
			return fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
		}
		if page {
			applied = append(applied, ConventionPagination)
		}
	}
	// A search changes nothing, so a retry is already safe without a key.
	if (method == "post" || method == "patch") && !isSearch(path, op) {
		if err := addParams(op, "IdempotencyKey"); err != nil {
			return fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
		}
		setDefault(responses, "409", responseRef("IdempotencyConflict"))
		setDefault(responses, "422", responseRef("IdempotencyMismatch"))
		applied = append(applied, ConventionIdempotency)
	}
	for code, r := range responses {
		if resp, ok := r.(ymap); ok && strings.HasPrefix(code, "2") && resp["$ref"] == nil {
			headers, _ := resp["headers"].(ymap)
			if headers == nil {
				headers = ymap{}
				resp["headers"] = headers
			}
			headers["X-RateLimit-Limit"] = ymap{"$ref": componentsPrefix + "headers/RateLimitLimit"}
			headers["X-RateLimit-Remaining"] = ymap{
				"$ref": componentsPrefix + "headers/RateLimitRemaining",
			}
			headers["X-RateLimit-Reset"] = ymap{"$ref": componentsPrefix + "headers/RateLimitReset"}
		}
	}
	setDefault(responses, "429", responseRef("TooManyRequests"))
	setDefault(responses, "503", responseRef("ApiDisabled"))
	applied = append(applied, ConventionRateLimit)
	op["x-infraweave-cost"] = costClass(path, op)
	op["x-infraweave-divergence"] = applied
	return nil
}

// paginate turns a GET whose 200 response is a JSON array into a cursor-paged
// one: the array becomes the items of a <Type>Page envelope, and the
// operation gains the cursor and limit parameters. It reports whether the
// operation is a collection.
func paginate(op, responses, refComps, added ymap) (bool, error) {
	ok200, _ := responses["200"].(ymap)
	content, _ := ok200["content"].(ymap)
	media, _ := content["application/json"].(ymap)
	schema, _ := media["schema"].(ymap)
	if schema["type"] != "array" {
		return false, nil
	}
	items := schema["items"]
	name := pageName(op, items)
	key := "schemas/" + name
	if _, clash := lookupComponent(refComps, "schemas", name); clash {
		return false, fmt.Errorf(
			"%w %s: the reference already has a schema of that name",
			ErrComponentClash,
			name,
		)
	}
	if _, ok := added[key]; !ok {
		added[key] = ymap{
			"type":        "object",
			"description": "One page of a cursor-paginated list.",
			"required":    []any{"items"},
			"properties": ymap{
				"items": ymap{"type": "array", "items": items},
				"nextCursor": ymap{
					"type":        "string",
					"description": "Pass as cursor to fetch the next page. Absent on the last page.",
				},
			},
		}
	}
	media["schema"] = ymap{"$ref": componentsPrefix + key}
	if err := addParams(op, "Cursor", "Limit"); err != nil {
		return false, err
	}
	setDefault(responses, "400", responseRef("InvalidCursor"))
	return true, nil
}

// pageName is <Item>Page for a list of a named schema, else the operation's
// PascalCase operationId with Page, so a list of plain values still gets a
// unique envelope.
func pageName(op ymap, items any) string {
	if it, ok := items.(ymap); ok {
		if r, ok := it["$ref"].(string); ok {
			return strings.TrimPrefix(r, componentsPrefix+"schemas/") + "Page"
		}
	}
	id, _ := op["operationId"].(string)
	return upperFirst(id) + "Page"
}

// paramNames are the wire names of the convention parameters, which must
// not collide with an argument the reference already passes.
var paramNames = map[string]string{
	"IdempotencyKey": "Idempotency-Key",
	"IfMatch":        "If-Match",
	"Cursor":         "cursor",
	"Limit":          "limit",
}

func addParams(op ymap, names ...string) error {
	params, _ := op["parameters"].([]any)
	for _, n := range names {
		for _, p := range params {
			if pm, ok := p.(ymap); ok && strings.EqualFold(fmt.Sprint(pm["name"]), paramNames[n]) {
				return fmt.Errorf("%w: %s", ErrParamClash, paramNames[n])
			}
		}
		params = append(params, ymap{"$ref": componentsPrefix + "parameters/" + n})
	}
	op["parameters"] = params
	return nil
}

// costClass is expensive for searches and batches, which fan out across
// many resources; curators mark other fan-out work, such as run triggers.
func costClass(path string, op ymap) string {
	if isSearch(path, op) || strings.HasSuffix(path, "/batch") {
		return CostExpensive
	}
	return CostStandard
}

func isSearch(path string, op ymap) bool {
	return op["x-rest-style"] == "search" || strings.HasSuffix(path, "/search")
}

func responseRef(name string) ymap { return ymap{"$ref": componentsPrefix + "responses/" + name} }

func setDefault(m ymap, k string, v any) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

// closure collects every component reachable from the picked paths, through
// $ref values and discriminator mappings alike (any string naming
// #/components/...). Components another fragment defines are referenced but
// not copied, and neither is what only they reach.
func closure(picked, refComps, added ymap, existing map[string]bool) (ymap, error) {
	out := ymap{}
	seen := map[string]bool{}
	queue := refs(picked)
	var missing []string
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if seen[key] || existing[key] {
			continue
		}
		seen[key] = true
		kind, name, _ := strings.Cut(key, "/")
		def, ok := added[key]
		if !ok {
			def, ok = lookupComponent(refComps, kind, name)
		}
		if !ok {
			missing = append(missing, key)
			continue
		}
		if out[kind] == nil {
			out[kind] = ymap{}
		}
		out[kind].(ymap)[name] = def
		queue = append(queue, refs(def)...)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("%w: %s", ErrMissingComponent, strings.Join(missing, ", "))
	}
	return out, nil
}

func lookupComponent(comps ymap, kind, name string) (any, bool) {
	items, _ := comps[kind].(ymap)
	def, ok := items[name]
	return def, ok
}

// refs returns every "kind/name" a value names, in a stable order.
func refs(v any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case ymap:
			for _, k := range sortedKeys(t) {
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case string:
			if r, ok := strings.CutPrefix(t, componentsPrefix); ok {
				out = append(out, r)
			}
		}
	}
	walk(v)
	return out
}

func hasTag(item ymap, tag string) bool {
	for _, m := range methods {
		if op, ok := item[m].(ymap); ok && slices.Contains(tags(op), tag) {
			return true
		}
	}
	return false
}

func tags(op ymap) []string {
	raw, _ := op["tags"].([]any)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		out = append(out, fmt.Sprint(t))
	}
	return out
}

func tagDescription(ref ymap, tag string) string {
	list, _ := ref["tags"].([]any)
	for _, t := range list {
		if m, ok := t.(ymap); ok && m["name"] == tag {
			d, _ := m["description"].(string)
			return d
		}
	}
	return ""
}

// ScaffoldFile scaffolds tag from the reference document at spec, any
// product's, into the fragment out, referencing what the other fragments in
// out's directory define, with each operation's action and subject from
// the action catalogue at catalogue. It refuses to replace an existing
// fragment unless force is set, because a scaffolded fragment is curated by
// hand afterwards.
func ScaffoldFile(spec, catalogue, tag, out string, force bool) (*Scaffolded, error) {
	if _, err := os.Stat(out); err == nil && !force {
		return nil, fmt.Errorf("%w: %s", ErrFragmentExists, out)
	}
	ref, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("specref: %w", err)
	}
	data, err := os.ReadFile(catalogue)
	if err != nil {
		return nil, fmt.Errorf("specref: %w", err)
	}
	rows, err := ParseActionCatalogue(data)
	if err != nil {
		return nil, err
	}
	existing, err := ReadDefined(filepath.Dir(out), filepath.Base(out))
	if err != nil {
		return nil, err
	}
	s, err := Scaffold(ScaffoldOptions{Reference: ref, Tag: tag, Existing: existing, Actions: rows})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(out, s.YAML, filePerm); err != nil {
		return nil, fmt.Errorf("specref: %w", err)
	}
	return s, nil
}

// ReadDefined collects the paths and components defined by every *.yaml in
// a service's fragment directory, except info.yaml and the file named skip
// (the fragment being scaffolded, when it is overwritten).
func ReadDefined(dir, skip string) (Defined, error) {
	d := Defined{Paths: map[string]bool{}, Components: map[string]bool{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return d, fmt.Errorf("specref: %w", err)
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".yaml") || n == "info.yaml" || n == skip {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return d, fmt.Errorf("specref: %w", err)
		}
		var frag struct {
			Paths      map[string]any            `yaml:"paths"`
			Components map[string]map[string]any `yaml:"components"`
		}
		if err := yaml.Unmarshal(data, &frag); err != nil {
			return d, fmt.Errorf("specref: parse %s: %w", n, err)
		}
		for p := range frag.Paths {
			d.Paths[p] = true
		}
		for kind, items := range frag.Components {
			for name := range items {
				d.Components[kind+"/"+name] = true
			}
		}
	}
	return d, nil
}

// updateVersion matches an update's operationId, whose PATCH twin is named
// for it with Patch in place of Update and any version suffix, which names
// a revision of the reference's input the twin has no need to repeat.
var updateVersion = regexp.MustCompile(`Update(V[0-9]+)?$`)

// twinUpdate gives the item's update PUT a PATCH beside it, which applies
// a JSON Merge Patch through the PUT, and versions the item: ETag on its
// GET, PUT and PATCH, If-Match and 412 on its PUT, PATCH and DELETE. It
// reports whether the item had an update PUT.
//
// ref: decision 0022
func twinUpdate(path string, item ymap) (bool, error) {
	put, ok := item["put"].(ymap)
	if !ok || put["x-rest-style"] != "update" {
		return false, nil
	}
	if _, taken := item["patch"]; taken {
		return false, fmt.Errorf("%w: PATCH %s", ErrPatchClash, path)
	}
	patch, _ := deepCopy(put).(ymap)
	id, _ := put["operationId"].(string)
	patchID := id + "Patch"
	if updateVersion.MatchString(id) {
		patchID = updateVersion.ReplaceAllString(id, "Patch")
	}
	input := "the update's input"
	if body, ok := put["requestBody"].(ymap); ok {
		content, _ := body["content"].(ymap)
		media, _ := content["application/json"].(ymap)
		schema, _ := media["schema"].(ymap)
		if r, ok := schema["$ref"].(string); ok {
			input = strings.TrimPrefix(r, componentsPrefix+"schemas/")
		}
	}
	patch["operationId"] = patchID
	if summary, ok := put["summary"].(string); ok {
		patch["summary"] = strings.NewReplacer("Update", "Patch", "update", "patch").
			Replace(summary)
	}
	patch["description"] = "Change the members of " + input + " that a JSON Merge Patch " +
		"(RFC 7396) names and keep the rest; null resets a member to its default. " +
		"It runs through " + id + ", so it validates and persists as the PUT does."
	patch["requestBody"] = ymap{
		"description": "A JSON Merge Patch of " + input + ".",
		"required":    true,
		"content": ymap{"application/merge-patch+json": ymap{
			"schema": ymap{"$ref": componentsPrefix + "schemas/MergePatch"},
		}},
	}
	for _, k := range []string{"x-graphql", "x-bruno", "x-rest-style", "x-terraform"} {
		delete(patch, k)
	}
	patch["x-upstream"] = "none"
	responses, _ := patch["responses"].(ymap)
	if err := addParams(patch, "IdempotencyKey"); err != nil {
		return false, fmt.Errorf("PATCH %s: %w", path, err)
	}
	setDefault(responses, "409", responseRef("IdempotencyConflict"))
	setDefault(responses, "422", responseRef("IdempotencyMismatch"))
	record(patch, ConventionIdempotency, ConventionMergePatch)
	record(put, ConventionMergePatch)
	item["patch"] = patch
	for _, m := range []string{"get", "put", "patch", "delete"} {
		op, ok := item[m].(ymap)
		if !ok {
			continue
		}
		if m != "delete" {
			etag(op)
		}
		if m != "get" {
			if err := addParams(op, "IfMatch"); err != nil {
				return false, fmt.Errorf("%s %s: %w", strings.ToUpper(m), path, err)
			}
			responses, _ := op["responses"].(ymap)
			setDefault(responses, "412", responseRef("PreconditionFailed"))
		}
		record(op, ConventionIfMatch)
	}
	return true, nil
}

// etag declares the ETag header on op's 200 response.
func etag(op ymap) {
	responses, _ := op["responses"].(ymap)
	ok200, _ := responses["200"].(ymap)
	if ok200 == nil || ok200["$ref"] != nil {
		return
	}
	headers, _ := ok200["headers"].(ymap)
	if headers == nil {
		headers = ymap{}
		ok200["headers"] = headers
	}
	headers["ETag"] = ymap{"$ref": componentsPrefix + "headers/ETag"}
}

// record adds conventions to op's x-infraweave-divergence, kept sorted
// and without repeats.
func record(op ymap, conventions ...string) {
	var all []string
	switch d := op["x-infraweave-divergence"].(type) {
	case []string:
		all = slices.Clone(d)
	case []any:
		all = tags(ymap{"tags": d})
	}
	for _, c := range conventions {
		if !slices.Contains(all, c) {
			all = append(all, c)
		}
	}
	slices.Sort(all)
	op["x-infraweave-divergence"] = all
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case ymap:
		out := make(ymap, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

// strictInputs sets additionalProperties: false on every object schema a
// request body reaches through properties, items and unions, among the
// components the fragment copies and the bodies' inline schemas, so an
// unknown member is refused rather than ignored. It leaves allOf parts
// open, since each part of a composition sees the others' members, and
// leaves a schema alone that already says what it allows.
//
// ref: decision 0022
func strictInputs(picked, comps ymap) {
	schemas, _ := comps["schemas"].(ymap)
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		s, ok := v.(ymap)
		if !ok {
			return
		}
		if r, ok := s["$ref"].(string); ok {
			name, isSchema := strings.CutPrefix(r, componentsPrefix+"schemas/")
			if def, ok := schemas[name]; isSchema && ok && !seen[name] {
				seen[name] = true
				walk(def)
			}
			return
		}
		if props, ok := s["properties"].(ymap); ok {
			if _, set := s["additionalProperties"]; !set {
				s["additionalProperties"] = false
			}
			for _, k := range sortedKeys(props) {
				walk(props[k])
			}
		}
		walk(s["items"])
		for _, k := range []string{"oneOf", "anyOf"} {
			list, _ := s[k].([]any)
			for _, e := range list {
				walk(e)
			}
		}
	}
	for _, p := range sortedKeys(picked) {
		item, _ := picked[p].(ymap)
		for _, m := range methods {
			op, _ := item[m].(ymap)
			body, _ := op["requestBody"].(ymap)
			content, _ := body["content"].(ymap)
			media, _ := content["application/json"].(ymap)
			walk(media["schema"])
		}
	}
}
