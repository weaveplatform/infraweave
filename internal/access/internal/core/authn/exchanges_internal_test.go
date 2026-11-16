package authn

import (
	"strconv"
	"testing"
)

// TestUnitExchangesBound checks that the kept exchanges start again empty
// once they hold maxExchanges.
func TestUnitExchangesBound(t *testing.T) {
	e := NewExchanges(nil)
	for i := range maxExchanges {
		e.put(strconv.Itoa(i), exchanged{})
	}
	e.put("last", exchanged{})
	if len(e.kept) != 1 {
		t.Errorf("past the bound the exchanges keep %d", len(e.kept))
	}
}
