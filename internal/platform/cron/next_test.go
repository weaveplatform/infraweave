package cron

import (
	"testing"
	"time"
)

func TestUnitNextGivesUp(t *testing.T) {
	// A Schedule that matches no day can only be made in the package, since
	// Parse rejects one.
	if got := (Schedule{}).Next(time.Now(), time.UTC); !got.IsZero() {
		t.Errorf("Next = %s, want the zero Time", got)
	}
}
