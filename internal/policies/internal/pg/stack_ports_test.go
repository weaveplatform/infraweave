package pg_test

import (
	"context"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Policies' own PostgreSQL failures are tested with real rows. This port lets
// those tests independently fail a cross-domain read without changing stacks SQL.
type stackPort struct {
	stacksapi.Stacks
	stack  stacksapi.Stack
	labels []string
	fail   string
	err    error
}

func (s *stackPort) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error) {
	if s.fail == "Get" {
		return stacksapi.Stack{}, s.err
	}
	if acct != s.stack.Account || id != s.stack.ID {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return s.stack, nil
}

func (s *stackPort) BySlug(_ context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error) {
	if s.fail == "BySlug" {
		return stacksapi.Stack{}, s.err
	}
	if acct != s.stack.Account || slug != s.stack.Slug {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return s.stack, nil
}

func (s *stackPort) Hold(_ context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if s.fail == "Hold" {
		return s.err
	}
	if acct != s.stack.Account || id != s.stack.ID {
		return stacksapi.ErrNotFound
	}
	return nil
}

func (s *stackPort) ByIDs(_ context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]stacksapi.Stack, error) {
	if s.fail == "ByIDs" {
		return nil, s.err
	}
	out := map[kernel.ULID]stacksapi.Stack{}
	if acct == s.stack.Account && slices.Contains(ids, s.stack.ID) {
		out[s.stack.ID] = s.stack
	}
	return out, nil
}

func (s *stackPort) InSpaces(_ context.Context, acct kernel.AccountID, spaces []kernel.ULID) ([]stacksapi.Stack, error) {
	if s.fail == "InSpaces" {
		return nil, s.err
	}
	if acct == s.stack.Account && slices.Contains(spaces, s.stack.Space) {
		return []stacksapi.Stack{s.stack}, nil
	}
	return nil, nil
}

func (s *stackPort) Find(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]stacksapi.Stack, error) {
	if s.fail == "Find" {
		return nil, s.err
	}
	if !slices.ContainsFunc(labels, func(l string) bool { return slices.Contains(s.labels, l) }) {
		return nil, nil
	}
	return s.InSpaces(ctx, acct, spaces)
}

func (s *stackPort) Labels(_ context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID][]string, error) {
	if s.fail == "Labels" {
		return nil, s.err
	}
	out := map[kernel.ULID][]string{}
	if acct == s.stack.Account && slices.Contains(ids, s.stack.ID) {
		out[s.stack.ID] = slices.Clone(s.labels)
	}
	return out, nil
}

func (s *stackPort) Preview(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string, all bool) (stacksapi.AttachmentPreview, error) {
	if s.fail == "Preview" {
		return stacksapi.AttachmentPreview{}, s.err
	}
	var rows []stacksapi.Stack
	var err error
	if all {
		rows, err = s.InSpaces(ctx, acct, spaces)
	} else {
		rows, err = s.Find(ctx, acct, spaces, labels)
	}
	out := stacksapi.AttachmentPreview{Sample: []stacksapi.AttachmentTarget{}}
	for _, row := range rows {
		out.Sample = append(out.Sample, stacksapi.AttachmentTarget{Stack: row, Labels: slices.Clone(s.labels)})
		out.Count++
	}
	return out, err
}
