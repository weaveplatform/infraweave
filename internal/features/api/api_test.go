package api

import "testing"

func TestUnitLimitError(t *testing.T) {
	for _, scope := range []Scope{ScopeOrganization, ScopeAccount} {
		e := &LimitError{Limit: Seats, Scope: scope, Value: 2}
		want := "features: seats reached its " + string(scope) + " limit of 2"
		if e.Error() != want {
			t.Errorf("Error = %q, want %q", e.Error(), want)
		}
	}
}
