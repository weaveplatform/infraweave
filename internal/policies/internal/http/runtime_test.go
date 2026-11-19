package http

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

func TestUnitLinkedRuntime(t *testing.T) {
	for _, tc := range []struct {
		version func() (string, error)
		want    string
		failed  bool
	}{
		{func() (string, error) { return "v1.22.4", nil }, "v1.22.4", false},
		{func() (string, error) { return "", rego.ErrVersion }, "", true},
	} {
		out, err := (Server{version: tc.version}).PolicyRuntime(t.Context(), api.PolicyRuntimeRequestObject{})
		if tc.failed {
			if !errors.Is(err, rego.ErrVersion) || out != nil {
				t.Fatalf("version fault: %T %v", out, err)
			}
			continue
		}
		got, ok := out.(api.PolicyRuntime200JSONResponse)
		if !ok || err != nil || got.Body.OpenPolicyAgentVersion != tc.want {
			t.Fatalf("version = %T %v", out, err)
		}
	}
	// The zero partial uses actual build information and cannot panic when an
	// application test constructs only selected domains.
	expected, expectedErr := rego.Version()
	out, err := (Server{}).PolicyRuntime(t.Context(), api.PolicyRuntimeRequestObject{})
	if expectedErr != nil {
		if !errors.Is(err, expectedErr) {
			t.Fatalf("metadata fault: %v", err)
		}
	} else if got := out.(api.PolicyRuntime200JSONResponse).Body.OpenPolicyAgentVersion; got != expected {
		t.Fatal(got)
	}
}
