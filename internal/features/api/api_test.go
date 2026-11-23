package api

import "testing"

func TestUnitRefusal(t *testing.T) {
	for _, tc := range []struct {
		refusal Refusal
		want    string
	}{
		{Refusal{Feature: "multiple-vcs-integrations"}, "features: multiple-vcs-integrations is not in the organization's tier"},
		{Refusal{Feature: "multiple-vcs-integrations", Disabled: true}, "features: multiple-vcs-integrations is switched off"},
		{Refusal{Feature: "multiple-vcs-integrations", Preview: true}, "features: multiple-vcs-integrations is a preview the account has not opted in to"},
	} {
		if got := tc.refusal.Error(); got != tc.want {
			t.Errorf("Error = %q, want %q", got, tc.want)
		}
	}
}

func TestUnitLimitError(t *testing.T) {
	for _, scope := range []Scope{ScopeOrganization, ScopeAccount} {
		e := &LimitError{Limit: Seats, Scope: scope, Value: 2}
		want := "features: seats reached its " + string(scope) + " limit of 2"
		if e.Error() != want {
			t.Errorf("Error = %q, want %q", e.Error(), want)
		}
	}
}
