package api

import "testing"

func TestUnitAuthorizationSchemeNames(t *testing.T) {
	for value, want := range map[AuthorizationScheme]bool{ManagedUsers: true, LoginPolicies: true, "": false, "UNKNOWN": false} {
		if value.Valid() != want {
			t.Errorf("scheme %q validity", value)
		}
	}
}
