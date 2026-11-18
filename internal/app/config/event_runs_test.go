package config

import (
	"strings"
	"testing"
)

func TestUnitVCSEventRunsCeiling(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil || c.VCS.EventRunsCeiling != 500 {
		t.Fatalf("event ceiling default: %v %v", c, err)
	}
	file := writeFile(t, "vcs:\n  event_runs_ceiling: 2\n")
	c, err = loadArgs(t, "--config", file)
	if err != nil || c.VCS.EventRunsCeiling != 2 {
		t.Fatalf("event ceiling YAML: %v %v", c, err)
	}
	t.Run("environment", func(t *testing.T) {
		t.Setenv("INFRAWEAVE_VCS_EVENT_RUNS_CEILING", "3")
		c, err := loadArgs(t, "--config", file)
		if err != nil || c.VCS.EventRunsCeiling != 3 {
			t.Fatalf("event ceiling environment: %v %v", c, err)
		}
	})
	for _, value := range []string{"0", "-1", "not-a-number"} {
		_, err := loadArgs(t, "--config", writeFile(t, "vcs:\n  event_runs_ceiling: "+value+"\n"))
		if err == nil || !strings.Contains(err.Error(), "event_runs_ceiling") {
			t.Fatalf("invalid event ceiling %s: %v", value, err)
		}
	}
}
