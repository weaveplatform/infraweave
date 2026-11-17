package config

import (
	"strings"
	"testing"
)

func TestUnitRuntimeConfigUnknownKeys(t *testing.T) {
	if got := Defaults().Runs.RuntimeConfig.UnknownKeys; got != "warn" {
		t.Fatalf("default unknown keys = %q", got)
	}
	t.Setenv("INFRAWEAVE_RUNS_RUNTIME_CONFIG_UNKNOWN_KEYS", "reject")
	c, err := loadArgs(t)
	if err != nil || c.Runs.RuntimeConfig.UnknownKeys != "reject" {
		t.Fatalf("plural setting: %+v, %v", c, err)
	}
	t.Setenv("INFRAWEAVE_RUNS_RUNTIME_CONFIG_UNKNOWN_KEYS", "ignore")
	if _, err := loadArgs(t); err == nil || !strings.Contains(err.Error(), "runs.runtime_config.unknown_keys") {
		t.Fatalf("invalid unknown-key policy: %v", err)
	}
}
