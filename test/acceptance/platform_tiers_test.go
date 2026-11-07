package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// platformSteps are the steps of the operator API's tiers, grants, billing
// and dead jobs, on authSteps' servers and named tokens.
type platformSteps struct {
	a *authSteps
	q *queueSteps
	// dead is the dead job the scenario listed.
	dead int64
}

func (p *platformSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)" creates the tier "([^"]+)" with the feature "([a-z-]+)"(?: and (\d+) seats)? through ([AB])$`,
		p.createTier,
	)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) with token "(\w+)" eventually answers '([^']*)'$`,
		p.eventuallyJSON,
	)
	sc.Step(
		`^the tier read through ([AB]) with token "(\w+)" is the tier "([^"]+)" as token "(\w+)" reads it through ([AB])$`,
		p.tierAsOperator,
	)
	sc.Step(
		`^token "(\w+)" gives the home organization the tier "([^"]+)" through ([AB])$`,
		p.setTier,
	)
	sc.Step(`^token "(\w+)" turns billing on through ([AB])$`, p.billingOn)
	sc.Step(`^token "(\w+)" grants the home organization the feature "([a-z-]+)" through ([AB])$`,
		p.grant)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) with token "(\w+)" eventually answers a (\d+) problem "([a-z-]+)"$`,
		p.eventuallyProblem,
	)
	sc.Step(`^"([A-Z]+) ([^"]+)" through ([AB]) with token "(\w+)" eventually answers (\d+)$`,
		p.eventuallyAnswers)
	sc.Step(
		`^two infraweave all instances, A and B, on one database with a (\d+)-second queue lease and an operator key$`,
		p.instances,
	)
	sc.Step(
		`^the dead jobs listed through ([AB]) with token "(\w+)" are the newest "([a-z.]+)" job alone, naming "([^"]+)"$`,
		p.listedDead,
	)
	sc.Step(`^the dead jobs listed through ([AB]) with token "(\w+)" are none$`, p.noneDead)
	sc.Step(`^the idempotency_keys table is renamed back$`, p.renameBack)
	sc.Step(`^token "(\w+)" (retries|deletes) the dead job through ([AB])$`, p.actOnDead)
	sc.Step(`^the retried job eventually succeeds$`, p.retriedSucceeds)
	sc.Step(
		`^reading the dead job through ([AB]) with token "(\w+)" answers a (\d+) problem "([a-z-]+)"$`,
		p.readDead,
	)
}

// ok sends a request and fails unless it answers want.
func (p *platformSteps) ok(
	ctx context.Context,
	replica, tok, method, path string,
	body any,
	want int,
) (response, error) {
	r, err := p.a.do(ctx, replica, tok, method, path, body)
	if err != nil {
		return r, err
	}
	if r.status != want {
		return r, fmt.Errorf("%w: %s %s answered %d, want %d: %s", errStep, method, path, r.status,
			want, r.body)
	}
	return r, nil
}

// createTier creates a tier of one feature and, when seats is set, that
// many seats.
func (p *platformSteps) createTier(
	ctx context.Context,
	tok, name, feature, seats, replica string,
) error {
	limits := map[string]int64{}
	if seats != "" {
		n, err := strconv.ParseInt(seats, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: seats %q: %w", errStep, seats, err)
		}
		limits["seats"] = n
	}
	_, err := p.ok(ctx, replica, tok, http.MethodPost, "/operator/v1/tiers", map[string]any{
		"name": name, "features": []string{feature}, "limits": limits,
	}, http.StatusCreated)
	return err
}

// eventuallyJSON waits for a replica to answer 200 with want, as
// eventuallyProblem waits for a problem.
func (p *platformSteps) eventuallyJSON(
	ctx context.Context,
	method, path, replica, tok, want string,
) error {
	return Eventually(ctx, method+" "+path+" to answer "+want,
		func(ctx context.Context) (bool, error) {
			r, err := p.a.do(ctx, replica, tok, method, path, nil)
			if err != nil {
				return false, err
			}
			return answered(r, method+" "+path, want) == nil, nil
		})
}

// tierAsOperator checks that the account's tier read is the tier name as
// the operator API reads it: its name, features and limits.
func (p *platformSteps) tierAsOperator(
	ctx context.Context,
	replica, tok, name, opTok, opReplica string,
) error {
	type tier struct {
		Name     string           `json:"name"`
		Features []string         `json:"features"`
		Limits   map[string]int64 `json:"limits"`
	}
	var got, want tier
	for _, read := range []struct {
		replica, tok, path string
		into               *tier
	}{
		{replica, tok, "/api/v1/tier", &got},
		{opReplica, opTok, "/operator/v1/tiers/" + name, &want},
	} {
		r, err := p.ok(ctx, read.replica, read.tok, http.MethodGet, read.path, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(r.body, read.into); err != nil {
			return fmt.Errorf("%w: %s answered %s: %w", errStep, read.path, r.body, err)
		}
	}
	if want.Name != name || !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%w: the tier read is %+v, want %+v", errStep, got, want)
	}
	return nil
}

func (p *platformSteps) setTier(ctx context.Context, tok, tier, replica string) error {
	_, err := p.ok(ctx, replica, tok, http.MethodPut,
		"/operator/v1/organizations/"+home.orgSlug+"/tier", map[string]string{"tier": tier},
		http.StatusOK)
	return err
}

