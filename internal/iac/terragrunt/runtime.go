package terragrunt

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// RuntimeRunAll reads the trigger's runtime configuration, stored as JSON.
// A YAML terragrunt block replaces the stack's mode: an omitted use_run_all
// is false. Nil means no block, so the stack decides. The other block keys
// stay unread. The YAML is one complete document. Checking node tags
// avoids YAML's legacy string-to-bool
// coercion, which would otherwise accept a quoted "yes".
func RuntimeRunAll(raw json.RawMessage) (*bool, error) {
	var runtime struct {
		YAML *string `json:"yaml"`
	}
	if len(raw) == 0 {
		return nil, nil //nolint:nilnil // an absent block leaves the stack's mode in force
	}
	if err := json.Unmarshal(raw, &runtime); err != nil {
		return nil, runtimeError("The runtime configuration cannot be read.")
	}
	if runtime.YAML == nil {
		return nil, nil //nolint:nilnil // an absent block leaves the stack's mode in force
	}
	var doc map[string]yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(*runtime.YAML))
	if err := decoder.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, runtimeError("yaml must be a valid mapping.")
	}
	// Decode does not consume a second document; checking EOF rejects
	// ignored configuration and malformed YAML after the first document.
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, runtimeError("yaml must contain one valid mapping.")
	}
	node, ok := doc[Kind]
	if !ok {
		return nil, nil //nolint:nilnil // an absent block leaves the stack's mode in force
	}
	var block map[string]yaml.Node
	if err := node.Decode(&block); err != nil || block == nil {
		return nil, runtimeError("terragrunt must be a mapping.")
	}
	mode := false
	if value, ok := block["use_run_all"]; ok {
		if value.Kind == yaml.AliasNode {
			value = *value.Alias
		}
		if value.Tag != "!!bool" {
			return nil, runtimeError("terragrunt.use_run_all must be a boolean.")
		}
		if err := value.Decode(&mode); err != nil {
			return nil, runtimeError("terragrunt.use_run_all must be a boolean.")
		}
	}
	return &mode, nil
}

func runtimeError(message string) error {
	return &kernel.Invalid{
		Detail: "The Terragrunt runtime configuration is refused.",
		Errors: []kernel.FieldError{{
			Pointer: "/runtimeConfig/yaml", Reason: kernel.ReasonRule, Message: message,
		}},
	}
}
