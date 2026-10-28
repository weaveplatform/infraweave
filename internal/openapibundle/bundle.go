// Package openapibundle merges the spec fragments under api/src/<service>/
// into the one document that oapi-codegen, the server and the parity check
// read. Authors edit fragments so families can be written in parallel
// without one enormous file; the bundle is generated and committed so a
// reader of api/<service>.openapi.yaml sees the whole surface.
//
// The rules match weaveplatform-artifact-registry's bundler, so fragments
// read the same in both repositories: info.yaml holds the document head,
// every other *.yaml holds only paths and components, and nothing may be
// defined twice.
package openapibundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// InfoFile holds the document head.
const InfoFile = "info.yaml"

// Version is the only OpenAPI version accepted: oapi-codegen generates from
// 3.0, and kin-openapi validates 3.0 documents.
const Version = "3.0.3"

const filePerm = 0o644

// infoKeys are the top-level keys info.yaml may hold, in output order.
var infoKeys = []string{"openapi", "info", "servers", "tags", "security", "externalDocs"}

var (
	ErrNoInfo     = errors.New("openapibundle: info.yaml is required")
	ErrNotMapping = errors.New("openapibundle: top level is not a mapping")
	ErrInfoKeys   = errors.New(
		"openapibundle: info.yaml may only hold " + strings.Join(infoKeys, ", "),
	)
	ErrFragmentKeys  = errors.New("openapibundle: fragments may only hold paths and components")
	ErrDuplicatePath = errors.New("openapibundle: duplicate path")
	ErrDuplicateComp = errors.New("openapibundle: duplicate component")
	ErrDuplicateOpID = errors.New("openapibundle: duplicate operationId")
	ErrVersion       = errors.New("openapibundle: openapi must be " + Version)
	ErrInvalid       = errors.New("openapibundle: bundled document is not valid OpenAPI")
)

