package blobs

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// Remove is blob.delete's handler.
func (r *Reaper) Remove(ctx context.Context, j queue.Job) error { return r.remove(ctx, j) }
