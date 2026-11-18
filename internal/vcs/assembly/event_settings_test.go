package assembly

import (
	"errors"
	"strings"
	"testing"
)

func TestUnitEventRunsCeiling(t *testing.T) {
	settings := DefaultSettings()
	if settings.EventRunsCeiling != 500 || settings.Validate() != nil {
		t.Fatal("default event ceiling is not valid500")
	}
	for _, ceiling := range []int{0, -1} {
		settings.EventRunsCeiling = ceiling
		err := settings.Validate()
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "vcs.event_runs_ceiling") {
			t.Fatal("invalid event ceiling accepted", ceiling, err)
		}
	}
	settings.EventRunsCeiling = 1
	if err := settings.Validate(); err != nil {
		t.Fatal("minimum valid event ceiling refused", err)
	}
}
