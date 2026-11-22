package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Attachment is one effective policy/stack relation, with its own identity.
type Attachment struct {
	Account           kernel.AccountID
	ID, Policy, Stack kernel.ULID
	Autoattached      bool
	CreatedAt         time.Time
}

var stackTypes = []rego.Type{rego.Approval, rego.Push, rego.Initialization, rego.Plan, rego.Task, rego.Trigger}

func attachable(kind rego.Type) bool { return slices.Contains(stackTypes, kind) }

func (s *Service) action(ctx context.Context, p kernel.Principal, action accessapi.Action, space kernel.ULID) error {
	ok, err := s.deps.Authz.Authorize(ctx, p, action, space)
	if err != nil {
		return fmt.Errorf("policies: authorize %s: %w", action, err)
	}
	if !ok {
		return &Denied{Action: action}
	}
	return nil
}

func (s *Service) readable(ctx context.Context, p kernel.Principal, space kernel.ULID, kind, id string) error {
	ok, err := s.deps.Authz.Authorize(ctx, p, accessapi.ActionSpaceRead, space)
	if err != nil {
		return fmt.Errorf("policies: authorize read: %w", err)
	}
	if !ok {
		return &Missing{Kind: kind, ID: id}
	}
	return nil
}

func (s *Service) locked(ctx context.Context, acct kernel.AccountID, id string) (Policy, error) {
	parsed, err := kernel.ParseULID(id)
	if err != nil {
		return Policy{}, &Missing{Kind: "policy", ID: id}
	}
	p, err := s.deps.Store.Get(ctx, acct, parsed, true)
	if errors.Is(err, ErrNotFound) {
		return Policy{}, &Missing{Kind: "policy", ID: id}
	}
	return p, err //nolint:wrapcheck // the store names the read
}

func (s *Service) attachment(ctx context.Context, acct kernel.AccountID, id string) (Attachment, error) {
	parsed, err := kernel.ParseULID(id)
	if err != nil {
		return Attachment{}, &Missing{Kind: "policy attachment", ID: id}
	}
	a, err := s.deps.Store.Attachment(ctx, acct, parsed)
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, &Missing{Kind: "policy attachment", ID: id}
	}
	return a, err //nolint:wrapcheck // the store names the read
}

func (s *Service) stack(ctx context.Context, acct kernel.AccountID, slug string, hold bool) (stacksapi.Stack, error) {
	st, err := s.deps.Stacks.BySlug(ctx, acct, slug)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return st, &Missing{Kind: "stack", ID: slug}
	}
	if err != nil {
		return st, fmt.Errorf("policies: read stack: %w", err)
	}
	if hold {
		if err := s.deps.Stacks.Hold(ctx, acct, st.ID); err != nil {
			if errors.Is(err, stacksapi.ErrNotFound) {
				return st, &Missing{Kind: "stack", ID: slug}
			}
			return st, fmt.Errorf("policies: hold stack: %w", err)
		}
		// A move between the slug lookup and hold must not leave a stale space.
		st, err = s.deps.Stacks.Get(ctx, acct, st.ID)
		if err != nil {
			if errors.Is(err, stacksapi.ErrNotFound) {
				return st, &Missing{Kind: "stack", ID: slug}
			}
			return st, fmt.Errorf("policies: read held stack: %w", err)
		}
	}
	return st, nil
}

