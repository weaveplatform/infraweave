package kektest

import "testing"

// TestUnitReplayUnversioned checks that a request matches its recording
// at any well-formed api-version, and that the rest of its path and query
// still has to match exactly.
func TestUnitReplayUnversioned(t *testing.T) {
	const wrap = "/keys/kek/wrapkey"
	recorded, ok := unversioned(wrap + "?api-version=2025-07-01")
	if !ok {
		t.Fatal("the recorded version is refused")
	}
	for _, uri := range []string{
		wrap + "?api-version=2026-04-01",
		wrap + "?api-version=2026-10-01-preview",
		wrap + "?api-version=7.6",
		wrap + "?api%2Dversion=2026%2D04%2D01",
	} {
		if got, ok := unversioned(uri); !ok || got != recorded {
			t.Errorf("%s: %q, %v; want %q", uri, got, ok, recorded)
		}
	}
	for _, uri := range []string{
		wrap,
		"/keys/other/wrapkey?api-version=2026-04-01",
		wrap + "?api-version=2026-04-01&maxresults=1",
		wrap + "?maxresults=1&api-version=2026-04-01",
	} {
		if got, ok := unversioned(uri); !ok || got == recorded {
			t.Errorf("%s: %q, %v matches the recording", uri, got, ok)
		}
	}
	for _, uri := range []string{
		wrap + "?api-version=latest",
		wrap + "?api-version=",
		wrap + "?api-version=%zz",
		wrap + "?api-version=2026-04-01&api-version=2025-07-01",
	} {
		if got, ok := unversioned(uri); ok {
			t.Errorf("%s: %q, want refused", uri, got)
		}
	}
	const gcp = "/v1/k:encrypt?%24alt=json%3Benum-encoding%3Dint"
	if got, ok := unversioned(gcp); !ok || got != gcp {
		t.Errorf("%s: %q, %v; want it unchanged", gcp, got, ok)
	}
}
