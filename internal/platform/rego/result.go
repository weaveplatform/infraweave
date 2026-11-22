package rego

import "fmt"

func boundDecision(kind Type, value map[string]any) error {
	return boundDecisionRedacted(kind, value, nil)
}

func boundDecisionRedacted(kind Type, value map[string]any, redact func(string) string) error {
	known := rules(kind)
	if len(value) != len(known) {
		return ErrResult
	}
	for name, k := range known {
		v, exists := value[name]
		if !exists {
			return fmt.Errorf("%w: missing rule %s", ErrResult, name)
		}
		valid := false
		switch k {
		case booleanRule:
			_, valid = v.(bool)
		case stringRule:
			_, valid = v.(string)
		case stringSet, messageSet, objectSet:
			entries, ok := v.([]any)
			if !ok {
				break
			}
			valid = true
			for i, entry := range entries {
				if k == objectSet {
					if _, ok := entry.(map[string]any); !ok {
						valid = false
						break
					}
					continue
				}
				text, ok := entry.(string)
				if !ok {
					valid = false
					break
				}
				if k == messageSet {
					if redact != nil {
						text = redact(text)
					}
					entries[i] = truncate(text, MessageBytes)
				}
			}
			if k == messageSet || k == objectSet {
				value[name] = entries[:min(len(entries), MessageCount)]
			}
		case rolesRule:
			roles, ok := v.(map[string]any)
			if !ok {
				break
			}
			valid = true
			for _, grants := range roles {
				switch grants := grants.(type) {
				case []any:
					for _, role := range grants {
						if _, ok := role.(string); !ok {
							valid = false
						}
					}
				case map[string]any:
					for _, granted := range grants {
						if _, ok := granted.(bool); !ok {
							valid = false
						}
					}
				default:
					valid = false
				}
			}
		}
		if !valid {
			return fmt.Errorf("%w: rule %s has an invalid JSON type", ErrResult, name)
		}
	}
	return nil
}
