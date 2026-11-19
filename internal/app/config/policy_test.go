package config

import (
	"strings"
	"testing"
	"time"
)

func TestUnitPolicyBudgets(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Timeouts.Plan != 45*time.Second || c.Policy.Timeouts.Push != 10*time.Second || c.Policy.Timeouts.Login != 5*time.Second ||
		c.Policy.Timeouts.Combined != 5*time.Minute ||
		c.Policy.MaxInputBytes != 64<<20 ||
		c.Policy.MaxBodyBytes != 256<<10 ||
		c.Policy.MaxSimulationBytes != 4<<20 {
		t.Fatalf("policy defaults: %+v", c.Policy)
	}
	file := writeFile(t, "policy:\n  timeouts:\n    plan: 1s\n    approval: 2s\n    push_event: 3m\n  max_input_bytes: 1024\n  max_body_bytes: 2048\n  max_simulation_bytes: 512\n")
	c, err = loadArgs(t, "--config", file)
	if err != nil || c.Policy.Timeouts.Plan != time.Second || c.Policy.Timeouts.Approval != 2*time.Second || c.Policy.Timeouts.PushEvent != 3*time.Minute ||
		c.Policy.MaxBodyBytes != 2048 {
		t.Fatalf("strict policy YAML: %+v %v", c.Policy, err)
	}
	for _, body := range []string{
		"policy:\n  unknown: true\n", "policy:\n  timeouts:\n    plan: 0s\n",
		"policy:\n  max_input_bytes: 0\n", "policy:\n  max_body_bytes: -1\n",
		"policy:\n  max_simulation_bytes: -1\n", "policy:\n  timeouts:\n    notification: invalid\n",
	} {
		if _, err := loadArgs(t, "--config", writeFile(t, body)); err == nil || !strings.Contains(err.Error(), "policy") {
			t.Errorf("invalid policy YAML accepted: %q: %v", body, err)
		}
	}
}