func reachable(nodes []accessapi.SpaceNode, space, ancestor kernel.ULID) bool {
	byID := make(map[kernel.ULID]accessapi.SpaceNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for visited := 0; visited <= len(nodes); visited++ {
		if space == ancestor {
			return true
		}
		n := byID[space]
		if !n.Inherit || n.Parent.IsZero() {
			return false
		}
		space = n.Parent
	}
	return false
}

// Attach makes one readable policy explicit on a stack in its inherited reach.
func (s *Service) Attach(ctx context.Context, p kernel.Principal, policy, stack string) (Attachment, stacksapi.Stack, error) {
	var a Attachment
	var st stacksapi.Stack
	err := tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.deps.Store.LockAutoattach(ctx, p.AccountID); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		row, err := s.locked(ctx, p.AccountID, policy)
		if err != nil {
			return err
		}
		if err := s.readable(ctx, p, row.Space, "policy", policy); err != nil {
			return err
		}
		if !attachable(row.Type) {
			return &kernel.Invalid{
				Detail: "This policy type does not attach to stacks.",
				Errors: []kernel.FieldError{
					{Pointer: "/policyId", Reason: kernel.ReasonRule, Message: "LOGIN and NOTIFICATION are space-scoped; INTENT policies attach to Intent projects."},
				},
			}
		}
		st, err = s.stack(ctx, p.AccountID, stack, true)
		if err != nil {
			return err
		}
		if err := s.readable(ctx, p, st.Space, "stack", stack); err != nil {
			return err
		}
		if err := s.action(ctx, p, accessapi.ActionStackPolicyAttach, st.Space); err != nil {
			return err
		}
		for _, space := range []kernel.ULID{row.Space, st.Space} {
			if err := s.deps.Spaces.Hold(ctx, p.AccountID, space); err != nil {
				return fmt.Errorf("policies: hold attachment space: %w", err)
			}
		}
		tree, err := s.deps.Spaces.Tree(ctx, p.AccountID)
		if err != nil {
			return fmt.Errorf("policies: read attachment reach: %w", err)
		}
		if !reachable(tree, st.Space, row.Space) {
			return &kernel.Invalid{
				Detail: "The stack cannot reach this policy's space.",
				Errors: []kernel.FieldError{
					{Pointer: "/policyId", Reason: kernel.ReasonRule, Message: "A stack attaches policies from its own space and the ancestors it inherits."},
				},
			}
		}
		a, err = s.deps.Store.Attach(ctx, Attachment{Account: p.AccountID, ID: kernel.NewULID(), Policy: row.ID, Stack: st.ID, CreatedAt: s.deps.Now()})
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		row.Version++
		row.UpdatedAt = s.deps.Now()
		if err := s.deps.Store.Update(ctx, row); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.changed(ctx, row, policyapi.ChangeAttached)
	})
	return a, st, err //nolint:wrapcheck // each transaction step names its failure
}

// PolicyAttachment reads an attachment only under its actual parent policy.
func (s *Service) PolicyAttachment(ctx context.Context, p kernel.Principal, policy, id string) (Attachment, stacksapi.Stack, error) {
	row, err := s.Get(ctx, p.AccountID, policy)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	a, err := s.attachment(ctx, p.AccountID, id)
	if err != nil {
		return a, stacksapi.Stack{}, err
	}
	if a.Policy != row.ID {
		return a, stacksapi.Stack{}, &Missing{Kind: "policy attachment", ID: id}
	}
	st, err := s.deps.Stacks.Get(ctx, p.AccountID, a.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return a, st, &Missing{Kind: "policy attachment", ID: id}
	}
	if err != nil {
		return a, st, fmt.Errorf("policies: read attached stack: %w", err)
	}
	if err := s.readable(ctx, p, st.Space, "policy attachment", id); err != nil {
		return a, st, err
	}
	if err := s.checkEffective(ctx, row, a, st); err != nil {
		return a, st, err
	}
	return a, st, nil
}

