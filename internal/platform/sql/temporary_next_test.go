package sql_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccTemporaryNextOnly fails on the next major only, to show that
// postgres-next reports without blocking. It is reverted before merge.
func TestAccTemporaryNextOnly(t *testing.T) {
	var v int
	if err := sqltest.Open(t).Primary().QueryRow(t.Context(),
		"SELECT current_setting('server_version_num')::int").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v >= 190000 {
		t.Fatalf("temporary: server_version_num %d is the next major", v)
	}
}
