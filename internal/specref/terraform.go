package specref

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// ProviderSource is the registry address of the official Spacelift provider.
const ProviderSource = "registry.terraform.io/spacelift-io/spacelift"

var (
	ErrProviderMissing = errors.New("terraform schema output lacks the spacelift provider")
	ErrBadVersion      = errors.New("provider version must be an exact semantic version")
)

// TerraformSchema is the Spacelift provider's schema as `terraform providers
// schema -json` reports it, reduced to the one provider and stripped of the
// description_kind noise.
type TerraformSchema struct {
	Resources   map[string]TFBlock `json:"resources"`
	DataSources map[string]TFBlock `json:"dataSources"`
}

type TFBlock struct {
	Description string                 `json:"description,omitempty"`
	Deprecated  bool                   `json:"deprecated,omitempty"`
	Attributes  map[string]TFAttribute `json:"attributes,omitempty"`
	BlockTypes  map[string]TFBlockType `json:"blockTypes,omitempty"`
}

type TFAttribute struct {
	Type        json.RawMessage `json:"type,omitempty"`
	Description string          `json:"description,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Optional    bool            `json:"optional,omitempty"`
	Computed    bool            `json:"computed,omitempty"`
	Sensitive   bool            `json:"sensitive,omitempty"`
	Deprecated  bool            `json:"deprecated,omitempty"`
}

type TFBlockType struct {
	NestingMode string  `json:"nestingMode"`
	MinItems    int     `json:"minItems,omitempty"`
	MaxItems    int     `json:"maxItems,omitempty"`
	Block       TFBlock `json:"block"`
}

// The raw shapes of terraform's JSON output, whose snake_case keys are fixed
// by terraform.
//
//nolint:tagliatelle // external format
type rawProviderSchemas struct {
	ProviderSchemas map[string]struct {
		ResourceSchemas   map[string]rawSchema `json:"resource_schemas"`
		DataSourceSchemas map[string]rawSchema `json:"data_source_schemas"`
	} `json:"provider_schemas"`
}

type rawSchema struct {
	Block rawBlock `json:"block"`
}

//nolint:tagliatelle // external format
type rawBlock struct {
	Description string                  `json:"description"`
	Deprecated  bool                    `json:"deprecated"`
	Attributes  map[string]TFAttribute  `json:"attributes"`
	BlockTypes  map[string]rawBlockType `json:"block_types"`
}

//nolint:tagliatelle // external format
type rawBlockType struct {
	NestingMode string   `json:"nesting_mode"`
	MinItems    int      `json:"min_items"`
	MaxItems    int      `json:"max_items"`
	Block       rawBlock `json:"block"`
}

func (b rawBlock) convert() TFBlock {
	out := TFBlock{Description: b.Description, Deprecated: b.Deprecated, Attributes: b.Attributes}
	if len(b.BlockTypes) > 0 {
		out.BlockTypes = make(map[string]TFBlockType, len(b.BlockTypes))
		for k, bt := range b.BlockTypes {
			out.BlockTypes[k] = TFBlockType{
				NestingMode: bt.NestingMode,
				MinItems:    bt.MinItems,
				MaxItems:    bt.MaxItems,
				Block:       bt.Block.convert(),
			}
		}
	}
	return out
}

// ParseTerraformSchema reduces `terraform providers schema -json` output to
// the Spacelift provider.
func ParseTerraformSchema(data []byte) (*TerraformSchema, error) {
	var raw rawProviderSchemas
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode terraform schema: %w", err)
	}
	p, ok := raw.ProviderSchemas[ProviderSource]
	if !ok {
		return nil, ErrProviderMissing
	}
	out := &TerraformSchema{Resources: map[string]TFBlock{}, DataSources: map[string]TFBlock{}}
	for k, s := range p.ResourceSchemas {
		out.Resources[k] = s.Block.convert()
	}
	for k, s := range p.DataSourceSchemas {
		out.DataSources[k] = s.Block.convert()
	}
	return out, nil
}

// CommandRunner runs a program in dir and returns its standard output.
type CommandRunner func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

// ExecRunner runs commands with os/exec, keeping stderr for error messages.
func ExecRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	// The binary comes from the operator's --terraform flag and the arguments
	// are fixed by FetchTerraformSchema, so there is no untrusted input.
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // see above
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %v: %w: %s", name, args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out, nil
}

var semver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// FetchTerraformSchema installs the pinned provider into a scratch working
// directory and asks terraform for its schema. Loading a schema never
// configures the provider, so no Spacelift credentials are involved.
func FetchTerraformSchema(
	ctx context.Context,
	run CommandRunner,
	terraform, version string,
) ([]byte, error) {
	// The version lands in HCL; an exact version keeps the pin honest and
	// rules out injecting anything else into the file.
	if !semver.MatchString(version) {
		return nil, fmt.Errorf("%w: %q", ErrBadVersion, version)
	}
	dir, err := os.MkdirTemp("", "specref-terraform-")
	if err != nil {
		return nil, fmt.Errorf("terraform scratch dir: %w", err)
	}
	defer os.RemoveAll(dir)
	main := fmt.Sprintf(
		"terraform {\n  required_providers {\n    spacelift = {\n      source  = %q\n      version = %q\n    }\n  }\n}\n",
		"spacelift-io/spacelift",
		version,
	)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o600); err != nil {
		return nil, fmt.Errorf("write main.tf: %w", err)
	}
	if _, err := run(
		ctx,
		dir,
		terraform,
		"init",
		"-backend=false",
		"-input=false",
		"-no-color",
	); err != nil {
		return nil, err
	}
	return run(ctx, dir, terraform, "providers", "schema", "-json")
}
