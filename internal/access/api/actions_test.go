package api

import "testing"

// TestUnitActions checks the generated catalogue against the counts report
// 14 section 3.3 gives: 150 values, 18 disabled, 2 deprecated.
func TestUnitActions(t *testing.T) {
	all := Actions()
	disabled, deprecated := 0, 0
	for _, a := range all {
		if a.Disabled {
			disabled++
		}
		if a.Deprecated {
			deprecated++
		}
		if a.Assignable() == (a.Disabled || a.Deprecated) {
			t.Errorf("%s: Assignable = %t", a.Action, a.Assignable())
		}
	}
	if len(all) != 150 || disabled != 18 || deprecated != 2 {
		t.Errorf("catalogue holds %d values, %d disabled and %d deprecated; want 150, 18 and 2",
			len(all), disabled, deprecated)
	}
	all[0].Subject = "changed"
	if Actions()[0].Subject == "changed" {
		t.Error("Actions shares the catalogue")
	}
	if a, ok := ActionOf(ActionSpaceRead); !ok || a.Subject != "space" ||
		a.Fallback != FallbackReader {
		t.Errorf("ActionOf(SPACE_READ) = %+v, %t", a, ok)
	}
	for _, a := range []Action{
		ActionAccountAdmin, ActionOrganizationAdmin, ActionAuthenticated,
	} {
		if _, ok := ActionOf(a); ok {
			t.Errorf("pseudo-action %s is in the catalogue", a)
		}
	}
}

func TestUnitFallbackWithin(t *testing.T) {
	r, w, a := FallbackReader, FallbackWriter, FallbackAdmin
	for _, tc := range []struct {
		f, level Fallback
		want     bool
	}{
		{r, r, true},
		{r, w, true},
		{r, a, true},
		{w, r, false},
		{w, w, true},
		{w, a, true},
		{a, r, false},
		{a, w, false},
		{a, a, true},
		{"owner", a, false},
		{r, "owner", false},
	} {
		if got := tc.f.Within(tc.level); got != tc.want {
			t.Errorf("%s.Within(%s) = %t, want %t", tc.f, tc.level, got, tc.want)
		}
	}
}
