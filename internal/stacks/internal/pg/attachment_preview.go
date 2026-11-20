package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Preview reads the count and sample from one statement snapshot. Filtering is
// inside the window query so hidden targets affect neither the count nor order.
func (s *Store) Preview(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string, all bool) (stacksapi.AttachmentPreview, error) {
	out := stacksapi.AttachmentPreview{Sample: []stacksapi.AttachmentTarget{}}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT s.id,s.slug,s.name,s.space_id,
 ARRAY(SELECT l.label FROM stack_labels l WHERE l.account_id=s.account_id AND l.stack_id=s.id ORDER BY l.label COLLATE "C"),count(*) OVER()
 FROM stacks s WHERE s.account_id=$1 AND s.space_id=ANY($2) AND ($4::boolean OR EXISTS(
 SELECT 1 FROM stack_labels l WHERE l.account_id=s.account_id AND l.stack_id=s.id AND l.label=ANY($3::text[])))
 ORDER BY s.updated_at DESC,s.id DESC LIMIT 100`, acct, spaces, labels, all)
	if err != nil {
		return out, fmt.Errorf("stacks: preview attachment targets: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	for rows.Next() {
		target := stacksapi.AttachmentTarget{Stack: stacksapi.Stack{Account: acct}}
		if err := rows.Scan(&target.ID, &target.Slug, &target.Name, &target.Space, &target.Labels, &out.Count); err != nil {
			return out, fmt.Errorf("stacks: read attachment preview: %w", sql.Unavailable(err))
		}
		out.Sample = append(out.Sample, target)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("stacks: collect attachment preview: %w", sql.Unavailable(err))
	}
	return out, nil
}
