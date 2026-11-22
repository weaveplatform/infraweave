package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

const (
	simulationBodyBytes  = 256 << 10
	simulationInputBytes = 4 << 20
)

// Simulate evaluates caller-supplied source and input without entering the live
// evaluator's selection, evidence or workflow paths. Its result is the recorded
// full decision, not permission to apply it to an account or run.
func (s *Service) Simulate(ctx context.Context, in rego.Request) (json.RawMessage, error) {
	if s.deps.Runtime == nil {
		return nil, kernel.ErrUnavailable
	}
	if len(in.Policy.Body) > simulationBodyBytes {
		return nil, simulationInvalid("/body", "Policy source exceeds 256 KiB.")
	}
	if len(in.Input) > simulationInputBytes {
		return nil, simulationInvalid("/input", "Edited policy input exceeds 4 MiB.")
	}
	if in.Policy.Engine == "" {
		in.Policy.Engine = rego.V1
	}
	if err := s.deps.Runtime.Check(ctx, in.Policy); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, simulationInvalid("/body", executionFailure(err).Message)
		}
		return nil, s.compileError(err, in.Policy.Body)
	}
	in.Simulation = true
	result, err := s.deps.Runtime.Evaluate(ctx, in)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("policies: cancel simulation: %w", err)
		}
		failure := executionFailure(err)
		pointer := "/body"
		if errors.Is(err, rego.ErrInput) || errors.Is(err, rego.ErrLimit) {
			pointer = "/input"
		}
		return nil, simulationInvalid(pointer, failure.Message)
	}
	return result.Decision, nil
}

func simulationInvalid(pointer, message string) error {
	return &kernel.Invalid{Detail: "The policy simulation was refused.", Errors: []kernel.FieldError{{Pointer: pointer, Reason: kernel.ReasonRule, Message: message}}}
}
