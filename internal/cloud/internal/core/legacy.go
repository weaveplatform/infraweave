package core

import (
	"context"
	"errors"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// LegacyInput is what a stack's legacy AWS role sets.
type LegacyInput struct {
	RoleARN    string
	Region     *string
	Duration   *int32
	Worker     bool
	ExternalID string
}

// legacyName is the name of the legacy integration bound to stack, unique
// to the stack's ULID, since an integration's name is unique in its
// account.
func legacyName(stack kernel.ULID) string { return "legacy-aws-role-" + stack.String() }

// Legacy reads the legacy integration bound to p's account's stack slug,
// with the stack; found is false when it has none.
func (s *Service) Legacy(ctx context.Context, p kernel.Principal, slug string) (Integration, stacksapi.Stack, bool, error) {
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return Integration{}, st, false, err
	}
	i, err := s.store.Legacy(ctx, p.AccountID, st.ID)
	if errors.Is(err, ErrNotFound) {
		return Integration{}, st, false, nil
	}
	return i, st, err == nil, err //nolint:wrapcheck // the store names the read
}

// SetLegacy sets the stack slug's legacy AWS role from in: an integration
// bound to that stack, in its space, attached for read and write, which
// it creates the first time and changes after. The role's trust policy is
// tested with the legacy external ID when the server assumes it. The
// route has decided STACK_AWS_INTEGRATION_ATTACH in the stack's space; its
// refusals point into stackIntegrationAwsCreate's body.
func (s *Service) SetLegacy(ctx context.Context, p kernel.Principal, slug string, in LegacyInput) (Integration, stacksapi.Stack, error) {
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return Integration{}, st, err
	}
	full := Input{
		Name: legacyName(st.ID), RoleARN: in.RoleARN, Region: in.Region, Duration: in.Duration,
		Worker: in.Worker, ExternalID: in.ExternalID,
	}
	duration, err := check("", full)
	if err != nil {
		return Integration{}, st, err
	}
	probe := Integration{
		Account: p.AccountID, ID: kernel.NewULID(), Legacy: true, RoleARN: in.RoleARN,
		Region: in.Region, Worker: in.Worker,
	}
	if err := s.trust(ctx, probe, st, true, true, "/roleArn"); err != nil {
		return Integration{}, st, err
	}
	var i Integration
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.holdStack(ctx, st); err != nil {
			return err
		}
		now := s.now()
		old, err := s.store.Legacy(ctx, p.AccountID, st.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			i = Integration{
				Account: p.AccountID, ID: kernel.NewULID(), Legacy: true, CreatedAt: now,
				UpdatedAt: now, Version: 1,
			}
			apply(&i, st.Space, duration, full)
			if err := s.store.Create(ctx, i); err != nil {
				return err //nolint:wrapcheck // the store names the write
			}
			if err := s.store.Attach(ctx, Attachment{
				Account: p.AccountID, ID: kernel.NewULID(), Integration: i.ID, Stack: st.ID,
				Read: true, Write: true, CreatedAt: now,
			}); err != nil {
				return err //nolint:wrapcheck // as above
			}
			return s.audit(ctx, p, ActionCreate, "CREATE", i, &st, in)
		case err != nil:
			return err //nolint:wrapcheck // the store names the read
		}
		if i, err = s.store.Get(ctx, p.AccountID, old.ID, Update); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		apply(&i, i.Space, duration, full)
		i.Version++
		i.UpdatedAt = now
		if err := s.store.Write(ctx, i); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionUpdate, "UPDATE", i, &st, in)
	})
	return i, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// DeleteLegacy deletes the stack slug's legacy AWS role and answers it as
// it was; found is false when the stack has none, which deletes nothing.
// The route has decided STACK_AWS_INTEGRATION_DETACH in the stack's space.
func (s *Service) DeleteLegacy(ctx context.Context, p kernel.Principal, slug string) (Integration, bool, error) {
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return Integration{}, false, err
	}
	var i Integration
	var found bool
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		old, err := s.store.Legacy(ctx, p.AccountID, st.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		} else if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if i, err = s.store.Get(ctx, p.AccountID, old.ID, Update); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		found = true
		if err := s.store.Delete(ctx, p.AccountID, i.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionDelete, "DELETE", i, &st, nil)
	})
	return i, found, err //nolint:wrapcheck // each step in the transaction names itself
}

// LegacyPolicy is the trust policy statement of the stack st's legacy AWS
// role, and the AWS console's link to create such a role, both naming the
// installation's AWS account and the legacy external ID; both are empty
// when the installation holds no AWS credentials.
func (s *Service) LegacyPolicy(ctx context.Context, st stacksapi.Stack) (string, string, error) {
	installation, err := s.roles.CallerAccount(ctx)
	if errors.Is(err, ErrUnsupported) {
		return "", "", nil
	} else if err != nil {
		return "", "", err //nolint:wrapcheck // the adapter's errors are the answers
	}
	account, err := s.accounts.Slug(ctx, st.Account)
	if err != nil {
		return "", "", err //nolint:wrapcheck // the access domain names the read
	}
	ext := LegacyExternalID(account, st.Slug)
	link := "https://console.aws.amazon.com/iam/home#/roles$new?step=type&roleType=crossAccount" +
		"&accountID=" + url.QueryEscape(installation) + "&externalID=" + url.QueryEscape(ext)
	return PolicyStatement(installation, ext), link, nil
}
