package core

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// CheckSurface supplies the policy compiler to the shared editor registry.
// The editor authorizes an account-authenticated caller; compiling submitted
// text reads no stored policy, space, sample or receipt.
func (s *Service) CheckSurface() expr.Surface {
	return expr.Surface{Name: "policy", CheckPolicy: func(ctx context.Context, p kernel.Principal, in expr.PolicySource) ([]kernel.FieldError, error) {
		return s.Check(ctx, p, rego.Policy{Type: rego.Type(in.Type), Engine: rego.EngineType(in.Engine), Body: in.Body})
	}}
}

// Check uses save-time compilation and diagnostics without any persistence.
func (s *Service) Check(ctx context.Context, _ kernel.Principal, in rego.Policy) ([]kernel.FieldError, error) {
	if s.deps.Runtime == nil {
		return nil, kernel.ErrUnavailable
	}
	if in.Engine == "" {
		in.Engine = rego.V1
	}
	err := s.deps.Runtime.Check(ctx, in)
	if err == nil {
		return []kernel.FieldError{}, nil
	}
	err = s.compileError(err, in.Body)
	var invalid *kernel.Invalid
	if errors.As(err, &invalid) {
		return invalid.Errors, nil
	}
	return nil, err
}
