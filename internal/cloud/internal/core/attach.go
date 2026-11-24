package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TrustDetail starts the detail of an attachment refused because the
// role does not trust the installation, the text clients match on.
//
// ref: decision 0022 "Errors: a status per class, problem details, Spacelift's words kept (PD-01, option B)"
const TrustDetail = "you need to configure trust relationship section in your AWS account"

// trustDuration is how long a trust check's session lasts: the shortest
// STS grants, since its credentials are discarded.
const trustDuration = MinDuration * time.Second

// sides refuses an attachment with neither side, pointing under at.
func sides(at string, read, write bool) error {
	if read || write {
		return nil
	}
	return &kernel.Invalid{
		Detail: "An attachment serves read phases, write phases or both.",
		Errors: []kernel.FieldError{{
			Pointer: at + "/read", Reason: kernel.ReasonRule,
			Message: "At least one of read and write is true.",
		}},
	}
}

// taken refuses giving stack the sides read and write when another of
// its stored attachments, not skip, serves one of them already, or when
// integration is attached to it already.
func (s *Service) taken(ctx context.Context, acct kernel.AccountID, st stacksapi.Stack, integration, skip kernel.ULID, read, write bool) error {
	have, err := s.store.Attachments(ctx, acct, []kernel.ULID{st.ID}, true)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for _, a := range have[st.ID] {
		switch {
		case a.ID == skip:
		case a.Integration == integration:
			return &Conflict{Detail: fmt.Sprintf(
				"the AWS integration is attached to stack %s already; update attachment %s instead",
				st.Slug, a.ID)}
		case read && a.Read:
			return &Conflict{Detail: fmt.Sprintf(
				"stack %s has a read attachment already; a stack has one read and one write attachment",
				st.Slug)}
		case write && a.Write:
			return &Conflict{Detail: fmt.Sprintf(
				"stack %s has a write attachment already; a stack has one read and one write attachment",
				st.Slug)}
		}
	}
	return nil
}

// trust assumes i's role as the stack st would on each side asked for,
// and discards the credentials, so an attachment the role does not trust
// is refused before it is stored; a refusal points at at+"/read" or
// at+"/write", or at pointer itself when one external ID serves both. A
// worker's integration is not tested: the server holds no credential its
// role trusts.
func (s *Service) trust(ctx context.Context, i Integration, st stacksapi.Stack, read, write bool, at string) error {
	if i.Worker {
		return nil
	}
	account, err := s.accounts.Slug(ctx, i.Account)
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	type side struct {
		pointer, id string
	}
	var all []side
	switch {
	case i.Legacy:
		all = append(all, side{at, LegacyExternalID(account, st.Slug)})
	default:
		if read {
			all = append(all, side{at + "/read", ExternalID(account, i.ID, st.Slug, Read)})
		}
		if write {
			all = append(all, side{at + "/write", ExternalID(account, i.ID, st.Slug, Write)})
		}
	}
	region := ""
	if i.Region != nil {
		region = *i.Region
	}
	inv := &kernel.Invalid{}
	for _, sd := range all {
		err := s.roles.Assume(ctx, Assumption{
			RoleARN: i.RoleARN, ExternalID: sd.id, Region: region, Duration: trustDuration,
			SessionName: SessionName(kernel.NewULID(), st.Slug, account),
		})
		var refused *Refused
		switch {
		case errors.As(err, &refused):
			inv.Errors = append(inv.Errors, kernel.FieldError{
				Pointer: sd.pointer, Reason: kernel.ReasonRule, Message: fmt.Sprintf(
					"Role %s does not let the installation assume it with external ID %s: %s",
					i.RoleARN, sd.id, refused.Message),
			})
		case err != nil:
			return err //nolint:wrapcheck // the adapter's errors are the answers
		}
	}
	if len(inv.Errors) == 0 {
		return nil
	}
	inv.Detail = TrustDetail + ": role " + i.RoleARN + " refused the installation."
	return inv
}

