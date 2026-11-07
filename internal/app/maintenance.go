package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// DeadJobs are the queue's dead jobs as the operator reaches them;
// *queue.Queue is one.
type DeadJobs interface {
	ListDead(ctx context.Context, after int64, limit int) ([]queue.Dead, error)
	ReadDead(ctx context.Context, id int64) (queue.Dead, error)
	Retry(ctx context.Context, id int64) error
	Discard(ctx context.Context, id int64) error
}

// Platform is what the installation's own operations reach, which no
// domain owns: queue depths, dead jobs, and RotateKey, which rotates the
// data key now and answers the new active key's ID. A nil member's
// operations fail.
type Platform struct {
	Queues    QueueStats
	Dead      DeadJobs
	RotateKey func(context.Context) (uint32, error)
}

// deadJobsList names the dead-job list's cursors.
const deadJobsList = "dead-jobs"

// DeadJobs lists every organization's dead jobs, oldest first.
func (s operatorServer) DeadJobs(
	ctx context.Context,
	req operatorapi.DeadJobsRequestObject,
) (operatorapi.DeadJobsResponseObject, error) {
	var after int64
	if req.Params.Cursor != nil {
		c, err := page.Decode[int64](deadJobsList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		after = c.Key
	}
	n := int(kernel.PageLimit(req.Params.Limit))
	// One more than the page shows whether another follows.
	dead, err := s.platform.Dead.ListDead(ctx, after, n+1)
	if err != nil {
		return nil, err //nolint:wrapcheck // the queue marks an unreachable database
	}
	body := operatorapi.DeadJobPage{Items: make([]operatorapi.DeadJob, 0, min(len(dead), n))}
	for _, d := range dead[:min(len(dead), n)] {
		body.Items = append(body.Items, deadView(d))
	}
	if len(dead) > n {
		last := dead[n-1].ID
		next, err := page.Encode(deadJobsList,
			kernel.Cursor[int64]{Key: last, ID: strconv.FormatInt(last, 10)})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding an int cannot fail
		}
		body.NextCursor = &next
	}
	return operatorapi.DeadJobs200JSONResponse{Body: body}, nil
}

// DeadJob reads a dead job.
func (s operatorServer) DeadJob(
	ctx context.Context,
	req operatorapi.DeadJobRequestObject,
) (operatorapi.DeadJobResponseObject, error) {
	d, err := s.platform.Dead.ReadDead(ctx, req.Id)
	if err != nil {
		return nil, deadError(err, req.Id)
	}
	return operatorapi.DeadJob200JSONResponse{Body: deadView(d)}, nil
}

// DeadJobRetry makes a dead job pending again and answers it as it was.
func (s operatorServer) DeadJobRetry(
	ctx context.Context,
	req operatorapi.DeadJobRetryRequestObject,
) (operatorapi.DeadJobRetryResponseObject, error) {
	d, err := s.platform.Dead.ReadDead(ctx, req.Id)
	if err == nil {
		err = s.platform.Dead.Retry(ctx, req.Id)
	}
	if err != nil {
		return nil, deadError(err, req.Id)
	}
	return operatorapi.DeadJobRetry200JSONResponse{Body: deadView(d)}, nil
}

// DeadJobDiscard cancels a dead job, which pruning then deletes, and
// answers it as it was.
func (s operatorServer) DeadJobDiscard(
	ctx context.Context,
	req operatorapi.DeadJobDiscardRequestObject,
) (operatorapi.DeadJobDiscardResponseObject, error) {
	d, err := s.platform.Dead.ReadDead(ctx, req.Id)
	if err == nil {
		err = s.platform.Dead.Discard(ctx, req.Id)
	}
	if err != nil {
		return nil, deadError(err, req.Id)
	}
	return operatorapi.DeadJobDiscard200JSONResponse{Body: deadView(d)}, nil
}

// deadError is err as the problem it answers: 404 for a job that does not
// exist or is not dead, which a concurrent retry or discard also makes
// it, 409 for a retry whose kind and unique key a live job holds, and err
// itself otherwise.
func deadError(err error, id int64) error {
	switch {
	case errors.Is(err, queue.ErrNotDead):
		//nolint:wrapcheck // a problem
		return problem.NotFound.Error(fmt.Sprintf("No dead job %d.", id))
	case errors.Is(err, queue.ErrDuplicate):
		return problem.InvalidState.Error(err.Error()) //nolint:wrapcheck // as above
	}
	return err
}

func deadView(d queue.Dead) operatorapi.DeadJob {
	v := operatorapi.DeadJob{
		Id:         d.ID,
		Queue:      string(d.Queue),
		Kind:       d.Kind,
		Attempt:    int32(min(d.Attempt, 1<<31-1)), //nolint:gosec // clamped
		LastError:  d.LastError,
		CreatedAt:  d.CreatedAt.Unix(),
		FinishedAt: d.FinishedAt.Unix(),
	}
	// A payload the queue stored is valid JSON, so it always decodes.
	_ = json.Unmarshal(d.Payload, &v.Payload)
	if !d.Account.IsZero() {
		a := d.Account.String()
		v.Account = &a
	}
	if d.UniqueKey != "" {
		v.UniqueKey = &d.UniqueKey
	}
	return v
}

// SecretsRotate rotates the data key now, as infraweave secrets rotate
// does, and answers the new active key's ID. A rotation whose old key is
// still retiring answers 409.
func (s operatorServer) SecretsRotate(
	ctx context.Context,
	_ operatorapi.SecretsRotateRequestObject,
) (operatorapi.SecretsRotateResponseObject, error) {
	active, err := s.platform.RotateKey(ctx)
	if errors.Is(err, crypt.ErrRotating) {
		return nil, problem.InvalidState.Error( //nolint:wrapcheck // a problem
			"The previous rotation's data key is still retiring; a scheduler reseals its " +
				"values and then retires it.")
	}
	if err != nil {
		return nil, err
	}
	return operatorapi.SecretsRotate200JSONResponse{
		Body: operatorapi.DataKey{KeyId: int64(active)},
	}, nil
}
