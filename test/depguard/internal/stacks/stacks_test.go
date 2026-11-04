package stacks

import (
	"testing"
	"time"
)

// TestWait breaks the time.Sleep ban.
func TestWait(t *testing.T) {
	time.Sleep(time.Millisecond)
}
