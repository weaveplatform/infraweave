package http

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// PolicyRuntime reports the library linked into this process, without querying
// a service or advertising the dated version observed in a reference account.
func (s Server) PolicyRuntime(_ context.Context, _ api.PolicyRuntimeRequestObject) (api.PolicyRuntimeResponseObject, error) {
	version := s.version
	if version == nil {
		version = rego.Version
	}
	linked, err := version()
	if err != nil {
		return nil, fmt.Errorf("read linked policy runtime: %w", err)
	}
	return api.PolicyRuntime200JSONResponse{Body: api.PolicyRuntimeDetails{OpenPolicyAgentVersion: linked}}, nil
}