// Attach attaches p's account's integration id to the stack slug for
// read, write or both, and answers the attachment with the stack. The
// route has decided STACK_AWS_INTEGRATION_ATTACH in the stack's space;
// the caller must read the integration too, and an integration outside
// the stack's reach is refused. An integration the server assumes is
// tested against the role's trust policy first. Its refusals point into
// awsIntegrationAttach's body.
func (s *Service) Attach(ctx context.Context, p kernel.Principal, slug, id string, read, write bool) (Attachment, stacksapi.Stack, error) {
	if err := sides("", read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	i, err := s.integration(ctx, p, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if i.Legacy {
		return Attachment{}, stacksapi.Stack{}, &kernel.Invalid{
			Detail: "A legacy AWS integration stays bound to its stack.",
			Errors: []kernel.FieldError{{
				Pointer: "/awsIntegrationId", Reason: kernel.ReasonRule,
				Message: "Attach an AWS integration created with awsIntegrationCreate.",
			}},
		}
	}
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if err := s.reaches(ctx, i, st, "/stackId"); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if err := s.taken(ctx, p.AccountID, st, i.ID, kernel.ULID{}, read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if err := s.trust(ctx, i, st, read, write, ""); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	a := Attachment{
		ID: kernel.NewULID(), Account: p.AccountID, Integration: i.ID, Stack: st.ID,
		Read: read, Write: write, CreatedAt: s.now(),
	}
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.hold(ctx, i); err != nil {
			return err
		}
		if err := s.holdStack(ctx, st); err != nil {
			return err
		}
		if err := s.reaches(ctx, i, st, "/stackId"); err != nil {
			return err
		}
		if err := s.store.Attach(ctx, a); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionAttach, "CREATE", i, &st,
			map[string]bool{"read": read, "write": write})
	})
	return a, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// hold locks i's row FOR SHARE and checks it is the version the caller
// tested, so neither a move, a delete nor a change of its role passes the
// write unseen.
func (s *Service) hold(ctx context.Context, i Integration) error {
	now, err := s.store.Get(ctx, i.Account, i.ID, Share)
	if errors.Is(err, ErrNotFound) {
		return &Missing{Kind: "AWS integration", ID: i.ID.String()}
	} else if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if now.Version != i.Version {
		return &Conflict{Detail: "the AWS integration changed meanwhile; try again"}
	}
	return nil
}

// holdStack holds st and its space in the caller's transaction.
func (s *Service) holdStack(ctx context.Context, st stacksapi.Stack) error {
	if err := s.stacks.Hold(ctx, st.Account, st.ID); errors.Is(err, stacksapi.ErrNotFound) {
		return &Missing{Kind: "stack", ID: st.Slug}
	} else if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the lock
	}
	return s.spaces.Hold(ctx, st.Account, st.Space) //nolint:wrapcheck // the access domain names the lock
}

// reaches refuses attaching i to st when st's space does not reach i's,
// pointing at pointer.
func (s *Service) reaches(ctx context.Context, i Integration, st stacksapi.Stack, pointer string) error {
	tree, err := s.spaces.Tree(ctx, i.Account)
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	if slices.Contains(Reach(index(tree), st.Space), i.Space) {
		return nil
	}
	return &kernel.Invalid{
		Detail: fmt.Sprintf("Stack %s cannot reach AWS integration %s.", st.Slug, i.Name),
		Errors: []kernel.FieldError{{
			Pointer: pointer, Reason: kernel.ReasonRule, Message: fmt.Sprintf(
				"A stack attaches the AWS integrations of its space and of the spaces it "+
					"inherits from; %s is in neither.", i.Name),
		}},
	}
}

// integration reads p's account's integration id when p may read it; a
// *Missing otherwise.
func (s *Service) integration(ctx context.Context, p kernel.Principal, id string) (Integration, error) {
	i, err := s.Get(ctx, p.AccountID, id)
	if errors.Is(err, ErrNotFound) {
		return Integration{}, &Missing{Kind: "AWS integration", ID: id}
	} else if err != nil {
		return Integration{}, err
	}
	if err := s.visible(ctx, p, i); err != nil {
		return Integration{}, err
	}
	return i, nil
}

// attachment reads acct's stored attachment by its ID as the API gives
// it; an automatic one's is refused, since a label made it.
func (s *Service) attachment(ctx context.Context, acct kernel.AccountID, id string) (Attachment, error) {
	if strings.HasPrefix(id, AutoPrefix) {
		return Attachment{}, autoRefused(id)
	}
	uid, err := kernel.ParseULID(id)
	if err != nil {
		return Attachment{}, &Missing{Kind: "attachment", ID: id}
	}
	a, err := s.store.Attachment(ctx, acct, uid)
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, &Missing{Kind: "attachment", ID: id}
	}
	return a, err //nolint:wrapcheck // the store names the read
}

// autoRefused refuses changing attachment id, which an autoattach label
// made: the label would make it again, so it is changed on the
// integration or the stack instead.
func autoRefused(id string) error {
	return &kernel.Invalid{
		Detail: fmt.Sprintf("Attachment %s follows an autoattach label.", id),
		Errors: []kernel.FieldError{{
			Pointer: "/attachmentId", Reason: kernel.ReasonRule,
			Message: "An automatic attachment changes with the integration's autoattach labels " +
				"or the stack's labels they name.",
		}},
	}
}

