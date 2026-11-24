package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func etag(i core.Integration) *string {
	t := strict.ETag(i.Version)
	return &t
}

// view is i as the API answers it.
func (s Server) view(ctx context.Context, i core.Integration) (api.AwsIntegration, error) {
	vs, err := s.views(ctx, i.Account, i)
	if err != nil {
		return api.AwsIntegration{}, err
	}
	return vs[0], nil
}

// views are all as the API answers them, each with its attachments,
// stored and computed, and the spaces and stacks they name read once.
func (s Server) views(ctx context.Context, acct kernel.AccountID, all ...core.Integration) ([]api.AwsIntegration, error) {
	slugs, err := s.spaces.Slugs(ctx, acct)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	attached := make([][]core.Attachment, len(all))
	var ids []kernel.ULID
	for n, i := range all {
		if attached[n], err = s.svc.AttachedTo(ctx, i); err != nil {
			return nil, err //nolint:wrapcheck // the store names the read
		}
		for _, a := range attached[n] {
			ids = append(ids, a.Stack)
		}
	}
	stacks, err := s.stacks.ByIDs(ctx, acct, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	out := make([]api.AwsIntegration, len(all))
	for n, i := range all {
		v := api.AwsIntegration{
			Id: i.ID.String(), Kind: api.AwsIntegrationKindAwsIntegration, Name: i.Name,
			Space: slugs[i.Space], SpaceDetails: api.SpaceRef{Id: slugs[i.Space]},
			RoleArn: i.RoleARN, Region: i.Region, DurationSeconds: &i.Duration,
			GenerateCredentialsInWorker: i.Worker, ExternalId: i.ExternalID,
			Labels: labelsOf(i.Labels), AutoattachEnabled: i.Autoattach,
			TagAssumeRole: i.TagSession, Legacy: i.Legacy,
			AttachedScans:  []api.AwsIntegrationScanAttachment{},
			AttachedStacks: []api.AwsIntegrationStackAttachment{},
		}
		for _, a := range attached[n] {
			st, ok := stacks[a.Stack]
			if !ok {
				continue
			}
			v.AttachedStacks = append(v.AttachedStacks, attachmentView(a, st, slugs[st.Space]))
		}
		v.HasAttachedStacks = len(v.AttachedStacks) > 0
		out[n] = v
	}
	return out, nil
}

func labelsOf(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func attachmentView(a core.Attachment, st stacksapi.Stack, space string) api.AwsIntegrationStackAttachment {
	return api.AwsIntegrationStackAttachment{
		Id: a.APIID(), IsAutoattached: a.Auto, Read: a.Read, Write: a.Write,
		StackId: st.Slug, StackName: st.Name, SpaceDetails: api.SpaceRef{Id: space},
	}
}

// attachment is a as the API answers it, with the stack it serves read.
func (s Server) attachment(ctx context.Context, a core.Attachment) (api.AwsIntegrationStackAttachment, error) {
	st, err := s.stacks.Get(ctx, a.Account, a.Stack)
	if err != nil {
		return api.AwsIntegrationStackAttachment{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	return s.attachmentOn(ctx, a, st)
}

// attachmentOn is a on st as the API answers it.
func (s Server) attachmentOn(ctx context.Context, a core.Attachment, st stacksapi.Stack) (api.AwsIntegrationStackAttachment, error) {
	slugs, err := s.spaces.Slugs(ctx, st.Account)
	if err != nil {
		return api.AwsIntegrationStackAttachment{}, err //nolint:wrapcheck // the access domain names the read
	}
	return attachmentView(a, st, slugs[st.Space]), nil
}

// inputView is i as an update's body, which a merge patch applies to.
func inputView(i core.Integration, space string) api.AwsIntegrationUpdateRequest {
	return api.AwsIntegrationUpdateRequest{
		Name: i.Name, Space: &space, RoleArn: i.RoleARN, Region: i.Region,
		DurationSeconds: i.Duration, GenerateCredentialsInWorker: i.Worker,
		ExternalID: i.ExternalID, Labels: labelsOf(i.Labels), AutoattachEnabled: &i.Autoattach,
		TagAssumeRole: &i.TagSession,
	}
}

// legacyView is the legacy integration i of st as the API answers it.
func (s Server) legacyView(ctx context.Context, i core.Integration, st stacksapi.Stack) (api.StackIntegrationAws, error) {
	statement, link, err := s.svc.LegacyPolicy(ctx, st)
	if err != nil {
		return api.StackIntegrationAws{}, err //nolint:wrapcheck // the service names it
	}
	v := api.StackIntegrationAws{
		Activated: true, AssumedRoleArn: &i.RoleARN, AssumeRoleLink: link,
		AssumeRolePolicyStatement: statement, DurationSeconds: &i.Duration,
		GenerateCredentialsInWorker: i.Worker, Region: i.Region,
	}
	if i.ExternalID != "" {
		v.ExternalID = &i.ExternalID
	}
	return v, nil
}
