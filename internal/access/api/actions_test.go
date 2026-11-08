package api

import "testing"

// TestUnitActions checks the generated catalogue against the counts report
// 14 section 3.3 gives: 150 values, 18 disabled, 2 deprecated, followed by
// infraweave's 15 account actions, none disabled or deprecated.
func TestUnitActions(t *testing.T) {
	all := Actions()
	disabled, deprecated, own := 0, 0, 0
	for i, a := range all {
		if a.Infraweave {
			own++
			if i < 150 || a.Disabled || a.Deprecated {
				t.Errorf("infraweave's %s at %d: disabled %t, deprecated %t",
					a.Action, i, a.Disabled, a.Deprecated)
			}
		} else if a.Anywhere {
			t.Errorf("the enum's %s is anywhere", a.Action)
		}
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
	if len(all)-own != 150 || disabled != 18 || deprecated != 2 || own != 15 {
		t.Errorf("catalogue holds %d enum values, %d disabled and %d deprecated, and %d "+
			"of infraweave's; want 150, 18, 2 and 15", len(all)-own, disabled, deprecated, own)
	}
	if a, ok := ActionOf(ActionApiKeyRead); !ok || !a.Infraweave || !a.Anywhere ||
		a.Subject != "api_key" {
		t.Errorf("ActionOf(API_KEY_READ) = %+v, %t", a, ok)
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