// AttachmentSpaceOf is the space of the stack acct's attachment id
// attaches to, automatic or stored, which the middleware decides an
// attachment's operations on; accessapi.ErrNoSubject when acct lacks it.
func (s *Service) AttachmentSpaceOf(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error) {
	var stack kernel.ULID
	if rest, ok := strings.CutPrefix(id, AutoPrefix); ok {
		_, after, found := strings.Cut(rest, "-")
		uid, err := kernel.ParseULID(after)
		if !found || err != nil {
			return kernel.ULID{}, accessapi.ErrNoSubject
		}
		stack = uid
	} else {
		a, err := s.attachment(ctx, acct, id)
		if _, missing := errors.AsType[*Missing](err); missing {
			return kernel.ULID{}, accessapi.ErrNoSubject
		} else if err != nil {
			return kernel.ULID{}, err
		}
		stack = a.Stack
	}
	st, err := s.stacks.Get(ctx, acct, stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return st.Space, err //nolint:wrapcheck // the stacks domain names the read
}

// UpdateAttachment sets which sides p's account's attachment id serves,
// testing the role's trust for each side first when the server assumes
// it, and answers it with its stack. The route has decided
// STACK_AWS_INTEGRATION_UPDATE in the stack's space.
func (s *Service) UpdateAttachment(ctx context.Context, p kernel.Principal, id string, read, write bool) (Attachment, stacksapi.Stack, error) {
	if err := sides("", read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	a, err := s.attachment(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	i, err := s.store.Get(ctx, p.AccountID, a.Integration, NoLock)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err //nolint:wrapcheck // the store names the read
	}
	st, err := s.stacks.Get(ctx, p.AccountID, a.Stack)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	if i.Legacy && (!read || !write) {
		return Attachment{}, stacksapi.Stack{}, &kernel.Invalid{
			Detail: "A legacy AWS integration serves both sides of its stack.",
			Errors: []kernel.FieldError{{
				Pointer: "/read", Reason: kernel.ReasonRule,
				Message: "Delete the stack's legacy AWS role, or keep read and write.",
			}},
		}
	}
	if err := s.taken(ctx, p.AccountID, st, kernel.ULID{}, a.ID, read, write); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	if err := s.trust(ctx, i, st, read, write, ""); err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if err := s.hold(ctx, i); err != nil {
			return err
		}
		if err := s.store.SetSides(ctx, p.AccountID, a.ID, read, write); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionAttachment, "UPDATE", i, &st,
			map[string]bool{"read": read, "write": write})
	})
	a.Read, a.Write = read, write
	return a, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// Detach deletes p's account's attachment id and answers it as it was,
// with its stack; detaching a legacy integration deletes it, since it
// serves that stack alone. The route has decided
// STACK_AWS_INTEGRATION_DETACH in the stack's space.
func (s *Service) Detach(ctx context.Context, p kernel.Principal, id string) (Attachment, stacksapi.Stack, error) {
	a, err := s.attachment(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	var st stacksapi.Stack
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		i, err := s.store.Get(ctx, p.AccountID, a.Integration, Update)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if st, err = s.stacks.Get(ctx, p.AccountID, a.Stack); err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		if i.Legacy {
			err = s.store.Delete(ctx, p.AccountID, i.ID)
		} else {
			err = s.store.Detach(ctx, p.AccountID, a.ID)
		}
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionDetach, "DELETE", i, &st, nil)
	})
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, stacksapi.Stack{}, &Missing{Kind: "attachment", ID: id}
	}
	return a, st, err //nolint:wrapcheck // each step in the transaction names itself
}

// ExternalID answers the external ID role assumption uses for p's
// account's integration id on the stack slug, for read, write or both,
// the write side's when both are asked for, with a trust policy statement
// that names the installation's AWS account and the external ID of each
// side asked for. The route has decided SPACE_READ on the stack's space.
func (s *Service) ExternalID(ctx context.Context, p kernel.Principal, id, slug string, read, write bool) (string, string, error) {
	if err := sides("", read, write); err != nil {
		return "", "", err
	}
	i, err := s.integration(ctx, p, id)
	if err != nil {
		return "", "", err
	}
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return "", "", err
	}
	account, err := s.accounts.Slug(ctx, p.AccountID)
	if err != nil {
		return "", "", err //nolint:wrapcheck // the access domain names the read
	}
	installation, err := s.roles.CallerAccount(ctx)
	if err != nil {
		return "", "", err //nolint:wrapcheck // the adapter's errors are the answers
	}
	if i.Legacy {
		ext := LegacyExternalID(account, st.Slug)
		return ext, PolicyStatement(installation, ext), nil
	}
	var ids []string
	if read {
		ids = append(ids, ExternalID(account, i.ID, st.Slug, Read))
	}
	if write {
		ids = append(ids, ExternalID(account, i.ID, st.Slug, Write))
	}
	return ids[len(ids)-1], PolicyStatement(installation, ids...), nil
}
