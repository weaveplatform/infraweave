package sqltest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeTB struct{ skipped, failed string }

func (f *fakeTB) Helper()                           {}
func (f *fakeTB) Skipf(format string, args ...any)  { f.skipped = fmt.Sprintf(format, args...) }
func (f *fakeTB) Fatalf(format string, args ...any) { f.failed = fmt.Sprintf(format, args...) }

func TestUnitDockerRule(t *testing.T) {
	noDocker := fmt.Errorf("%w: socket missing", errNoDocker)
	for _, tc := range []struct {
		name         string
		err          error
		require      bool
		skips, fails bool
	}{
		{"no Docker locally", noDocker, false, true, false},
		{"no Docker in CI", noDocker, true, false, true},
		{"a container that fails", errors.New("pull failed"), false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTB{}
			unavailable(f, tc.err, tc.require)
			if (f.skipped != "") != tc.skips || (f.failed != "") != tc.fails {
				t.Errorf("skipped %q, failed %q", f.skipped, f.failed)
			}
			if tc.skips && !strings.Contains(f.skipped, RequireDocker) {
				t.Errorf("the skip does not name %s: %q", RequireDocker, f.skipped)
			}
		})
	}
}

func TestUnitWithDatabase(t *testing.T) {
	got, err := withDatabase(
		"postgres://u:p@127.0.0.1:5432/infraweave?sslmode=disable",
		"iw_test_1",
	)
	if err != nil || got != "postgres://u:p@127.0.0.1:5432/iw_test_1?sslmode=disable" {
		t.Errorf("withDatabase = %q, %v", got, err)
	}
	if _, err := withDatabase("postgres://u:p@[::1/x", "y"); err == nil {
		t.Error("an unparsable connection string was accepted")
	}
}
