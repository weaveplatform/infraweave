package api

import "testing"

// TestUnitClientAddr checks that a context carries the address
// WithClientAddr put on it, and that one without answers none.
func TestUnitClientAddr(t *testing.T) {
	if got := ClientAddr(t.Context()); got != "" {
		t.Errorf("a bare context carries %q", got)
	}
	if got := ClientAddr(WithClientAddr(t.Context(), "192.0.2.1")); got != "192.0.2.1" {
		t.Errorf("ClientAddr = %q, want 192.0.2.1", got)
	}
}
