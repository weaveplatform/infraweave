package policytest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/policies/policytest"
)

func TestUnitPlanVendor(t *testing.T) {
	raw, err := policytest.PlanVendor("terraform", strings.NewReader(`{"format_version":"1.2","resource_changes":null}`))
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"resource_changes":null`) {
		t.Fatalf("projection: %s, %v", raw, err)
	}
	if _, err := policytest.PlanVendor("terraform", strings.NewReader(`{}`)); err == nil {
		t.Fatal("invalid plan projected")
	}
}
