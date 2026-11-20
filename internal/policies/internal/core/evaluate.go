package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/combine"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// Evaluate joins the caller's transition. Engine failures become fail-closed
// decision evidence; storage failures roll back the entire transition.
func (s *Service) Evaluate(ctx context.Context, req policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	if err := checkEvaluation(req); err != nil {
		return policyapi.Evaluation{}, err
	}
	var out policyapi.Evaluation
	err := tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		rows, space, err := s.selectPolicies(ctx, req)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			out = combineDecisions(req.Type, nil)
			return nil
		}
		input := req.Input
		var inputFailure *policyapi.Failure
		if req.BuildInput != nil {
			input, err = req.BuildInput(ctx)
			if err != nil {
				if !errors.Is(err, policyapi.ErrInput) && !errors.Is(err, inputs.ErrInput) {
					return fmt.Errorf("policies: build evaluation input: %w", err)
				}
				inputFailure = &policyapi.Failure{Code: "INPUT", Message: "Policy input facts are unavailable or invalid."}
			}
		}
		limit := int64(inputs.MaxDocumentBytes)
		if s.deps.Runtime != nil {
			limit = s.deps.Runtime.InputLimit()
		}
		if inputFailure == nil {
			if int64(len(input)) > limit {
				inputFailure = &policyapi.Failure{Code: "LIMIT", Message: "Policy input exceeds its configured byte limit."}
			} else if inputs.ValidateBounded(string(req.Type), input, limit) != nil {
				inputFailure = &policyapi.Failure{Code: "INPUT", Message: "Policy input does not match its typed contract."}
			}
		}
		if inputFailure == nil {
			input = bytes.Clone(input)
		} else {
			input = nil
		}
		receipts := make([]policyapi.Receipt, len(rows))
		requests := make([]rego.Request, len(rows))
		for i, row := range rows {
			receipts[i] = policyapi.Receipt{
				ID:            kernel.NewULID(),
				Policy:        row.ID,
				PolicyName:    row.Name,
				PolicyVersion: row.Version,
				Type:          req.Type,
				Engine:        string(row.Engine),
				CreatedAt:     s.deps.Now(),
			}
			requests[i] = rego.Request{Policy: rego.Policy{Type: row.Type, Engine: row.Engine, Body: row.Body}, Input: input}
		}
		results, documents := s.evaluatePrograms(ctx, req.Type, requests, inputFailure)
		for i, result := range results {
			receipts[i].Decision = result.Result.Decision
			if inputFailure != nil {
				failure := *inputFailure
				receipts[i].Failure = &failure
			} else if result.Err != nil {
				receipts[i].Failure = executionFailure(result.Err)
			}
		}
		out = combineDecisions(req.Type, receipts)
		for i, receipt := range out.Receipts {
			if receipt.Sample {
				out.Samples = append(
					out.Samples,
					policyapi.SampleRequest{
						Receipt:        copyReceipt(receipt),
						Body:           rows[i].Body,
						Input:          bytes.Clone(documents[i]),
						Print:          results[i].Result.Print,
						PrintTruncated: results[i].Result.PrintTruncated,
					},
				)
			}
			if err := s.evaluated(ctx, req, space, receipt); err != nil {
				return err
			}
		}
		for _, failure := range out.Failures {
			doc, err := json.Marshal(policyapi.InternalError{Evaluation: out.Receipts[0].ID, Type: req.Type, Target: req.Target, Run: req.Run, Failure: failure})
			if err != nil {
				return fmt.Errorf("policies: encode internal error: %w", err)
			}
			if err := s.evaluationEvent(ctx, req, space, policyapi.InternalErrorKind, kernel.NewULID(), doc); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return policyapi.Evaluation{}, fmt.Errorf("policies: evaluate: %w", err)
	}
	return out, nil
}

func copyReceipt(r policyapi.Receipt) policyapi.Receipt {
	r.Decision = bytes.Clone(r.Decision)
	r.Flags = slices.Clone(r.Flags)
	r.ResetFlags = slices.Clone(r.ResetFlags)
	if r.Failure != nil {
		failure := *r.Failure
		r.Failure = &failure
	}
	return r
}

