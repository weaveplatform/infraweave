package core

import (
	"context"
	"encoding/json"
	"fmt"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

var _ policyapi.TaskInputBuilder = (*Service)(nil)

// BuildTaskInput uses the published TASK models without creating run facts.
func (*Service) BuildTaskInput(ctx context.Context, facts policyapi.TaskInputFacts) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("build task policy input: %w", err)
	}
	if facts.Account.IsZero() || facts.At.IsZero() || facts.Stack.ID.IsZero() || facts.Command == "" {
		return nil, policyapi.ErrInput
	}
	b := &inputBuilder{}
	document := inputs.TaskInput{
		Request: inputs.Value(inputs.TaskInputRequest{
			Command: inputs.Value(facts.Command), TimestampNS: inputs.Value(facts.At.UnixNano()), RemoteIP: fact(b, facts.RemoteIP),
		}),
		Session: inputs.Value(b.session(facts.Session)), Stack: inputs.Value(b.stack(facts.Stack)),
	}
	if b.invalid {
		return nil, policyapi.ErrInput
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode task policy input: %w", policyapi.ErrInput)
	}
	if err := inputs.Validate(string(policyapi.Task), raw); err != nil {
		return nil, policyapi.ErrInput
	}
	return raw, nil
}
