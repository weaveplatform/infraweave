package runtimeconfig

import (
	"fmt"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// checkEnvironmentOrigins runs on the original, type-checked YAML graph.
// Converting aliases to strings first would copy an environment value into
// clear execution metadata; decoded yaml.Node copies also lose its identity.
// An anchor may be defined in either setting, so classify every environment
// value before checking the other settings. Equal independent literals remain
// independent, and aliases between environment values remain allowed.
func checkEnvironmentOrigins(root *yaml.Node) error {
	origins := map[*yaml.Node]bool{}
	mappingCandidates(root, map[*yaml.Node]bool{}, func(key string, node *yaml.Node) {
		if key == "environment" {
			mappingCandidates(node, map[*yaml.Node]bool{}, func(_ string, value *yaml.Node) {
				valueOrigins(value, origins)
			})
		}
	})
	if len(origins) == 0 {
		return nil
	}
	fields := originalMapping(root, map[*yaml.Node]bool{})
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if key == "environment" {
			continue
		}
		if err := checkSettingOrigin(fields[key], key, origins); err != nil {
			return err
		}
	}
	return nil
}

// mappingCandidates visits every declared value, including losing merge
// candidates. Shadowing an environment value cannot declassify its origin.
func mappingCandidates(node *yaml.Node, seen map[*yaml.Node]bool, visit func(string, *yaml.Node)) {
	node = unalias(node)
	if node == nil || seen[node] {
		return
	}
	seen[node] = true
	switch node.Kind {
	case yaml.SequenceNode:
		for _, source := range node.Content {
			mappingCandidates(source, seen, visit)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			if key := node.Content[i]; key.Tag == "!!merge" {
				mappingCandidates(node.Content[i+1], seen, visit)
			} else {
				visit(key.Value, node.Content[i+1])
			}
		}
	}
}

func valueOrigins(node *yaml.Node, origins map[*yaml.Node]bool) {
	node = unalias(node)
	if node == nil || origins[node] {
		return
	}
	origins[node] = true
	for _, child := range node.Content {
		valueOrigins(child, origins)
	}
}

func checkSettingOrigin(node *yaml.Node, name string, origins map[*yaml.Node]bool) error {
	node = unalias(node)
	if origins[node] {
		return invalid(yamlPointer, kernel.ReasonRule,
			name+" reuses an environment value through a YAML alias.")
	}
	switch node.Kind {
	case yaml.MappingNode:
		fields := originalMapping(node, map[*yaml.Node]bool{})
		for _, key := range slices.Sorted(maps.Keys(fields)) {
			if err := checkSettingOrigin(fields[key], name+"."+key, origins); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			if err := checkSettingOrigin(item, fmt.Sprintf("%s[%d]", name, i), origins); err != nil {
				return err
			}
		}
	}
	return nil
}

// originalMapping follows the already validated mapping's merge precedence
// while preserving pointers to its original values. Explicit keys win over
// merges; the first mapping in a merge sequence wins over later mappings.
// ancestors bounds a recursive merge even when explicit keys shadow it.
func originalMapping(node *yaml.Node, ancestors map[*yaml.Node]bool) map[string]*yaml.Node {
	node = unalias(node)
	if node == nil || node.Kind != yaml.MappingNode || ancestors[node] {
		return nil
	}
	ancestors[node] = true
	defer delete(ancestors, node)
	out := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i]; key.Tag != "!!merge" {
			out[key.Value] = node.Content[i+1]
		}
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Tag != "!!merge" {
			continue
		}
		value := unalias(node.Content[i+1])
		sources := []*yaml.Node{value}
		if value.Kind == yaml.SequenceNode {
			sources = value.Content
		}
		for _, source := range sources {
			for key, value := range originalMapping(source, ancestors) {
				if _, exists := out[key]; !exists {
					out[key] = value
				}
			}
		}
	}
	return out
}