// AttachmentSpaceOf resolves mutation subjects by attachment identity and account.
func (s *Service) AttachmentSpaceOf(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error) {
	a, err := s.attachment(ctx, acct, id)
	var missing *Missing
	if errors.As(err, &missing) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	if err != nil {
		return kernel.ULID{}, err
	}
	st, err := s.deps.Stacks.Get(ctx, acct, a.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return st.Space, err //nolint:wrapcheck // the stack port names the read
}

// Detach removes an explicit relation. Automatic selection is changed by labels.
func (s *Service) Detach(ctx context.Context, p kernel.Principal, id string) (Attachment, stacksapi.Stack, error) {
	a, err := s.attachment(ctx, p.AccountID, id)
	if err != nil {
		return a, stacksapi.Stack{}, err
	}
	var st stacksapi.Stack
	err = tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.deps.Store.LockAutoattach(ctx, p.AccountID); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		row, err := s.locked(ctx, p.AccountID, a.Policy.String())
		if err != nil {
			return err
		}
		a, err = s.attachment(ctx, p.AccountID, id)
		if err != nil {
			return err
		}
		if a.Autoattached {
			return &kernel.Invalid{Detail: "Change the policy's autoattach labels to remove an automatic attachment."}
		}
		st, err = s.deps.Stacks.Get(ctx, p.AccountID, a.Stack)
		if err != nil {
			if errors.Is(err, stacksapi.ErrNotFound) {
				return &Missing{Kind: "policy attachment", ID: id}
			}
			return fmt.Errorf("policies: read detach target: %w", err)
		}
		if err := s.deps.Stacks.Hold(ctx, p.AccountID, st.ID); err != nil {
			if errors.Is(err, stacksapi.ErrNotFound) {
				return &Missing{Kind: "policy attachment", ID: id}
			}
			return fmt.Errorf("policies: hold detach target: %w", err)
		}
		st, err = s.deps.Stacks.Get(ctx, p.AccountID, a.Stack)
		if err != nil {
			if errors.Is(err, stacksapi.ErrNotFound) {
				return &Missing{Kind: "policy attachment", ID: id}
			}
			return fmt.Errorf("policies: read held detach target: %w", err)
		}
		if err := s.action(ctx, p, accessapi.ActionStackPolicyDetach, st.Space); err != nil {
			return err
		}
		if err := s.readable(ctx, p, row.Space, "policy", row.ID.String()); err != nil {
			return err
		}
		if err := s.deps.Store.Detach(ctx, p.AccountID, a.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		row.Version++
		row.UpdatedAt = s.deps.Now()
		if err := s.deps.Store.Update(ctx, row); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.changed(ctx, row, policyapi.ChangeDetached)
	})
	return a, st, err //nolint:wrapcheck // each transaction step names its failure
}

// Details batches policy-owned attachment projections, excluding unreadable targets.
type Details struct {
	Attachments map[kernel.ULID][]Attachment
	Stacks      map[kernel.ULID]stacksapi.Stack
	Evidence    EvidenceProjection
}

func (s *Service) Details(ctx context.Context, p kernel.Principal, rows ...Policy) (Details, error) {
	out := Details{Attachments: map[kernel.ULID][]Attachment{}, Stacks: map[kernel.ULID]stacksapi.Stack{}}
	ids := make([]kernel.ULID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	var err error
	out.Evidence, err = s.ProjectionEvidence(ctx, p, ids, nil, true)
	if err != nil {
		return out, err
	}
	all, err := s.deps.Store.Attachments(ctx, p.AccountID, ids, false)
	if err != nil {
		return out, err //nolint:wrapcheck // the store names the read
	}
	var stackIDs []kernel.ULID
	for _, as := range all {
		for _, a := range as {
			stackIDs = append(stackIDs, a.Stack)
		}
	}
	if len(stackIDs) == 0 {
		return out, nil
	}
	out.Stacks, err = s.deps.Stacks.ByIDs(ctx, p.AccountID, stackIDs)
	if err != nil {
		return out, fmt.Errorf("policies: read attachment targets: %w", err)
	}
	read, err := s.deps.Authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return out, fmt.Errorf("policies: readable attachment targets: %w", err)
	}
	tree, targetLabels, err := s.selectionData(ctx, p.AccountID, stackIDs)
	if err != nil {
		return out, err
	}
	policies := make(map[kernel.ULID]Policy, len(rows))
	for _, row := range rows {
		policies[row.ID] = row
	}
	for id, as := range all {
		for _, a := range as {
			st, ok := out.Stacks[a.Stack]
			if ok && slices.Contains(read, st.Space) && effective(tree, policies[id], a, st, targetLabels[st.ID]) {
				out.Attachments[id] = append(out.Attachments[id], a)
			}
		}
	}
	return out, nil
}

// StackAttachment reads an attachment only under its actual target stack.
func (s *Service) StackAttachment(ctx context.Context, p kernel.Principal, stack, id string) (Attachment, Policy, error) {
	st, err := s.stack(ctx, p.AccountID, stack, false)
	if err != nil {
		return Attachment{}, Policy{}, err
	}
	if err := s.readable(ctx, p, st.Space, "stack", stack); err != nil {
		return Attachment{}, Policy{}, err
	}
	a, err := s.attachment(ctx, p.AccountID, id)
	if err != nil {
		return a, Policy{}, err
	}
	if a.Stack != st.ID {
		return a, Policy{}, &Missing{Kind: "policy attachment", ID: id}
	}
	row, err := s.Get(ctx, p.AccountID, a.Policy.String())
	if err != nil {
		return a, row, err
	}
	if err := s.readable(ctx, p, row.Space, "policy attachment", id); err != nil {
		return a, row, err
	}
	if err := s.checkEffective(ctx, row, a, st); err != nil {
		return a, row, err
	}
	return a, row, nil
}

