package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// deadJobs is DeadJobs in memory: jobs are the dead ones, in ID order, and
// every call fails with err when it is set.
type deadJobs struct {
	jobs  []queue.Dead
	calls []string
	dup   bool
	err   error
}

func (d *deadJobs) ListDead(_ context.Context, after int64, limit int) ([]queue.Dead, error) {
	var out []queue.Dead
	for _, j := range d.jobs {
		if j.ID > after && len(out) < limit {
			out = append(out, j)
		}
	}
	return out, d.err
}

func (d *deadJobs) ReadDead(_ context.Context, id int64) (queue.Dead, error) {
	if d.err != nil {
		return queue.Dead{}, d.err
	}
	i := slices.IndexFunc(d.jobs, func(j queue.Dead) bool { return j.ID == id })
	if i < 0 {
		return queue.Dead{}, queue.ErrNotDead
	}
	return d.jobs[i], nil
}

func (d *deadJobs) Retry(_ context.Context, id int64) error {
	d.calls = append(d.calls, fmt.Sprint("retry ", id))
	if d.dup {
		return queue.ErrDuplicate
	}
	return d.err
}

func (d *deadJobs) Discard(_ context.Context, id int64) error {
	d.calls = append(d.calls, fmt.Sprint("discard ", id))
	return d.err
}

func operatorAPI(p Platform) http.Handler {
	c := config.Defaults().Server
	return API(discard(), c, memory(c), Domains{OperatorAuth: byAddress{}}, p, Blobs{})
}

func TestUnitDeadJobs(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	acct := kernel.NewULID()
	d := &deadJobs{jobs: []queue.Dead{
		{
			ID: 3, Queue: queue.Default, Kind: "test.work", Payload: json.RawMessage(`{"i":1}`),
			Account: acct, UniqueKey: "k", Attempt: 5, LastError: "broken",
			CreatedAt: at, FinishedAt: at.Add(time.Minute),
		},
		{
			ID:         7,
			Queue:      queue.Maintenance,
			Kind:       "secrets.reseal",
			Payload:    json.RawMessage(`null`),
			CreatedAt:  at,
			FinishedAt: at,
		},
	}}
	h := operatorAPI(Platform{Dead: d})

	rec := request(t, h, http.MethodGet, OperatorBaseURL+"/queue/jobs?limit=1")
	var first operatorapi.DeadJobPage
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil || rec.Code != http.StatusOK ||
		len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page %d %s", rec.Code, rec.Body)
	}
	j := first.Items[0]
	if j.Id != 3 || j.Queue != "default" || j.Kind != "test.work" ||
		fmt.Sprint(j.Payload) != "map[i:1]" ||
		j.Account == nil || *j.Account != acct.String() || j.UniqueKey == nil || *j.UniqueKey != "k" ||
		j.Attempt != 5 || j.LastError != "broken" || j.CreatedAt != at.Unix() ||
		j.FinishedAt != at.Add(time.Minute).Unix() {
		t.Errorf("dead job %+v", j)
	}
	rec = request(t, h, http.MethodGet, OperatorBaseURL+"/queue/jobs?cursor="+*first.NextCursor)
	var rest operatorapi.DeadJobPage
	if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil || len(rest.Items) != 1 ||
		rest.Items[0].Id != 7 || rest.Items[0].Account != nil || rest.Items[0].UniqueKey != nil ||
		rest.NextCursor != nil {
		t.Errorf("second page %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, request(t, h, http.MethodGet, OperatorBaseURL+"/queue/jobs?cursor=nope"),
		http.StatusBadRequest, "")

	if rec := request(
		t,
		h,
		http.MethodGet,
		OperatorBaseURL+"/queue/jobs/7",
	); rec.Code != http.StatusOK {
		t.Errorf("read %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, request(t, h, http.MethodGet, OperatorBaseURL+"/queue/jobs/8"),
		http.StatusNotFound, "No dead job 8.")
	if rec := request(
		t,
		h,
		http.MethodPost,
		OperatorBaseURL+"/queue/jobs/3/retry",
	); rec.Code != http.StatusOK {
		t.Errorf("retry %d %s", rec.Code, rec.Body)
	}
	if rec := request(
		t,
		h,
		http.MethodDelete,
		OperatorBaseURL+"/queue/jobs/7",
	); rec.Code != http.StatusOK {
		t.Errorf("discard %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(d.calls, []string{"retry 3", "discard 7"}) {
		t.Errorf("calls %v", d.calls)
	}
	wantProblem(t, request(t, h, http.MethodPost, OperatorBaseURL+"/queue/jobs/8/retry"),
		http.StatusNotFound, "No dead job 8.")
	d.dup = true
	wantProblem(t, request(t, h, http.MethodPost, OperatorBaseURL+"/queue/jobs/3/retry"),
		http.StatusConflict, "")

	d.err = fmt.Errorf("jobs: %w", kernel.ErrUnavailable)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/queue/jobs"},
		{http.MethodGet, "/queue/jobs/3"},
		{http.MethodDelete, "/queue/jobs/3"},
	} {
		if rec := request(
			t,
			h,
			r.method,
			OperatorBaseURL+r.path,
		); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with the queue down = %d", r.method, r.path, rec.Code)
		}
	}
}

func TestUnitSecretsRotate(t *testing.T) {
	var err error
	h := operatorAPI(Platform{RotateKey: func(context.Context) (uint32, error) { return 4, err }})
	rec := request(t, h, http.MethodPost, OperatorBaseURL+"/secrets/rotate")
	var k operatorapi.DataKey
	if e := json.Unmarshal(
		rec.Body.Bytes(),
		&k,
	); e != nil || rec.Code != http.StatusOK ||
		k.KeyId != 4 {
		t.Errorf("rotate = %d %s", rec.Code, rec.Body)
	}
	err = fmt.Errorf("rotate: %w", crypt.ErrRotating)
	wantProblem(t, request(t, h, http.MethodPost, OperatorBaseURL+"/secrets/rotate"),
		http.StatusConflict, "")
	err = errors.New("keyring broken")
	if rec := request(t, h, http.MethodPost, OperatorBaseURL+"/secrets/rotate"); rec.Code !=
		http.StatusInternalServerError {
		t.Errorf("a broken keyring = %d %s", rec.Code, rec.Body)
	}
}
