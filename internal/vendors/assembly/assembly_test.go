package assembly

import "testing"

// TestUnitDomain checks that the domain's partial serves no operation yet.
func TestUnitDomain(t *testing.T) {
	if New().API() != (VendorsAPI{}) {
		t.Error("API serves an operation")
	}
}
