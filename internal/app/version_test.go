package app

import "testing"

func TestUnitVersion(t *testing.T) {
	if got := Version(); got != "infraweave devel (commit unknown, built unknown)" {
		t.Errorf("Version() = %q", got)
	}
	if got := orUnknown("abc"); got != "abc" {
		t.Errorf("orUnknown(abc) = %q", got)
	}
}
