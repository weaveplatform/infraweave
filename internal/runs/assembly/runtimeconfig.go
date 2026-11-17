package assembly

import "fmt"

// RuntimeSettings are runs.runtime_config, separate from run's scheduling
// and liveness settings. UnknownKeys is warn or reject, defaulting to warn.
type RuntimeSettings struct {
	RuntimeConfig struct {
		UnknownKeys string `mapstructure:"unknown_keys"`
	} `mapstructure:"runtime_config"`
}

// DefaultRuntimeSettings reports unknown repository configuration keys.
func DefaultRuntimeSettings() RuntimeSettings {
	var s RuntimeSettings
	s.RuntimeConfig.UnknownKeys = "warn"
	return s
}

// Validate refuses a policy that could silently skip the operator's intent.
func (s RuntimeSettings) Validate() error {
	if v := s.RuntimeConfig.UnknownKeys; v != "warn" && v != "reject" {
		return fmt.Errorf("%w: runs.runtime_config.unknown_keys must be warn or reject", ErrConfig)
	}
	return nil
}
