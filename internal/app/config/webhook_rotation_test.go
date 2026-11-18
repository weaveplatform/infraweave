package config

import (
	"strings"
	"testing"
	"time"
)

func TestUnitWebhookSecretOverlap(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil || c.VCS.WebhookSecretOverlap != 24*time.Hour {
		t.Fatalf("rotation default: %v %v", c, err)
	}
	file := writeFile(t, "vcs:\n  webhook_secret_overlap: 2h\n")
	c, err = loadArgs(t, "--config", file)
	if err != nil || c.VCS.WebhookSecretOverlap != 2*time.Hour {
		t.Fatalf("rotation YAML: %v %v", c, err)
	}
	t.Run("environment", func(t *testing.T) {
		t.Setenv("INFRAWEAVE_VCS_WEBHOOK_SECRET_OVERLAP", "90m")
		c, err := loadArgs(t, "--config", file)
		if err != nil || c.VCS.WebhookSecretOverlap != 90*time.Minute {
			t.Fatalf("rotation environment: %v %v", c, err)
		}
	})
	for _, value := range []string{"0s", "-1h", "not-a-duration"} {
		_, err := loadArgs(t, "--config", writeFile(t, "vcs:\n  webhook_secret_overlap: "+value+"\n"))
		if err == nil || !strings.Contains(err.Error(), "webhook_secret_overlap") {
			t.Fatalf("invalid rotation window %s: %v", value, err)
		}
	}
}