// Bundle reads srcDir and returns the merged, validated document as YAML.
// Keys inside paths and each components section are sorted, so the output
// does not depend on which fragment a path or component came from.
func Bundle(srcDir string) ([]byte, error) {
	info, err := readNode(filepath.Join(srcDir, InfoFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w in %s", ErrNoInfo, srcDir)
	}
	if err != nil {
		return nil, err
	}
	for _, k := range mappingKeys(info) {
		if !slices.Contains(infoKeys, k) {
			return nil, fmt.Errorf("%w: %s has %q", ErrInfoKeys, InfoFile, k)
		}
	}
	if v := lookup(info, "openapi"); v == nil || v.Value != Version {
		return nil, fmt.Errorf("%w in %s", ErrVersion, InfoFile)
	}
	names, err := fragments(srcDir)
	if err != nil {
		return nil, err
	}
	paths := mapping()
	comps := map[string]*yaml.Node{}
	// seen maps each path, operationId and component to the fragment that
	// defined it, so a duplicate names both files.
	seen := map[string]string{}
	for _, n := range names {
		frag, err := readNode(filepath.Join(srcDir, n))
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(frag.Content); i += 2 {
			switch key, val := frag.Content[i].Value, frag.Content[i+1]; key {
			case "paths":
				err = mergePaths(paths, val, n, seen)
			case "components":
				err = mergeComponents(comps, val, n, seen)
			default:
				err = fmt.Errorf("%w: %s has %q", ErrFragmentKeys, n, key)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	out, err := encode(filepath.Base(srcDir), assemble(info, paths, comps))
	if err != nil {
		return nil, err
	}
	if err := validate(out); err != nil {
		return nil, err
	}
	return out, nil
}

// BundleAll bundles every service directory under srcRoot into
// <outDir>/<service>.openapi.yaml and returns the services written, sorted.
func BundleAll(srcRoot, outDir string) ([]string, error) {
	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		return nil, fmt.Errorf("openapibundle: %w", err)
	}
	var done []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out, err := Bundle(filepath.Join(srcRoot, e.Name()))
		if err != nil {
			return done, fmt.Errorf("%s: %w", e.Name(), err)
		}
		target := filepath.Join(outDir, e.Name()+".openapi.yaml")
		if err := os.WriteFile(target, out, filePerm); err != nil {
			return done, fmt.Errorf("openapibundle: %w", err)
		}
		done = append(done, e.Name())
	}
	return done, nil
}

// fragments lists every *.yaml in dir except info.yaml, sorted. Any other
// YAML file is a fragment: a misnamed one fails the key check instead of
// being skipped without a word.
func fragments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("openapibundle: %w", err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && n != InfoFile && strings.HasSuffix(n, ".yaml") {
			names = append(names, n)
		}
	}
	return names, nil
}

func assemble(info, paths *yaml.Node, comps map[string]*yaml.Node) *yaml.Node {
	doc := mapping()
	for _, k := range infoKeys {
		if v := lookup(info, k); v != nil {
			doc.Content = append(doc.Content, scalar(k), v)
		}
	}
	sortMapping(paths)
	doc.Content = append(doc.Content, scalar("paths"), paths)
	if len(comps) == 0 {
		return doc
	}
	cn := mapping()
	for _, kind := range sortedKeys(comps) {
		sortMapping(comps[kind])
		cn.Content = append(cn.Content, scalar(kind), comps[kind])
	}
	doc.Content = append(doc.Content, scalar("components"), cn)
	return doc
}

func encode(service string, doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(
		&buf,
		"# Generated by `make bundle` from api/src/%s/; edit the fragments, not this file.\n",
		service,
	)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("openapibundle: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("openapibundle: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// validate loads the bundle as kin-openapi would and runs its 3.0
// validation, so a dangling $ref or a malformed operation fails here rather
// than in code generation.
func validate(data []byte) error {
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

func readNode(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("openapibundle: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("openapibundle: parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return mapping(), nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: %s", ErrNotMapping, path)
	}
	return root, nil
}

func mergePaths(dst, src *yaml.Node, frag string, seen map[string]string) error {
	if src.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s paths", ErrNotMapping, frag)
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		p, item := src.Content[i].Value, src.Content[i+1]
		if prev, ok := seen["path "+p]; ok {
			return fmt.Errorf("%w %s in %s (first in %s)", ErrDuplicatePath, p, frag, prev)
		}
		seen["path "+p] = frag
		for j := 0; j+1 < len(item.Content); j += 2 {
			id := lookup(item.Content[j+1], "operationId")
			if id == nil {
				continue
			}
			if prev, ok := seen["operationId "+id.Value]; ok {
				return fmt.Errorf(
					"%w %s in %s (first in %s)",
					ErrDuplicateOpID,
					id.Value,
					frag,
					prev,
				)
			}
			seen["operationId "+id.Value] = frag
		}
		dst.Content = append(dst.Content, src.Content[i], item)
	}
	return nil
}

func mergeComponents(
	dst map[string]*yaml.Node,
	src *yaml.Node,
	frag string,
	seen map[string]string,
) error {
	if src.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s components", ErrNotMapping, frag)
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		kind, items := src.Content[i].Value, src.Content[i+1]
		if items.Kind != yaml.MappingNode {
			return fmt.Errorf("%w: %s components/%s", ErrNotMapping, frag, kind)
		}
		if dst[kind] == nil {
			dst[kind] = mapping()
		}
		for j := 0; j+1 < len(items.Content); j += 2 {
			ref := kind + "/" + items.Content[j].Value
			if prev, ok := seen["component "+ref]; ok {
				return fmt.Errorf("%w %s in %s (first in %s)", ErrDuplicateComp, ref, frag, prev)
			}
			seen["component "+ref] = frag
			dst[kind].Content = append(dst[kind].Content, items.Content[j], items.Content[j+1])
		}
	}
	return nil
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mappingKeys(m *yaml.Node) []string {
	out := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

func sortedKeys(m map[string]*yaml.Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func mapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

func scalar(
	v string,
) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// sortMapping orders a mapping's pairs by key, keeping each value node (with
// its comments and scalar styles) intact.
func sortMapping(m *yaml.Node) {
	type pair struct{ k, v *yaml.Node }
	pairs := make([]pair, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		pairs = append(pairs, pair{m.Content[i], m.Content[i+1]})
	}
	slices.SortStableFunc(
		pairs,
		func(a, b pair) int { return strings.Compare(a.k.Value, b.k.Value) },
	)
	m.Content = m.Content[:0]
	for _, p := range pairs {
		m.Content = append(m.Content, p.k, p.v)
	}
}
