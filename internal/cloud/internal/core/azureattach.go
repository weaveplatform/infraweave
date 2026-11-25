package core

import (
	"context"
	"errors"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// azureKind is Azure integrations as a kind. A candidate has a default
// subscription, which the store's Labelled requires of each it reads.
func (s *Service) azureKind() kind {
	return kind{
		noun:   "Azure integration",
		stored: s.azure.Attachments,
		labelled: func(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]candidate, error) {
			all, err := s.azure.Labelled(ctx, acct, spaces, labels)
			out := make([]candidate, len(all))
			for n, i := range all {
				out[n] = candidate{ID: i.ID, Space: i.Space, Labels: i.Labels}
			}
			return out, err //nolint:wrapcheck // the store names the read
		},
	}
}

// subscription refuses a subscription ID that is not a GUID, pointing
// at /subscriptionId.
func subscription(id *string) error {
	if id == nil || IsGUID(*id) {
		return nil
	}
	return &kernel.Invalid{
		Detail: "The attachment's subscription is refused.",
		Errors: []kernel.FieldError{{
			Pointer: "/subscriptionId", Reason: kernel.ReasonFormat,
			Message: "A subscription ID is an Azure subscription's GUID.",
		}},
	}
}

// azureIntegration reads p's account's Azure integration id when p may
// read it; a *Missing otherwise.
func (s *Service) azureIntegration(ctx context.Context, p kernel.Principal, id string) (AzureIntegration, error) {
	i, err := s.GetAzure(ctx, p.AccountID, id)
	if err != nil {
		return AzureIntegration{}, err
	}
	return i, s.azureVisible(ctx, p, i)
}

