package runtimeconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// FilePath is read at the repository root, independent of projectRoot.
const FilePath = ".infraweave/config.yml"

// FileError identifies a refused key without retaining its source or value.
type FileError struct {
	Path, Reason, Message string
}

func (e *FileError) Error() string { return e.Path + ": " + e.Message }

// ParseFile reads one bounded document. Runtime extraction and public YAML
// projection use separate node trees: redaction cannot change execution values.
func ParseFile(data []byte, slug, vendor, unknownKeys string) File {
	out := File{Version: "1"}
	if len(data) > runsapi.SourceFileLimit {
		out.Refusal = &FileError{FilePath, kernel.ReasonRule, "the file exceeds 1 MiB"}
		return out
	}
	root, refusal := fileDocument(data)
	if refusal != nil {
		out.Refusal = refusal
		return out
	}
	origins := fileOrigins(root)
	if sensitiveMappingKey(root, origins, map[*yaml.Node]bool{}) {
		out.Refusal = &FileError{FilePath, kernel.ReasonRule, "a mapping key shares an environment value's YAML origin"}
		return out
	}
	if _, err := mapping(root, FilePath); err != nil {
		out.Refusal = fileError(err, FilePath)
		return out
	}
	fields := originalMapping(root, map[*yaml.Node]bool{})
	if node := fields["version"]; node != nil {
		version, err := stringValue(node, "version")
		if err != nil || version != "1" && version != "2" {
			out.Refusal = &FileError{"version", kernel.ReasonEnum, "must be the string 1 or 2"}
			return out
		}
		out.Version = version
	}
	var entries map[string]*yaml.Node
	if node := fields["stacks"]; node != nil {
		if _, err := mapping(node, "stacks"); err != nil {
			out.Refusal = fileError(err, "stacks")
			return out
		}
		entries = originalMapping(node, map[*yaml.Node]bool{})
	}
	known := map[string]bool{
		"version": true, "stack_defaults": true, "stacks": true,
		"module_version": true, "test_defaults": true, "tests": true,
	}
	projection := map[string]*yaml.Node{}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if !known[key] {
			out.Notices = append(out.Notices, "unknown key "+key)
			projection[key] = fields[key]
		}
	}
	defaultsDiagnostic := map[string]*yaml.Node{}
	if node := fields["stack_defaults"]; node != nil {
		out.Defaults, out.Refusal = fileInput(node, "stack_defaults", vendor, origins, &out.Notices)
		defaultsDiagnostic = diagnosticFields(node)
		maps.Copy(projection, defaultsDiagnostic)
	}
	selected := fields["stack_defaults"]
	if node := entries[slug]; node != nil {
		selected = node
		entry, err := fileInput(node, "stacks."+slug, vendor, origins, &out.Notices)
		out.Entry = &entry
		if out.Refusal == nil {
			out.Refusal = err
		}
	}
	if selected != nil {
		for key, value := range originalMapping(selected, map[*yaml.Node]bool{}) {
			if (key == "opentofu" || key == "terragrunt") && defaultsDiagnostic[key] != nil && unalias(value).Kind == yaml.MappingNode {
				combined := originalMapping(defaultsDiagnostic[key], map[*yaml.Node]bool{})
				maps.Copy(combined, originalMapping(value, map[*yaml.Node]bool{}))
				projection[key] = nodeMapping(combined)
				continue
			}
			projection[key] = value
		}
	}
	public := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range slices.Sorted(maps.Keys(projection)) {
		public.Content = append(public.Content, scalar(key), publicNode(projection[key], origins))
	}
	text, _ := yaml.Marshal(public) // fresh bounded nodes, no external alias links
	out.YAML = string(text)
	slices.Sort(out.Notices)
	if out.Refusal == nil && unknownKeys == "reject" && len(out.Notices) > 0 {
		out.Refusal = &FileError{strings.TrimPrefix(out.Notices[0], "unknown key "), kernel.ReasonUnknownField, "unknown key"}
	}
	return out
}