func checkEvaluation(req policyapi.EvaluationRequest) error {
	if req.Account.IsZero() || req.Target.ID.IsZero() || req.Run != nil && req.Run.IsZero() {
		return fmt.Errorf("%w: account and target identities are required", policyapi.ErrTarget)
	}
	if req.Input != nil && req.BuildInput != nil {
		return fmt.Errorf("%w: choose input or input builder", policyapi.ErrInput)
	}
	switch req.Type {
	case policyapi.Login:
		if req.Target.Kind == policyapi.SpaceTarget && req.Target.ID == req.Account.ULID {
			return nil
		}
	case policyapi.Notification:
		if req.Target.Kind == policyapi.SpaceTarget {
			return nil
		}
	case policyapi.Plan, policyapi.Approval, policyapi.Push, policyapi.Trigger, policyapi.Task, policyapi.Initialization:
		if req.Target.Kind == policyapi.StackTarget {
			return nil
		}
	}
	return fmt.Errorf("%w: %s on %s is unavailable", policyapi.ErrTarget, req.Type, req.Target.Kind)
}

func (s *Service) selectPolicies(ctx context.Context, req policyapi.EvaluationRequest) ([]Policy, kernel.ULID, error) {
	space := req.Target.ID
	var stack *kernel.ULID
	names := []string{}
	if req.Target.Kind == policyapi.StackTarget {
		stack = &req.Target.ID
		if err := s.deps.Stacks.Hold(ctx, req.Account, *stack); err != nil {
			return nil, kernel.ULID{}, fmt.Errorf("policies: hold evaluation stack: %w", err)
		}
		st, err := s.deps.Stacks.Get(ctx, req.Account, *stack)
		if err != nil {
			return nil, kernel.ULID{}, fmt.Errorf("policies: read evaluation stack: %w", err)
		}
		space = st.Space
		labels, err := s.deps.Stacks.Labels(ctx, req.Account, []kernel.ULID{*stack})
		if err != nil {
			return nil, kernel.ULID{}, fmt.Errorf("policies: read evaluation labels: %w", err)
		}
		names = append(names, autoPrefix+"*")
		for _, label := range labels[*stack] {
			names = append(names, autoPrefix+label)
		}
	}
	// Access locks a moved subtree. This hold therefore fences ancestor moves
	// and inheritance changes before reading the current tree.
	if err := s.deps.Spaces.Hold(ctx, req.Account, space); err != nil {
		return nil, kernel.ULID{}, fmt.Errorf("policies: hold evaluation space: %w", err)
	}
	tree, err := s.deps.Spaces.Tree(ctx, req.Account)
	if err != nil {
		return nil, kernel.ULID{}, fmt.Errorf("policies: read evaluation reach: %w", err)
	}
	sources := []kernel.ULID{}
	for _, node := range tree {
		if reachable(tree, space, node.ID) {
			sources = append(sources, node.ID)
		}
	}
	rows, err := s.deps.Store.Effective(ctx, req.Account, rego.Type(req.Type), sources, stack, names)
	if err != nil {
		return nil, kernel.ULID{}, fmt.Errorf("policies: effective evaluation selection: %w", err)
	}
	return rows, space, nil
}

func (s *Service) evaluatePrograms(ctx context.Context, kind policyapi.Type, requests []rego.Request, inputFailure *policyapi.Failure) ([]rego.Outcome, []json.RawMessage) {
	documents := make([]json.RawMessage, len(requests))
	for i, request := range requests {
		documents[i] = request.Input
	}
	if inputFailure != nil {
		return make([]rego.Outcome, len(requests)), documents
	}
	if s.deps.Runtime == nil {
		out := make([]rego.Outcome, len(requests))
		for i := range out {
			out[i].Err = errRuntime
		}
		return out, documents
	}
	if kind != policyapi.Login {
		return s.deps.Runtime.EvaluateAll(ctx, requests), documents
	}
	current := requests[0].Input
	results := s.deps.Runtime.EvaluateSequence(ctx, requests, func(i int, result rego.Outcome) (json.RawMessage, error) {
		documents[i] = current
		if result.Err != nil {
			return current, nil
		}
		next, err := rewriteLoginTeams(current, result.Result.Decision)
		if err != nil {
			return nil, err
		}
		current = next
		return current, nil
	})
	return results, documents
}

