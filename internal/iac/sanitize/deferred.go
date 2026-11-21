package sanitize

import (
	"encoding/json"
	"fmt"
)

// The experimental producer emits the action wrapper with identity/trigger
// metadata and no value members. If values are present, the same validated
// action contract owns them; the wrapper never bypasses its masks.
func deferredAction(d map[string]any, op *operation) error {
	if err := knownFields(d, []string{"reason", "action_invocation"}); err != nil {
		return err
	}
	if _, ok := d["reason"].(string); !ok {
		return fmt.Errorf("%w: deferred reason must be a string", ErrMalformed)
	}
	a, err := mapping(d["action_invocation"])
	if err != nil || a == nil {
		return fmt.Errorf("%w: deferred action must be an object", ErrMalformed)
	}
	return action(a, op)
}

// Deferred changes use the same value and marker paths as ordinary changes.
// Their reason and wildcard instance identity remain structural. Supporting
// this artifact shape does not enable experimental CLI versions or execution.
func deferredResource(d map[string]any, op *operation) error {
	if err := knownFields(d, []string{"reason", "resource_change"}); err != nil {
		return err
	}
	if _, ok := d["reason"].(string); !ok {
		return fmt.Errorf("%w: deferred reason must be a string", ErrMalformed)
	}
	r, err := mapping(d["resource_change"])
	if err != nil || r == nil {
		return fmt.Errorf("%w: deferred resource must be an object", ErrMalformed)
	}
	if err := knownFields(r, []string{
		"address", "previous_address", "module_address", "mode", "type", "name", "index",
		"index_unknown", "provider_name", "deposed", "change", "action_reason",
	}); err != nil {
		return err
	}
	if err := stringFields(r, "address", "previous_address", "module_address", "mode", "type", "name", "provider_name", "deposed", "action_reason"); err != nil {
		return err
	}
	if err := booleanField(r, "index_unknown"); err != nil {
		return err
	}
	if index := r["index"]; index != nil && !pathStep(index) {
		return fmt.Errorf("%w: deferred index must be a string or integer", ErrMalformed)
	}
	c, err := mapping(r["change"])
	if err != nil || c == nil {
		return fmt.Errorf("%w: deferred change must be an object", ErrMalformed)
	}
	if err := knownFields(c, []string{
		"actions", "before", "after", "after_unknown", "before_sensitive", "after_sensitive",
		"replace_paths", "importing", "generated_config", "before_identity", "after_identity",
	}); err != nil {
		return err
	}
	if _, err := marked(c["after"], c["after_unknown"], nil); err != nil {
		return err
	}
	if err := deferredMetadata(c); err != nil {
		return err
	}
	return change(c, op)
}

func deferredMetadata(c map[string]any) error {
	if actions := c["actions"]; actions != nil {
		list, ok := actions.([]any)
		if !ok {
			return fmt.Errorf("%w: deferred actions must be strings", ErrMalformed)
		}
		for _, action := range list {
			if _, ok := action.(string); !ok {
				return fmt.Errorf("%w: deferred action must be a string", ErrMalformed)
			}
		}
	}
	if paths := c["replace_paths"]; paths != nil {
		list, ok := paths.([]any)
		if !ok {
			return fmt.Errorf("%w: replacement paths must be arrays", ErrMalformed)
		}
		for _, path := range list {
			steps, ok := path.([]any)
			if !ok {
				return fmt.Errorf("%w: replacement path must be an array", ErrMalformed)
			}
			for _, step := range steps {
				if !pathStep(step) {
					return fmt.Errorf("%w: replacement step must be a string or integer", ErrMalformed)
				}
			}
		}
	}
	importing, err := mapping(c["importing"])
	if err != nil {
		return err
	}
	if err := knownFields(importing, []string{"id", "unknown", "identity"}); err != nil {
		return err
	}
	if err := booleanField(importing, "unknown"); err != nil {
		return err
	}
	if err := stringFields(importing, "id"); err != nil {
		return err
	}
	for _, v := range []any{c["before_identity"], c["after_identity"], importing["identity"]} {
		if _, err := mapping(v); err != nil {
			return err
		}
	}
	if generated := c["generated_config"]; generated != nil {
		if _, ok := generated.(string); !ok {
			return fmt.Errorf("%w: generated configuration must be a string", ErrMalformed)
		}
	}
	return nil
}

func pathStep(value any) bool {
	switch v := value.(type) {
	case string:
		return true
	case json.Number:
		_, err := v.Int64()
		return err == nil
	}
	return false
}