// AttachAzure attaches p's account's Azure integration id to the stack
// slug for read, write or both, with the subscription sub when it is not
// nil, and answers the attachment with the stack. The route has decided
// STACK_AZURE_INTEGRATION_ATTACH in the stack's space; the caller must
// read the integration too, and an integration outside the stack's reach
// is refused. Its refusals point into azureIntegrationAttach's body.
func (s *Service) AttachAzure(ctx context.Context, p kernel.Principal, slug, id string, read, write bool, sub *string) (Attachment, stacksapi.Stack, error) {
	if err := sides("", read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if err := subscription(sub); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	i, err := s.azureIntegration(ctx, p, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	k := s.azureKind()
	if err := s.taken(ctx, k, p.AccountID, st, i.ID, kernel.ULID{}, read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	a := Attachment{
		ID: kernel.NewULID(), Account: p.AccountID, Integration: i.ID, Stack: st.ID,
		Read: read, Write: write, Subscription: sub, CreatedAt: s.now(),
	}
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.holdAzure(ctx, i); err != nil {
			return err
		}
		if err := s.holdStack(ctx, st); err != nil {
			return err
		}
		if err := s.reaches(ctx, k, i.Account, i.Space, i.Name, st, "/stackId"); err != nil {
			return err
		}
		if err := s.azure.Attach(ctx, a); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.auditAzure(ctx, p, AzureActionAttach, "CREATE", i, &st, attachmentBody(read, write, sub))
	})
	return a, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// attachmentBody is an attachment's sides and subscription as an audit
// entry's body.
func attachmentBody(read, write bool, sub *string) any {
	return struct {
		Read           bool    `json:"read"`
		Write          bool    `json:"write"`
		SubscriptionID *string `json:"subscriptionId"`
	}{read, write, sub}
}

// holdAzure locks i's row FOR SHARE and checks it is the version the
// caller read, so neither a move nor a delete passes the write unseen.
func (s *Service) holdAzure(ctx context.Context, i AzureIntegration) error {
	now, err := s.azure.Get(ctx, i.Account, i.ID, Share)
	if errors.Is(err, ErrNotFound) {
		return azureMissing(i.ID.String())
	} else if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if now.Version != i.Version {
		return &Conflict{Detail: "the Azure integration changed meanwhile; try again"}
	}
	return nil
}

// azureAttachment reads acct's stored Azure attachment by its ID as the
// API gives it; an automatic one's is refused, since a label made it.
func (s *Service) azureAttachment(ctx context.Context, acct kernel.AccountID, id string) (Attachment, error) {
	if strings.HasPrefix(id, AutoPrefix) {
		return Attachment{}, autoRefused(id)
	}
	uid, err := kernel.ParseULID(id)
	if err != nil {
		return Attachment{}, &Missing{Kind: "attachment", ID: id}
	}
	a, err := s.azure.Attachment(ctx, acct, uid)
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, &Missing{Kind: "attachment", ID: id}
	}
	return a, err //nolint:wrapcheck // the store names the read
}

// AzureAttachmentSpaceOf is the space of the stack acct's Azure
// attachment id attaches to, automatic or stored, which the middleware
// decides an attachment's operations on; accessapi.ErrNoSubject when acct
// lacks it.
func (s *Service) AzureAttachmentSpaceOf(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error) {
	return s.attachmentSpace(ctx, acct, id, s.azureAttachment)
}

// UpdateAzureAttachment sets which sides p's account's Azure attachment
// id serves and the subscription it uses, and answers it with its stack.
// The route has decided STACK_AZURE_INTEGRATION_UPDATE in the stack's
// space.
func (s *Service) UpdateAzureAttachment(ctx context.Context, p kernel.Principal, id string, read, write bool, sub *string) (Attachment, stacksapi.Stack, error) {
	if err := sides("", read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if err := subscription(sub); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	a, err := s.azureAttachment(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	i, err := s.azure.Get(ctx, p.AccountID, a.Integration, NoLock)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err //nolint:wrapcheck // the store names the read
	}
	st, err := s.stacks.Get(ctx, p.AccountID, a.Stack)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	if err := s.taken(ctx, s.azureKind(), p.AccountID, st, kernel.ULID{}, a.ID, read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.holdAzure(ctx, i); err != nil {
			return err
		}
		if err := s.azure.SetAttachment(ctx, p.AccountID, a.ID, read, write, sub); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.auditAzure(ctx, p, AzureActionAttachment, "UPDATE", i, &st, attachmentBody(read, write, sub))
	})
	a.Read, a.Write, a.Subscription = read, write, sub
	return a, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// DetachAzure deletes p's account's Azure attachment id and answers it as
// it was, with its stack. The route has decided
// STACK_AZURE_INTEGRATION_DETACH in the stack's space.
func (s *Service) DetachAzure(ctx context.Context, p kernel.Principal, id string) (Attachment, stacksapi.Stack, error) {
	a, err := s.azureAttachment(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	var st stacksapi.Stack
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		i, err := s.azure.Get(ctx, p.AccountID, a.Integration, Share)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if st, err = s.stacks.Get(ctx, p.AccountID, a.Stack); err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		if err := s.azure.Detach(ctx, p.AccountID, a.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.auditAzure(ctx, p, AzureActionDetach, "DELETE", i, &st, nil)
	})
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, stacksapi.Stack{}, &Missing{Kind: "attachment", ID: id}
	}
	return a, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// AzureAttachedTo are the attachments of i, stored or computed, as
// AttachedTo orders an AWS integration's.
func (s *Service) AzureAttachedTo(ctx context.Context, i AzureIntegration) ([]Attachment, error) {
	return s.attachedTo(ctx, s.azureKind(), i.Account, candidate{ID: i.ID, Space: i.Space, Labels: i.Labels},
		i.Autoattach && i.DefaultSubscription != nil)
}

// AzureAttachedStack reads p's account's Azure integration id's
// attachment attachment, stored or computed; a *Missing when the
// integration has none of that ID.
func (s *Service) AzureAttachedStack(ctx context.Context, p kernel.Principal, id, attachment string) (Attachment, error) {
	i, err := s.GetAzure(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, err
	}
	all, err := s.AzureAttachedTo(ctx, i)
	if err != nil {
		return Attachment{}, err
	}
	for _, a := range all {
		if a.APIID() == attachment {
			return a, nil
		}
	}
	return Attachment{}, &Missing{Kind: "attachment", ID: attachment}
}

// AzureAttached are the Azure attachments that serve each of stacks,
// stored or computed, keyed by stack ID, as Attached answers the AWS
// ones.
func (s *Service) AzureAttached(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]Attachment, error) {
	_, out, err := s.attached(ctx, s.azureKind(), acct, stacks)
	return out, err
}
