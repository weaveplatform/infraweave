package http

import (
	"context"
	"fmt"
	"math"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// AutoattachmentForLabels previews a full count and at most 100 updated targets.
func (s Server) AutoattachmentForLabels(ctx context.Context, req api.AutoattachmentForLabelsRequestObject) (api.AutoattachmentForLabelsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Preview(ctx, p, req.Params.Space, req.Params.Labels)
	if err != nil {
		return nil, policyError(err)
	}
	if res.Count < 0 || res.Count > math.MaxInt32 {
		return nil, fmt.Errorf("policies: preview count exceeds the API range: %w", kernel.ErrUnavailable)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, fmt.Errorf("policies: preview space slugs: %w", err)
	}
	out := api.AutoattachmentTargets{Count: int32(res.Count), Sample: []api.AutoattachmentTarget{}}
	for _, target := range res.Sample {
		space := slugs[target.Space]
		out.Sample = append(out.Sample, api.AutoattachmentTarget{Id: target.Slug, Name: target.Name, Labels: target.Labels, Space: space, SpaceDetails: api.SpaceRef{Id: space}})
	}
	return api.AutoattachmentForLabels200JSONResponse{Body: out}, nil
}

// AttachablePoliciesInSpace lists readable, inheritable stack policy candidates.
func (s Server) AttachablePoliciesInSpace(ctx context.Context, req api.AttachablePoliciesInSpaceRequestObject) (api.AttachablePoliciesInSpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Candidates(ctx, p, req.Params.Id, attachmentPage(req.Params.Cursor, req.Params.Limit))
	if err != nil {
		return nil, policyError(err)
	}
	out, err := s.candidatePage(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.AttachablePoliciesInSpace200JSONResponse{Body: out}, nil
}

// StackAttachablePolicies uses the actual stack's current inheritance scope.
func (s Server) StackAttachablePolicies(ctx context.Context, req api.StackAttachablePoliciesRequestObject) (api.StackAttachablePoliciesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.StackCandidates(ctx, p, req.StackId, attachmentPage(req.Params.Cursor, req.Params.Limit))
	if err != nil {
		return nil, policyError(err)
	}
	out, err := s.candidatePage(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.StackAttachablePolicies200JSONResponse{Body: out}, nil
}

func attachmentPage(cursor *api.Cursor, limit *api.Limit) search.Input {
	in := search.Input{First: limit}
	if cursor != nil {
		in.After = *cursor
	}
	return in
}

func (s Server) candidatePage(ctx context.Context, p kernel.Principal, res search.Result[core.Policy]) (api.PolicyPage, error) {
	rows := make([]core.Policy, len(res.Edges))
	for i, edge := range res.Edges {
		rows[i] = edge.Node
	}
	views, err := s.views(ctx, p, rows...)
	if err != nil {
		return api.PolicyPage{}, err
	}
	out := api.PolicyPage{Items: views}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return out, nil
}

// PolicyAttach makes an effective relation explicit under the stack action.
func (s Server) PolicyAttach(ctx context.Context, req api.PolicyAttachRequestObject) (api.PolicyAttachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.Attach(ctx, p, req.PolicyId, req.StackId)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.attachmentView(ctx, p, a, st)
	if err != nil {
		return nil, err
	}
	return api.PolicyAttach200JSONResponse{Body: v}, nil
}

// PolicyDetach removes an attachment by its own identity.
func (s Server) PolicyDetach(ctx context.Context, req api.PolicyDetachRequestObject) (api.PolicyDetachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.Detach(ctx, p, req.AttachmentId)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.attachmentView(ctx, p, a, st)
	if err != nil {
		return nil, err
	}
	return api.PolicyDetach200JSONResponse{Body: v}, nil
}

// PolicyAttachedStack reads only an attachment of the named parent policy.
func (s Server) PolicyAttachedStack(ctx context.Context, req api.PolicyAttachedStackRequestObject) (api.PolicyAttachedStackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.PolicyAttachment(ctx, p, req.PolicyId, req.Params.Id)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.attachmentView(ctx, p, a, st)
	if err != nil {
		return nil, err
	}
	return api.PolicyAttachedStack200JSONResponse{Body: v}, nil
}

// StackPolicyAttachment reads only an attachment of the named target stack.
func (s Server) StackPolicyAttachment(ctx context.Context, req api.StackPolicyAttachmentRequestObject) (api.StackPolicyAttachmentResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, row, err := s.svc.StackAttachment(ctx, p, req.StackId, req.Params.Id)
	if err != nil {
		return nil, policyError(err)
	}
	projection, err := s.svc.ProjectionEvidence(ctx, p, []kernel.ULID{row.ID}, &a.Stack, false)
	if err != nil {
		return nil, policyError(err)
	}
	return api.StackPolicyAttachment200JSONResponse{
		Body: api.StackPolicyAttachment{
			Id:                a.ID.String(),
			PolicyId:          row.ID.String(),
			PolicyName:        row.Name,
			PolicyBody:        row.Body,
			PolicyLabels:      row.Labels,
			PolicyType:        api.PolicyType(row.Type),
			IsAutoattached:    a.Autoattached,
			EvaluationRecords: evaluationRecordViews(projection.Records[row.ID]),
		},
	}, nil
}

func (s Server) attachmentView(ctx context.Context, p kernel.Principal, a core.Attachment, st stacksapi.Stack) (api.PolicyStackAttachment, error) {
	names, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.PolicyStackAttachment{}, fmt.Errorf("policies: name attachment space: %w", err)
	}
	return attachmentView(a, st, names), nil
}

func attachmentView(a core.Attachment, st stacksapi.Stack, names map[kernel.ULID]string) api.PolicyStackAttachment {
	return api.PolicyStackAttachment{Id: a.ID.String(), StackId: st.Slug, StackName: st.Name, StackSpaceDetails: api.SpaceRef{Id: names[st.Space]}, IsAutoattached: a.Autoattached}
}

func (s Server) views(ctx context.Context, p kernel.Principal, rows ...core.Policy) ([]api.Policy, error) {
	out := make([]api.Policy, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	names, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, fmt.Errorf("policies: name spaces: %w", err)
	}
	details, err := s.svc.Details(ctx, p, rows...)
	if err != nil {
		return nil, policyError(err)
	}
	for _, row := range rows {
		view := policyView(row, names)
		count := details.Evidence.SampleCounts[row.ID]
		if count < 0 || count > math.MaxInt32 {
			return nil, fmt.Errorf("policies: sample count exceeds API range: %w", kernel.ErrUnavailable)
		}
		view.SampleCount = int32(count)
		view.EvaluationRecords = evaluationRecordViews(details.Evidence.Records[row.ID])
		for _, a := range details.Attachments[row.ID] {
			view.AttachedStacks = append(view.AttachedStacks, attachmentView(a, details.Stacks[a.Stack], names))
		}
		view.HasAttachedStacks = len(view.AttachedStacks) > 0
		out = append(out, view)
	}
	return out, nil
}

func evaluationRecordViews(rows []core.EvaluationRecord) []api.PolicyEvaluationRecord {
	out := make([]api.PolicyEvaluationRecord, 0, len(rows))
	for _, record := range rows {
		out = append(out, api.PolicyEvaluationRecord{Key: record.ID.String(), Outcome: record.Outcome, Timestamp: unix(record.At.Unix())})
	}
	return out
}
