package sanitize

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Action payloads are artifact-only. Their masks describe config_values;
// addresses, provider identity and invocation provenance remain structural.
func action(a map[string]any, op *operation) error {
	if err := knownFields(a, []string{
		"address", "type", "name", "provider_name", "config_values", "config_sensitive",
		"config_unknown", "lifecycle_action_trigger", "invoke_action_trigger",
	}); err != nil {
		return err
	}
	if err := stringFields(a, "address", "type", "name", "provider_name"); err != nil {
		return err
	}
	values, err := mapping(a["config_values"])
	if err != nil {
		return err
	}
	// Unknown paths are structural boolean masks, never another value tree.
	if _, err := marked(values, a["config_unknown"], nil); err != nil {
		return err
	}
	if err := actionTrigger(a); err != nil {
		return err
	}
	return field(a, "config_values", a["config_sensitive"], op)
}

func actionTrigger(a map[string]any) error {
	lifecycle, err := mapping(a["lifecycle_action_trigger"])
	if err != nil {
		return err
	}
	if err := knownFields(lifecycle, []string{
		"triggering_resource_address", "action_trigger_event", "action_trigger_block_index",
		"actions_list_index", "on_failure",
	}); err != nil {
		return err
	}
	if err := stringFields(lifecycle, "triggering_resource_address", "action_trigger_event", "on_failure"); err != nil {
		return err
	}
	for _, key := range []string{"action_trigger_block_index", "actions_list_index"} {
		if value, exists := lifecycle[key]; exists {
			n, ok := value.(json.Number)
			if !ok {
				return fmt.Errorf("%w: action trigger index must be an integer", ErrMalformed)
			}
			i, err := n.Int64()
			if err != nil || i < 0 {
				return fmt.Errorf("%w: action trigger index must be nonnegative", ErrMalformed)
			}
		}
	}
	invoke, err := mapping(a["invoke_action_trigger"])
	if err != nil {
		return err
	}
	if err := knownFields(invoke, []string{"calling_resource_address"}); err != nil {
		return err
	}
	return stringFields(invoke, "calling_resource_address")
}

func knownFields(m map[string]any, allowed []string) error {
	for key := range m {
		if !slices.Contains(allowed, key) {
			// Even an unknown key can contain a value; do not echo it.
			return fmt.Errorf("%w: payload fields", ErrUnsupported)
		}
	}
	return nil
}

func stringFields(m map[string]any, keys ...string) error {
	for _, key := range keys {
		if value, exists := m[key]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%w: payload metadata must be a string", ErrMalformed)
			}
		}
	}
	return nil
}