var errRuntime = errors.New("policy runtime unavailable")

func executionFailure(err error) *policyapi.Failure {
	code, message := "EXECUTION", "Policy evaluation failed."
	var compile *rego.CompileError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, message = "TIMEOUT", "Policy evaluation timed out."
	case errors.Is(err, context.Canceled):
		code, message = "CANCELED", "Policy evaluation was canceled."
	case errors.Is(err, rego.ErrInput):
		code, message = "INPUT", "Policy input is invalid."
	case errors.Is(err, rego.ErrResult):
		code, message = "RESULT", "Policy returned an invalid decision."
	case errors.Is(err, rego.ErrLimit):
		code, message = "LIMIT", "Policy execution exceeded its limit."
	case errors.As(err, &compile), errors.Is(err, rego.ErrPolicy):
		code, message = "COMPILE", "Policy could not be compiled."
	case errors.Is(err, errRuntime):
		code, message = "RUNTIME", "Policy runtime is unavailable."
	}
	return &policyapi.Failure{Code: code, Message: message}
}

func rewriteLoginTeams(raw, decision json.RawMessage) (json.RawMessage, error) {
	var d inputs.LoginDecision
	if err := json.Unmarshal(decision, &d); err != nil {
		return nil, rego.ErrResult
	}
	teams, _ := d.Team.Get()
	if len(teams) == 0 {
		return raw, nil
	}
	var input inputs.LoginInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, rego.ErrInput
	}
	session, present := input.Session.Get()
	if !present {
		return raw, nil
	}
	session.Teams = inputs.Value(teams)
	input.Session = inputs.Value(session)
	next, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("rewrite login policy teams: %w", err)
	}
	return next, nil
}

func combineDecisions(kind policyapi.Type, receipts []policyapi.Receipt) policyapi.Evaluation {
	switch kind {
	case policyapi.Plan:
		return combine.Plan(receipts)
	case policyapi.Approval:
		return combine.Approval(receipts)
	case policyapi.Push:
		return combine.Push(receipts)
	case policyapi.Trigger:
		return combine.Trigger(receipts)
	case policyapi.Login:
		return combine.Login(receipts)
	case policyapi.Notification:
		return combine.Notification(receipts)
	default:
		return combine.Negative(kind, receipts)
	}
}

func (s *Service) evaluated(ctx context.Context, req policyapi.EvaluationRequest, space kernel.ULID, receipt policyapi.Receipt) error {
	doc, err := json.Marshal(
		policyapi.Evaluated{
			Evaluation:    receipt.ID,
			Policy:        receipt.Policy,
			PolicyVersion: receipt.PolicyVersion,
			Type:          receipt.Type,
			Engine:        receipt.Engine,
			Outcome:       receipt.Outcome,
			Target:        req.Target,
			Run:           req.Run,
			Failure:       receipt.Failure,
		},
	)
	if err != nil {
		return fmt.Errorf("policies: encode evaluation event: %w", err)
	}
	return s.evaluationEvent(ctx, req, space, policyapi.EvaluatedKind, receipt.ID, doc)
}

func (s *Service) evaluationEvent(ctx context.Context, req policyapi.EvaluationRequest, space kernel.ULID, kind string, id kernel.ULID, doc json.RawMessage) error {
	_, err := s.deps.Events.Append(
		ctx,
		outbox.Event{
			Account:       req.Account.ULID,
			ID:            id,
			Kind:          kind,
			SchemaVersion: policyapi.EvaluationEventVersion,
			Space:         space.String(),
			Subject:       outbox.Subject{Kind: "policy_evaluation", ID: id.String()},
			Sequence:      1,
			Document:      doc,
			OccurredAt:    s.deps.Now(),
		},
	)
	if err != nil {
		return fmt.Errorf("policies: append evaluation event: %w", err)
	}
	return nil
}
