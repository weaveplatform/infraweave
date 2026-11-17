package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

const yamlPointer = "/runtimeConfig/yaml"

// Parse checks either trigger form before creating a run. Both become Input;
// YAML coercions are refused so a quoted Boolean or numeric string never
// silently changes a setting's type. Errors identify keys, never values.
func Parse(raw json.RawMessage, vendor string) (Input, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Input{}, invalid("/runtimeConfig", kernel.ReasonFormat, "The runtime configuration must be an object.")
	}
	var in Input
	if value, ok := fields["yaml"]; ok && string(value) != "null" {
		if len(fields) != 1 {
			return in, invalid(yamlPointer, kernel.ReasonRule, "yaml is set beside other runtime configuration properties.")
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return in, invalid(yamlPointer, kernel.ReasonFormat, "yaml must be a string.")
		}
		parsed, err := parseYAML(text)
		if err != nil {
			return in, err
		}
		in = parsed
		in.YAMLInput = true
	} else if err := parseFields(fields, &in); err != nil {
		return in, err
	}
	if err := CheckVariables(in.Environment, "/runtimeConfig/environment"); err != nil {
		if in.YAMLInput {
			var refused *kernel.Invalid
			if errors.As(err, &refused) {
				for i := range refused.Errors {
					refused.Errors[i].Pointer = yamlPointer
					refused.Errors[i].Message = "environment: " + refused.Errors[i].Message
				}
			}
		}
		return Input{}, err //nolint:wrapcheck // the shared validator names each refused variable
	}
	if root := in.ProjectRoot; root != nil && *root != "" &&
		(path.Clean(*root) != *root || strings.HasPrefix(*root, "/") || *root == ".." ||
			strings.HasPrefix(*root, "../") || strings.ContainsAny(*root, "\\\x00")) {
		return Input{}, invalid("/runtimeConfig/projectRoot", kernel.ReasonRule,
			"project_root must be a clean relative path within the checkout.")
	}
	if err := in.validateVendor(vendor); err != nil {
		return Input{}, err
	}
	return in, nil
}

func parseFields(fields map[string]json.RawMessage, in *Input) error {
	in.Hooks = map[string][]string{}
	hooks := map[string]string{}
	for _, phase := range stacksapi.HookPhases {
		parts := strings.Split(phase, "_")
		hooks[parts[0]+strings.ToUpper(parts[1][:1])+parts[1][1:]] = phase
	}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		raw := fields[key]
		phase, hook := hooks[key]
		switch key {
		case "environment", "gitSparseCheckoutPaths", "projectRoot", "runnerImage", "yaml":
		default:
			if !hook {
				return invalid("/runtimeConfig/"+key, kernel.ReasonUnknownField, "Unknown runtime configuration property "+key+".")
			}
		}
		if string(raw) == "null" {
			continue
		}
		if !nonNullJSON(raw) {
			return invalid("/runtimeConfig/"+key, kernel.ReasonFormat, key+" contains a null value where a string is required.")
		}
		var err error
		switch key {
		case "environment":
			err = json.Unmarshal(raw, &in.Environment)
		case "gitSparseCheckoutPaths":
			err = json.Unmarshal(raw, &in.GitSparseCheckoutPaths)
		case "projectRoot":
			err = json.Unmarshal(raw, &in.ProjectRoot)
		case "runnerImage":
			err = json.Unmarshal(raw, &in.RunnerImage)
		default:
			var commands []string
			err = json.Unmarshal(raw, &commands)
			in.Hooks[phase] = clone(commands)
		}
		if err != nil {
			return invalid("/runtimeConfig/"+key, kernel.ReasonFormat, key+" has the wrong type.")
		}
	}
	return nil
}

// nonNullJSON prevents encoding/json's null-to-zero-value coercion inside
// string lists and variable objects. Known nullable top-level fields have
// already been treated as omitted by parseFields.
func nonNullJSON(raw json.RawMessage) bool {
	var value any
	_ = json.Unmarshal(raw, &value) // the enclosing object already decoded
	var valid func(any) bool
	valid = func(value any) bool {
		switch v := value.(type) {
		case nil:
			return false
		case []any:
			for _, item := range v {
				if !valid(item) {
					return false
				}
			}
		case map[string]any:
			for _, item := range v {
				if !valid(item) {
					return false
				}
			}
		}
		return true
	}
	return valid(value)
}

