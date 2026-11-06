package objectstoretest_test

import (
	"maps"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
)

// TestUnitObjectstoreGaps checks that every skipped case exists and says why, and
// that a case is either an emulator's gap, which the cloud job runs, or
// something the service cannot bind, never both.
func TestUnitObjectstoreGaps(t *testing.T) {
	cases := map[string]bool{}
	for _, c := range objectstoretest.Cases() {
		cases[c.Name] = true
	}
	drivers := map[string]bool{
		objectstore.DriverS3: true, objectstore.DriverGCS: true,
		objectstore.DriverAzureBlob: true, objectstore.DriverFS: true,
	}
	for kind, m := range map[string]map[string]map[string]string{
		"Gaps": objectstoretest.Gaps, "Unbound": objectstoretest.Unbound,
	} {
		for driver, skips := range m {
			if !drivers[driver] {
				t.Errorf("%s names driver %q", kind, driver)
			}
			for name, why := range skips {
				if !cases[name] || why == "" {
					t.Errorf("%s[%s] skips %q because %q", kind, driver, name, why)
				}
				if _, both := objectstoretest.Gaps[driver][name]; both && kind == "Unbound" {
					t.Errorf("%s: %q is both a gap and unbound", driver, name)
				}
			}
		}
	}
	for driver := range drivers {
		want := maps.Clone(objectstoretest.Gaps[driver])
		if want == nil {
			want = map[string]string{}
		}
		maps.Copy(want, objectstoretest.Unbound[driver])
		if got := objectstoretest.Skip(driver); !maps.Equal(got, want) {
			t.Errorf("Skip(%s) = %v, want %v", driver, got, want)
		}
	}
}