// diagnosticFields keeps unknown defaults without importing their known
// execution settings into a selected entry's source projection.
func diagnosticFields(node *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	for key, value := range originalMapping(node, map[*yaml.Node]bool{}) {
		if !runtimeKey(key) {
			out[key] = value
			continue
		}
		if key != "opentofu" && key != "terragrunt" || unalias(value).Kind != yaml.MappingNode {
			continue
		}
		unknown := map[string]*yaml.Node{}
		for field, child := range originalMapping(value, map[*yaml.Node]bool{}) {
			if !slices.Contains(blockKeys(key), field) {
				unknown[field] = child
			}
		}
		if len(unknown) > 0 {
			out[key] = nodeMapping(unknown)
		}
	}
	return out
}

func nodeMapping(fields map[string]*yaml.Node) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		out.Content = append(out.Content, scalar(key), fields[key])
	}
	return out
}

func fileError(err error, prefix string) *FileError {
	if inv, ok := errors.AsType[*kernel.Invalid](err); ok && len(inv.Errors) > 0 {
		field := inv.Errors[0]
		return &FileError{prefix, field.Reason, field.Message}
	}
	return &FileError{prefix, kernel.ReasonFormat, "cannot be read as runtime configuration"}
}

func fileInput(node *yaml.Node, prefix, vendor string, origins map[*yaml.Node]bool, notices *[]string) (Input, *FileError) {
	if _, err := mapping(node, prefix); err != nil {
		return Input{}, fileError(err, prefix)
	}
	fields := originalMapping(node, map[*yaml.Node]bool{})
	filtered := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		value := fields[key]
		if !runtimeKey(key) {
			*notices = append(*notices, "unknown key "+prefix+"."+key)
			continue
		}
		if key == "opentofu" || key == "terragrunt" {
			block, refusal := fileBlock(value, prefix+"."+key, key, origins, notices)
			if refusal != nil {
				return Input{}, refusal
			}
			filtered.Content = append(filtered.Content, scalar(key), block)
			continue
		}
		if key != "environment" {
			if err := checkSettingOrigin(value, key, origins); err != nil {
				return Input{}, fileError(err, prefix+"."+key)
			}
		} else {
			if _, err := mapping(value, prefix+".environment"); err != nil {
				return Input{}, fileError(err, prefix+".environment")
			}
			environment := originalMapping(value, map[*yaml.Node]bool{})
			for _, name := range slices.Sorted(maps.Keys(environment)) {
				if _, err := stringValue(environment[name], "environment."+name); err != nil {
					return Input{}, fileError(err, prefix+".environment."+name)
				}
			}
		}
		filtered.Content = append(filtered.Content, scalar(key), publicNode(value, nil))
	}
	text, _ := yaml.Marshal(filtered)
	raw, _ := json.Marshal(map[string]string{"yaml": string(text)})
	in, err := Parse(raw, vendor)
	if err != nil {
		refusal := fileError(err, prefix)
		if inv, ok := errors.AsType[*kernel.Invalid](err); ok && len(inv.Errors) > 0 {
			key, _, _ := strings.Cut(inv.Errors[0].Message, " ")
			key = strings.TrimSuffix(key, ":")
			root, _, _ := strings.Cut(key, ".")
			root, _, _ = strings.Cut(root, "[")
			if runtimeKey(root) {
				refusal.Path += "." + key
			}
		}
		return Input{}, refusal
	}
	return in, nil
}

func runtimeKey(key string) bool {
	return slices.Contains(stacksapi.HookPhases, key) || slices.Contains([]string{
		"environment", "project_root", "runner_image", "terraform_version", "opentofu_version",
		"terraform_workflow_tool", "git_sparse_checkout_paths", "opentofu", "terragrunt",
	}, key)
}

