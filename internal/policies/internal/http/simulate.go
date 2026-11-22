package http

import (
	"context"
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// PolicySimulatev2 returns the recorded decision-string projection. The shared
// listener authorizes space visibility; the supplied source/input reveal no
// stored policy or capture and cannot grant access or create work.
func (s Server) PolicySimulatev2(ctx context.Context, req api.PolicySimulatev2RequestObject) (api.PolicySimulatev2ResponseObject, error) {
	if _, err := principal(ctx); err != nil {
		return nil, err
	}
	in := req.Body
	if in.Libraries != nil && len(in.Libraries.Libraries) != 0 {
		return nil, &kernel.Invalid{
			Detail: "Policy libraries are not supported by this runtime.",
			Errors: []kernel.FieldError{{Pointer: "/libraries", Reason: kernel.ReasonRule, Message: "Remove libraries before simulating this policy."}},
		}
	}
	policy := rego.Policy{Type: rego.Type(in.Type), Body: in.Body}
	markDeprecated(ctx, policy.Type)
	if in.EngineType != nil {
		policy.Engine = rego.EngineType(*in.EngineType)
	}
	decision, err := s.svc.Simulate(ctx, rego.Request{Policy: policy, Input: json.RawMessage(in.Input)})
	if err != nil {
		return nil, policyError(err)
	}
	return api.PolicySimulatev2200JSONResponse{Body: string(decision)}, nil
}
