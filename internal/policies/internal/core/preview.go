package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Preview includes only readable targets which inherit the source policy space.
func (s *Service) Preview(ctx context.Context, p kernel.Principal, slug string, labels []string) (stacksapi.AttachmentPreview, error) {
	if fields := kernel.CheckLabels("/labels", labels); len(fields) > 0 {
		return stacksapi.AttachmentPreview{}, &kernel.Invalid{Detail: "The preview labels are refused.", Errors: fields}
	}
	source, err := s.deps.Spaces.Find(ctx, p.AccountID, slug)
	if err != nil {
		if errors.Is(err, accessapi.ErrNoSpace) {
			return stacksapi.AttachmentPreview{}, &Missing{Kind: "space", ID: slug}
		}
		return stacksapi.AttachmentPreview{}, fmt.Errorf("policies: preview source space: %w", err)
	}
	if err := s.readable(ctx, p, source, "space", slug); err != nil {
		return stacksapi.AttachmentPreview{}, err
	}
	tree, err := s.deps.Spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return stacksapi.AttachmentPreview{}, fmt.Errorf("policies: preview tree: %w", err)
	}
	readable, err := s.deps.Authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return stacksapi.AttachmentPreview{}, fmt.Errorf("policies: preview readable spaces: %w", err)
	}
	spaces := slices.DeleteFunc(reached(tree, source), func(id kernel.ULID) bool { return !slices.Contains(readable, id) })
	names, all := automaticLabels(labels)
	return s.deps.Targets.Preview(ctx, p.AccountID, spaces, names, all) //nolint:wrapcheck // the stacks port names the query
}