// AttachedPolicies implements the stack-owned back edge without importing stacks
// internals or exposing another account's or unreadable policy's source.
func (s *Service) AttachedPolicies(ctx context.Context, p kernel.Principal, stacks []kernel.ULID) (map[kernel.ULID][]stacksapi.AttachedPolicy, error) {
	out := map[kernel.ULID][]stacksapi.AttachedPolicy{}
	all, err := s.deps.Store.Attachments(ctx, p.AccountID, stacks, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	var ids []kernel.ULID
	for _, as := range all {
		for _, a := range as {
			ids = append(ids, a.Policy)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.deps.Store.ByIDs(ctx, p.AccountID, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	read, err := s.deps.Authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, fmt.Errorf("policies: readable stack projections: %w", err)
	}
	targets, err := s.deps.Stacks.ByIDs(ctx, p.AccountID, stacks)
	if err != nil {
		return nil, fmt.Errorf("policies: read projection targets: %w", err)
	}
	tree, targetLabels, err := s.selectionData(ctx, p.AccountID, stacks)
	if err != nil {
		return nil, err
	}
	for stack, as := range all {
		for _, a := range as {
			row, ok := rows[a.Policy]
			st, hasTarget := targets[stack]
			if ok && hasTarget && slices.Contains(read, row.Space) && slices.Contains(read, st.Space) && effective(tree, row, a, st, targetLabels[stack]) {
				out[stack] = append(
					out[stack],
					stacksapi.AttachedPolicy{ID: a.ID, Policy: row.ID, Name: row.Name, Body: row.Body, Type: string(row.Type), Labels: row.Labels, Autoattached: a.Autoattached},
				)
			}
		}
		if len(out[stack]) > 0 {
			policyIDs := make([]kernel.ULID, len(out[stack]))
			for i, policy := range out[stack] {
				policyIDs[i] = policy.Policy
			}
			projection, err := s.ProjectionEvidence(ctx, p, policyIDs, &stack, false)
			if err != nil {
				return nil, err
			}
			for i := range out[stack] {
				for _, record := range projection.Records[out[stack][i].Policy] {
					out[stack][i].EvaluationRecords = append(
						out[stack][i].EvaluationRecords,
						stacksapi.PolicyEvaluationRecord{ID: record.ID, Outcome: record.Outcome, CreatedAt: record.At},
					)
				}
			}
		}
		slices.SortFunc(out[stack], func(a, b stacksapi.AttachedPolicy) int {
			if a.Name < b.Name {
				return -1
			}
			if a.Name > b.Name {
				return 1
			}
			return bytes.Compare(a.Policy[:], b.Policy[:])
		})
	}
	return out, nil
}

func effective(tree []accessapi.SpaceNode, p Policy, a Attachment, st stacksapi.Stack, labels []string) bool {
	return attachable(p.Type) && reachable(tree, st.Space, p.Space) && (!a.Autoattached || automaticMatch(p.Labels, labels))
}

// Selection reads current reach and labels as well as persisted relations: a
// delayed cleanup job must never expose a relation outside its current scope.
func (s *Service) selectionData(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) ([]accessapi.SpaceNode, map[kernel.ULID][]string, error) {
	tree, err := s.deps.Spaces.Tree(ctx, acct)
	if err != nil {
		return nil, nil, fmt.Errorf("policies: read effective reach: %w", err)
	}
	labels, err := s.deps.Stacks.Labels(ctx, acct, stacks)
	if err != nil {
		return nil, nil, fmt.Errorf("policies: read effective labels: %w", err)
	}
	return tree, labels, nil
}

func (s *Service) checkEffective(ctx context.Context, p Policy, a Attachment, st stacksapi.Stack) error {
	tree, labels, err := s.selectionData(ctx, p.Account, []kernel.ULID{st.ID})
	if err != nil {
		return err
	}
	if !effective(tree, p, a, st, labels[st.ID]) {
		return &Missing{Kind: "policy attachment", ID: a.ID.String()}
	}
	return nil
}