func fileBlock(node *yaml.Node, prefix, kind string, origins map[*yaml.Node]bool, notices *[]string) (*yaml.Node, *FileError) {
	if _, err := mapping(node, prefix); err != nil {
		return nil, fileError(err, prefix)
	}
	allowed := blockKeys(kind)
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	fields := originalMapping(node, map[*yaml.Node]bool{})
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if !slices.Contains(allowed, key) {
			*notices = append(*notices, "unknown key "+prefix+"."+key)
			continue
		}
		if err := checkSettingOrigin(fields[key], kind+"."+key, origins); err != nil {
			return nil, fileError(err, prefix+"."+key)
		}
		out.Content = append(out.Content, scalar(key), publicNode(fields[key], nil))
	}
	return out, nil
}

func blockKeys(kind string) []string {
	if kind == "terragrunt" {
		return []string{"terragrunt_tool", "terragrunt_version", "terraform_version", "use_run_all", "use_smart_sanitization", "use_state_management"}
	}
	return []string{"version", "workflow_tool"}
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// publicNode expands aliases into an independent graph and drops comments and
// anchor declarations, which can otherwise retain values outside the selection.
func publicNode(node *yaml.Node, origins map[*yaml.Node]bool) *yaml.Node {
	node = unalias(node)
	if origins[node] {
		return scalar("")
	}
	out := &yaml.Node{Kind: node.Kind, Tag: node.Tag, Value: node.Value}
	if node.Kind == yaml.MappingNode {
		fields := originalMapping(node, map[*yaml.Node]bool{})
		for _, key := range slices.Sorted(maps.Keys(fields)) {
			out.Content = append(out.Content, scalar(key), publicNode(fields[key], origins))
		}
	} else {
		for _, child := range node.Content {
			out.Content = append(out.Content, publicNode(child, origins))
		}
	}
	return out
}

func fileOrigins(root *yaml.Node) map[*yaml.Node]bool {
	origins := map[*yaml.Node]bool{}
	input := func(node *yaml.Node) {
		mappingCandidates(node, map[*yaml.Node]bool{}, func(key string, value *yaml.Node) {
			if key == "environment" {
				mappingCandidates(value, map[*yaml.Node]bool{}, func(_ string, value *yaml.Node) { valueOrigins(value, origins) })
			}
		})
	}
	mappingCandidates(root, map[*yaml.Node]bool{}, func(key string, node *yaml.Node) {
		if key == "stack_defaults" {
			input(node)
		}
		if key == "stacks" {
			mappingCandidates(node, map[*yaml.Node]bool{}, func(_ string, node *yaml.Node) { input(node) })
		}
	})
	return origins
}

func fileDocument(data []byte) (*yaml.Node, *FileError) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document, extra yaml.Node
	err := decoder.Decode(&document)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, &FileError{FilePath, kernel.ReasonFormat, "must be one valid YAML mapping"}
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, &FileError{FilePath, kernel.ReasonFormat, "must contain one complete document"}
	}
	if len(document.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := document.Content[0]
	count, expandedBytes := 0, 0
	active := map[*yaml.Node]bool{}
	var visit func(*yaml.Node, int) bool
	visit = func(node *yaml.Node, depth int) bool {
		count++
		if node == nil || active[node] || depth > 64 || count > 65536 {
			return false
		}
		expandedBytes += len(node.Value) + depth*2 + 12
		if expandedBytes > runsapi.SourceFileLimit {
			return false
		}
		active[node] = true
		defer delete(active, node)
		if node.Kind == yaml.AliasNode {
			return visit(node.Alias, depth+1)
		}
		for _, child := range node.Content {
			if !visit(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !visit(root, 0) {
		return nil, &FileError{FilePath, kernel.ReasonRule, "contains cyclic or excessive YAML expansion"}
	}
	if root.Kind != yaml.MappingNode {
		return nil, &FileError{FilePath, kernel.ReasonFormat, "must be a YAML mapping"}
	}
	return root, nil
}
