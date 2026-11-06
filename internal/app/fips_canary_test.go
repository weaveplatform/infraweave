package app

import (
	"crypto/md5" //nolint:gosec // a temporary canary that FIPS 140-only mode must fail
	"testing"
)

func TestUnitFIPSCanary(t *testing.T) {
	t.Parallel()
	_ = md5.Sum([]byte("infraweave")) //nolint:gosec // see the import
}
