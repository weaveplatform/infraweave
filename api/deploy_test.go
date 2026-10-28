package api_test

import (
	"bytes"
	"testing"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/openapibundle"
)

// TestUnitDeployBundleCurrent fails when a fragment under src/deploy changes
// without `make bundle`, so the committed and embedded document never drifts
// from what the fragments say.
func TestUnitDeployBundleCurrent(t *testing.T) {
	want, err := openapibundle.Bundle("src/deploy")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(api.Deploy, want) {
		t.Error("api/deploy.openapi.yaml is stale; run make bundle and commit the result")
	}
}