func (p *platformSteps) billingOn(ctx context.Context, tok, replica string) error {
	_, err := p.ok(ctx, replica, tok, http.MethodPut, "/operator/v1/billing",
		map[string]bool{"enabled": true}, http.StatusOK)
	return err
}

func (p *platformSteps) grant(ctx context.Context, tok, feature, replica string) error {
	_, err := p.ok(ctx, replica, tok, http.MethodPut,
		"/operator/v1/organizations/"+home.orgSlug+"/grants/features/"+feature, nil, http.StatusOK)
	return err
}

// eventuallyProblem waits for a replica to answer the problem: a replica
// drops what it holds when infraweave_invalidate tells it to, which lands
// after the write that sends it has answered.
func (p *platformSteps) eventuallyProblem(
	ctx context.Context,
	method, path, replica, tok, code, slug string,
) error {
	return Eventually(
		ctx,
		method+" "+path+" to answer "+code,
		func(ctx context.Context) (bool, error) {
			r, err := p.a.do(ctx, replica, tok, method, path, nil)
			if err != nil {
				return false, err
			}
			return isProblem(r, method+" "+path, code, slug) == nil, nil
		},
	)
}

func (p *platformSteps) eventuallyAnswers(
	ctx context.Context,
	method, path, replica, tok string,
	code int,
) error {
	return Eventually(ctx, method+" "+path+" to answer "+strconv.Itoa(code),
		func(ctx context.Context) (bool, error) {
			r, err := p.a.do(ctx, replica, tok, method, path, nil)
			return err == nil && r.status == code, err
		})
}

// instances starts the queue scenario's two all instances with the
// operator key in auth.operator.keys.
func (p *platformSteps) instances(ctx context.Context, lease int) error {
	p.a.s.w.auth = operatorAuth()
	return p.q.twoInstances(ctx, lease)
}

// deadJobs lists the dead jobs through replica.
func (p *platformSteps) deadJobs(ctx context.Context, replica, tok string) ([]deadJob, error) {
	r, err := p.ok(ctx, replica, tok, http.MethodGet, "/operator/v1/queue/jobs", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var page struct {
		Items []deadJob `json:"items"`
	}
	if err := json.Unmarshal(r.body, &page); err != nil {
		return nil, fmt.Errorf("%w: the dead jobs answered %s: %w", errStep, r.body, err)
	}
	return page.Items, nil
}

type deadJob struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Attempt   int    `json:"attempt"`
	LastError string `json:"lastError"`
}

func (p *platformSteps) listedDead(ctx context.Context, replica, tok, kind, naming string) error {
	jobs, err := p.deadJobs(ctx, replica, tok)
	if err != nil {
		return err
	}
	newest, err := p.q.number(ctx, fmt.Sprintf(
		`SELECT max(id) FROM jobs WHERE kind = %s`, literal(kind)))
	if err != nil {
		return err
	}
	if len(jobs) != 1 || jobs[0].ID != int64(newest) || jobs[0].Kind != kind ||
		!strings.Contains(jobs[0].LastError, naming) {
		return fmt.Errorf("%w: the dead jobs are %+v, want job %d of %s naming %q", errStep, jobs,
			newest, kind, naming)
	}
	p.dead = jobs[0].ID
	return nil
}

func (p *platformSteps) noneDead(ctx context.Context, replica, tok string) error {
	jobs, err := p.deadJobs(ctx, replica, tok)
	if err == nil && len(jobs) != 0 {
		err = fmt.Errorf("%w: the dead jobs are %+v, want none", errStep, jobs)
	}
	return err
}

func (p *platformSteps) renameBack(ctx context.Context) error {
	_, err := p.a.s.query(ctx, `ALTER TABLE idempotency_keys_away RENAME TO idempotency_keys`)
	return err
}

// actOnDead retries or deletes the listed dead job, which answers it as
// it was.
func (p *platformSteps) actOnDead(ctx context.Context, tok, act, replica string) error {
	path := "/operator/v1/queue/jobs/" + strconv.FormatInt(p.dead, 10)
	method := http.MethodDelete
	if act == "retries" {
		method, path = http.MethodPost, path+"/retry"
	}
	r, err := p.ok(ctx, replica, tok, method, path, nil, http.StatusOK)
	if err != nil {
		return err
	}
	var j deadJob
	if err := json.Unmarshal(r.body, &j); err != nil || j.ID != p.dead {
		return fmt.Errorf("%w: %s answered %s, want dead job %d", errStep, path, r.body, p.dead)
	}
	return nil
}

func (p *platformSteps) retriedSucceeds(ctx context.Context) error {
	return Eventually(ctx, "the retried job to succeed", func(ctx context.Context) (bool, error) {
		out, err := p.a.s.query(ctx, fmt.Sprintf(`SELECT state FROM jobs WHERE id = %d`, p.dead))
		return strings.TrimSpace(out) == "succeeded", err
	})
}

func (p *platformSteps) readDead(ctx context.Context, replica, tok, code, slug string) error {
	path := "/operator/v1/queue/jobs/" + strconv.FormatInt(p.dead, 10)
	r, err := p.a.do(ctx, replica, tok, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return isProblem(r, "GET "+path, code, slug)
}
