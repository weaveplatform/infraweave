package core

import (
	"context"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// The service is the stack view's reader of each stack's AWS integration
// attachments.
var _ stacksapi.StackAwsAttachments = (*Service)(nil)

// AwsAttachments are the attachments that serve each of stacks, stored
// and computed, as Attached orders them, keyed by stack ID, with a stack
// none serves absent. An attachment whose integration lives in a space p
// may not read is left out, as that integration answers as one that does
// not exist everywhere else. Its reads are Attached's, the caller's
// readable spaces, the account's slug and the integrations named, so a
// page of stacks costs the same queries however long it is.
func (s *Service) AwsAttachments(ctx context.Context, p kernel.Principal, stacks []kernel.ULID) (map[kernel.ULID][]stacksapi.AwsAttachment, error) {
	found, attached, err := s.attached(ctx, s.aws(), p.AccountID, stacks)
	if err != nil {
		return nil, err
	}
	var ids []kernel.ULID
	for _, as := range attached {
		for _, a := range as {
			ids = append(ids, a.Integration)
		}
	}
	out := map[kernel.ULID][]stacksapi.AwsAttachment{}
	if len(ids) == 0 {
		return out, nil
	}
	readable, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	slices.SortFunc(ids, byID)
	integrations, err := s.store.ByIDs(ctx, p.AccountID, slices.Compact(ids))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	account, err := s.accounts.Slug(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	for id, as := range attached {
		for _, a := range as {
			i, ok := integrations[a.Integration]
			if !ok || !slices.Contains(readable, i.Space) {
				continue
			}
			out[id] = append(out[id], stacksapi.AwsAttachment{
				ID: a.APIID(), Integration: i.ID, Name: i.Name, RoleARN: i.RoleARN,
				ExternalID: AttachmentExternalID(i, account, found[id].Slug, a.Read, a.Write),
				Duration:   i.Duration, Labels: labels(i.Labels), Worker: i.Worker,
				Legacy: i.Legacy, Read: a.Read, Write: a.Write, Autoattached: a.Auto,
			})
		}
	}
	return out, nil
}

// AttachmentExternalID is the external ID an attachment of i to the stack
// whose slug is stack shows, in the account whose slug is account: a
// worker's integration's custom external ID when it sets one, else the
// legacy external ID of a legacy integration, else the external ID of the
// side it serves, the write side's when it serves both, as
// awsIntegrationExternalIdForStack answers.
//
// ref: decision 0006 "AWS external ID and session name"
func AttachmentExternalID(i Integration, account, stack string, read, write bool) string {
	if i.Worker && i.ExternalID != "" {
		return i.ExternalID
	}
	if i.Legacy {
		return LegacyExternalID(account, stack)
	}
	side := Write
	if read && !write {
		side = Read
	}
	return ExternalID(account, i.ID, stack, side)
}
