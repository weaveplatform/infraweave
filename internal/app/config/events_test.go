package config

import (
	"strings"
	"testing"
	"time"
)

func TestUnitEventDeliverySettings(t *testing.T) {
	c, err := loadArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.Events.Retention != 72*time.Hour || c.Events.Deliveries.Retention != 7*24*time.Hour {
		t.Fatalf("defaults %+v", c.Events)
	}
	file := writeFile(t, "events:\n  retention: 24h\n  deliveries:\n    retention: 120h\n")
	t.Setenv("INFRAWEAVE_EVENTS_DELIVERIES_RETENTION", "144h")
	c, err = loadArgs(t, "--config", file)
	if err != nil || c.Events.Retention != 24*time.Hour || c.Events.Deliveries.Retention != 144*time.Hour {
		t.Fatalf("load = %+v, %v", c, err)
	}
}

func TestUnitEventDeliverySettingsRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key": "events:\n  deliveries:\n    retentoin: 168h\n",
		"nonpositive": "events:\n  deliveries:\n    retention: 0s\n",
		"equal":       "events:\n  deliveries:\n    retention: 72h\n",
		"shorter":     "events:\n  deliveries:\n    retention: 48h\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadArgs(t, "--config", writeFile(t, body))
			if err == nil || !strings.Contains(err.Error(), "deliveries") {
				t.Fatalf("load = %v", err)
			}
		})
	}
}
