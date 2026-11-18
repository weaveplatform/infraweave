package config

import (
	"strings"
	"testing"
	"time"
)

func TestUnitWebhookSettings(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil || c.Policy.Timeouts.PushEvent != 10*time.Minute || c.VCS.Deliveries.Retention != 7*24*time.Hour {
		t.Fatalf("defaults=%+v,%v", c, err)
	}
	file := writeFile(t, "policy:\n  timeouts:\n    push_event: 20m\nvcs:\n  deliveries:\n    retention: 48h\n")
	c, err = loadArgs(t, "--config", file)
	if err != nil || c.Policy.Timeouts.PushEvent != 20*time.Minute || c.VCS.Deliveries.Retention != 48*time.Hour {
		t.Fatalf("settings=%+v,%v", c, err)
	}
	t.Run("environment", func(t *testing.T) {
		t.Setenv("INFRAWEAVE_POLICY_TIMEOUTS_PUSH_EVENT", "30m")
		t.Setenv("INFRAWEAVE_VCS_DELIVERIES_RETENTION", "72h")
		c, err := loadArgs(t, "--config", file)
		if err != nil || c.Policy.Timeouts.PushEvent != 30*time.Minute || c.VCS.Deliveries.Retention != 72*time.Hour {
			t.Fatal("environment precedence", c, err)
		}
	})
	for _, body := range []string{"policy:\n  timeouts:\n    push_event: 0s\n", "vcs:\n  deliveries:\n    retention: -1h\n", "policy:\n  timeouts:\n    unknown: 1h\n"} {
		if _, err := loadArgs(t, "--config", writeFile(t, body)); err == nil || !strings.Contains(err.Error(), "policy") && !strings.Contains(err.Error(), "vcs") {
			t.Fatalf("invalid setting accepted: %s %v", body, err)
		}
	}
}
