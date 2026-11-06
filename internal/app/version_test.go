package app

import (
	"crypto/fips140"
	"testing"
)

func TestUnitVersion(t *testing.T) {
	if got := Version(); got != "infraweave devel (commit unknown, built unknown)" {
		t.Errorf("Version() = %q", got)
	}
	if got := orUnknown("abc"); got != "abc" {
		t.Errorf("orUnknown(abc) = %q", got)
	}
}

// TestUnitFIPS checks the FIPS line for each mode, and that FIPS reports
// the mode this test binary runs in, which the fips-only job sets to only.
func TestUnitFIPS(t *testing.T) {
	for _, tc := range []struct {
		enabled, enforced bool
		module, want      string
	}{
		{false, false, "latest", "FIPS 140-3 mode off, Go Cryptographic Module latest"},
		{true, false, "v1.0.0", "FIPS 140-3 mode on, Go Cryptographic Module v1.0.0"},
		{true, true, "v1.0.0", "FIPS 140-3 mode only, Go Cryptographic Module v1.0.0"},
	} {
		if got := fipsLine(tc.enabled, tc.enforced, tc.module); got != tc.want {
			t.Errorf("fipsLine(%v, %v, %q) = %q", tc.enabled, tc.enforced, tc.module, got)
		}
	}
	if got, want := FIPS(), fipsLine(
		fips140.Enabled(),
		fips140.Enforced(),
		fips140.Version(),
	); got != want {
		t.Errorf("FIPS() = %q, want %q", got, want)
	}
}