func parseYAML(text string) (Input, error) {
	in := Input{Hooks: map[string][]string{}}
	decoder := yaml.NewDecoder(strings.NewReader(text))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil && !errors.Is(err, io.EOF) {
		return in, invalid(yamlPointer, kernel.ReasonFormat, "yaml must be one valid mapping.")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return in, invalid(yamlPointer, kernel.ReasonFormat, "yaml must contain one complete document.")
	}
	if len(document.Content) == 0 || document.Content[0].Kind == yaml.ScalarNode &&
		document.Content[0].Tag == "!!null" && document.Content[0].Value == "" {
		return in, nil
	}
	fields, err := mapping(document.Content[0], "runtime configuration")
	if err != nil {
		return in, err
	}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		node := fields[key]
		switch key {
		case "environment":
			var env map[string]*yaml.Node
			if env, err = mapping(node, key); err != nil {
				return in, err
			}
			in.Environment = []Variable{}
			for _, name := range slices.Sorted(maps.Keys(env)) {
				var value string
				if value, err = stringValue(env[name], key+"."+name); err != nil {
					return in, err
				}
				in.Environment = append(in.Environment, Variable{Key: name, Value: value})
			}
		case "project_root":
			in.ProjectRoot, err = stringPointer(node, key)
		case "runner_image":
			in.RunnerImage, err = stringPointer(node, key)
		case "terraform_version":
			in.TerraformVersion, err = stringPointer(node, key)
		case "opentofu_version":
			in.OpenTofuVersion, err = stringPointer(node, key)
		case "terraform_workflow_tool":
			in.WorkflowTool, err = enumValue(node, key, "TERRAFORM_FOSS", "OPEN_TOFU", "CUSTOM")
		case "git_sparse_checkout_paths":
			var paths []string
			paths, err = stringList(node, key)
			in.GitSparseCheckoutPaths = &paths
		case "opentofu":
			in.OpenTofu, err = parseOpenTofu(node)
		case "terragrunt":
			in.Terragrunt, err = parseTerragrunt(node)
		default:
			if !slices.Contains(stacksapi.HookPhases, key) {
				return in, unknown(key)
			}
			in.Hooks[key], err = stringList(node, key)
		}
		if err != nil {
			return in, err
		}
	}
	return in, nil
}

func parseOpenTofu(node *yaml.Node) (*OpenTofuInput, error) {
	fields, err := mapping(node, "opentofu")
	if err != nil {
		return nil, err
	}
	out := &OpenTofuInput{}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		switch key {
		case "version":
			out.Version, err = stringPointer(fields[key], "opentofu."+key)
		case "workflow_tool":
			out.WorkflowTool, err = enumValue(fields[key], "opentofu."+key, "OPENTOFU", "CUSTOM")
		default:
			return nil, unknown("opentofu." + key)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseTerragrunt(node *yaml.Node) (*TerragruntInput, error) {
	fields, err := mapping(node, "terragrunt")
	if err != nil {
		return nil, err
	}
	out := &TerragruntInput{}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		name := "terragrunt." + key
		switch key {
		case "terragrunt_tool":
			out.Tool, err = enumValue(fields[key], name, "TERRAFORM_FOSS", "OPEN_TOFU")
		case "terragrunt_version":
			out.Version, err = stringPointer(fields[key], name)
		case "terraform_version":
			out.TerraformVersion, err = stringPointer(fields[key], name)
		case "use_run_all":
			out.UseRunAll, err = boolValue(fields[key], name)
		case "use_smart_sanitization":
			out.UseSmartSanitization, err = boolValue(fields[key], name)
		case "use_state_management":
			out.UseStateManagement, err = boolValue(fields[key], name)
		default:
			return nil, unknown(name)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func mapping(node *yaml.Node, name string) (map[string]*yaml.Node, error) {
	node = unalias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, invalid(yamlPointer, kernel.ReasonFormat, name+" must be a mapping.")
	}
	seen := map[string]bool{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Tag != "!!str" && key.Tag != "!!merge" {
			return nil, invalid(yamlPointer, kernel.ReasonFormat, name+" keys must be strings.")
		}
		if seen[key.Value] {
			return nil, invalid(yamlPointer, kernel.ReasonDuplicate, name+" repeats key "+key.Value+".")
		}
		seen[key.Value] = true
	}
	var raw map[string]yaml.Node
	if err := node.Decode(&raw); err != nil {
		return nil, invalid(yamlPointer, kernel.ReasonFormat, name+" cannot be read as a mapping.")
	}
	out := make(map[string]*yaml.Node, len(raw))
	for key, value := range raw {
		out[key] = &value
	}
	return out, nil
}

func unalias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

func stringValue(node *yaml.Node, name string) (string, error) {
	node = unalias(node)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", invalid(yamlPointer, kernel.ReasonFormat, name+" must be a string.")
	}
	return node.Value, nil
}

func stringPointer(node *yaml.Node, name string) (*string, error) {
	value, err := stringValue(node, name)
	return &value, err
}

func boolValue(node *yaml.Node, name string) (bool, error) {
	node = unalias(node)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, invalid(yamlPointer, kernel.ReasonFormat, name+" must be a boolean.")
	}
	var value bool
	if err := node.Decode(&value); err != nil {
		return false, invalid(yamlPointer, kernel.ReasonFormat, name+" must be a boolean.")
	}
	return value, nil
}

func stringList(node *yaml.Node, name string) ([]string, error) {
	node = unalias(node)
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, invalid(yamlPointer, kernel.ReasonFormat, name+" must be a list of strings.")
	}
	out := make([]string, 0, len(node.Content))
	for i, item := range node.Content {
		value, err := stringValue(item, fmt.Sprintf("%s[%d]", name, i))
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func enumValue(node *yaml.Node, name string, allowed ...string) (*string, error) {
	value, err := stringValue(node, name)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(allowed, value) {
		return nil, invalid(yamlPointer, kernel.ReasonEnum, name+" is not a supported tool.")
	}
	return &value, nil
}

func unknown(name string) error {
	return invalid(yamlPointer, kernel.ReasonUnknownField, "Unknown runtime configuration key "+name+".")
}

func invalid(pointer, reason, message string) error {
	return &kernel.Invalid{
		Detail: "The runtime configuration is refused.",
		Errors: []kernel.FieldError{{Pointer: pointer, Reason: reason, Message: message}},
	}
}
