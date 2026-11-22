package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

type publicClaimOwner struct {
	account      kernel.AccountID
	organization kernel.OrganizationID
	stack        kernel.ULID
}

type publicCapacityWorld struct {
	*claimWorld
	pool    kernel.ULID
	owners  []publicClaimOwner
	workers []accessapi.Bearer
	runs    []kernel.ULID
}

// Every scheduler, entitlement, owner fence, registration and token below is
// assembled over real PostgreSQL. Rows seed READY candidates without claiming
// execution; the Docker feature separately proves actual launcher execution.
func newPublicCapacityWorld(t *testing.T, billing bool, workerCount int) *publicCapacityWorld {
	t.Helper()
	cfg := config.Defaults()
	cfg.Worker.PublicPool.Enabled = true
	w := &publicCapacityWorld{claimWorld: newClaimWorldConfig(t, cfg)}
	ctx := t.Context()
	operator := kernel.WithPrincipal(ctx, kernel.Principal{Kind: kernel.PrincipalOperator})
	principal, _ := kernel.PrincipalFrom(w.admin)
	w.exec(t, `INSERT INTO organization_admins (organization_id,member_id,created_by) VALUES($1,$2,'test')`, w.org, principal.MemberID)
	sibling, err := w.d.Access.API().OrganizationAccountCreate(w.admin, api.OrganizationAccountCreateRequestObject{
		Body: &api.OrganizationAccountCreateRequest{Slug: "sibling", Name: "Sibling", Admin: principal.MemberID.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	siblingAccount, err := kernel.ParseAccountID(sibling.(api.OrganizationAccountCreate201JSONResponse).Body.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.Access.OperatorAPI().OrganizationCreate(operator, opapi.OrganizationCreateRequestObject{
		Body: &opapi.OrganizationCreateInput{Slug: "other", Name: "Other", Account: opapi.AccountInput{Slug: "other", Name: "Other"}},
	}); err != nil {
		t.Fatal(err)
	}
	var otherOrg kernel.OrganizationID
	var otherAccount kernel.AccountID
	if err := w.q.QueryRow(ctx, `SELECT id,organization_id FROM accounts WHERE slug='other'`).Scan(&otherAccount, &otherOrg); err != nil {
		t.Fatal(err)
	}
	otherMember := kernel.NewMemberID()
	w.exec(t, `INSERT INTO members (organization_id,id,login,name,email,status) VALUES($1,$2,'bob','Bob','bob@example.invalid','active')`, otherOrg, otherMember)
	organization := kernel.WithPrincipal(ctx, kernel.Principal{Kind: kernel.PrincipalOrganization, OrganizationID: otherOrg, ActorID: kernel.NewULID()})
	if _, err := w.d.Access.API().
		OrganizationAccountClaim(organization, api.OrganizationAccountClaimRequestObject{Account: "other", Body: &api.OrganizationAccountClaimRequest{Admin: otherMember.String()}}); err != nil {
		t.Fatal(err)
	}
	siblingPrincipal := principal
	siblingPrincipal.AccountID = siblingAccount
	otherPrincipal := kernel.Principal{Kind: kernel.PrincipalUser, OrganizationID: otherOrg, AccountID: otherAccount, ActorID: otherMember.ULID, MemberID: otherMember}
	owners := []struct {
		principal kernel.Principal
		stack     string
	}{{principal, "one"}, {siblingPrincipal, "sibling"}, {otherPrincipal, "other"}}
	for i, owner := range owners {
		stack := w.stacks["one"]
		if i != 0 {
			pctx := kernel.WithPrincipal(ctx, owner.principal)
			space := owner.stack
			if _, err := w.d.Stacks.API().StackCreate(pctx, api.StackCreateRequestObject{Body: &api.StackCreateRequest{Input: api.StackInput{
				Name: owner.stack, Repository: "infra", Branch: "main", Space: &space, Provider: new(api.VCSProviderGIT),
			}}}); err != nil {
				t.Fatal(err)
			}
			if err := w.q.QueryRow(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND slug=$2`, owner.principal.AccountID, owner.stack).Scan(&stack); err != nil {
				t.Fatal(err)
			}
		}
		w.owners = append(w.owners, publicClaimOwner{owner.principal.AccountID, owner.principal.OrganizationID, stack})
	}
	features := w.d.Features.OperatorAPI(w.d.Access.OrganizationID)
	for _, grant := range []struct {
		slug  string
		value int64
	}{{"hold", 3}, {"other", 2}} {
		value := grant.value
		if _, err := features.OrganizationLimitGrant(
			operator,
			opapi.OrganizationLimitGrantRequestObject{Organization: grant.slug, Limit: featuresapi.PublicWorkerSlots, Body: &opapi.LimitGrantInput{Value: &value}},
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := features.BillingSet(operator, opapi.BillingSetRequestObject{Body: &opapi.BillingInput{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	for _, ceiling := range []struct {
		account string
		value   int64
	}{{"hold", 1}, {"sibling", 2}} {
		if _, err := w.d.Access.API().
			OrganizationAccountLimitSet(w.admin, api.OrganizationAccountLimitSetRequestObject{Account: ceiling.account, Limit: featuresapi.PublicWorkerSlots, Body: &api.OrganizationAccountLimitRequest{Value: ceiling.value}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.d.Access.API().
		OrganizationAccountLimitSet(w.admin, api.OrganizationAccountLimitSetRequestObject{Account: "hold", Limit: featuresapi.PublicWorkerSlots, Body: &api.OrganizationAccountLimitRequest{Value: 4}}); err == nil {
		t.Fatal("account ceiling expanded the organization grant")
	}
	if !billing {
		if _, err := features.BillingSet(operator, opapi.BillingSetRequestObject{Body: &opapi.BillingInput{Enabled: false}}); err != nil {
			t.Fatal(err)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "public-workers"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	created, err := w.d.Workers.OperatorAPI().
		PublicWorkerPoolCreate(operator, opapi.PublicWorkerPoolCreateRequestObject{Body: &opapi.PublicWorkerPoolCreateRequest{CertificateSigningRequest: base64.StdEncoding.EncodeToString(csr)}})
	if err != nil {
		t.Fatal(err)
	}
	credential := created.(opapi.PublicWorkerPoolCreate201JSONResponse).Body
	w.pool, err = kernel.ParseULID(credential.Id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(credential.Config)
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		Certificate string `json:"certificate"`
	}
	if err := json.Unmarshal(raw, &token); err != nil {
		t.Fatal(err)
	}
	certificate, _ := pem.Decode([]byte(token.Certificate))
	if certificate == nil {
		t.Fatal("pool certificate missing")
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: key},
		new(jose.SignerOptions).WithHeader("x5c", []string{base64.StdEncoding.EncodeToString(certificate.Bytes)}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for range workerCount {
		body, err := json.Marshal(
			map[string]any{
				"pool":         w.pool.String(),
				"iat":          time.Now().Unix(),
				"jti":          kernel.NewULID().String(),
				"version":      "1.0.0",
				"os":           "linux",
				"arch":         "amd64",
				"capabilities": []string{"plan-sanitization-v1", "public-pool-v1"},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := signer.Sign(body)
		if err != nil {
			t.Fatal(err)
		}
		proof, err := signed.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		registered, err := w.d.Workers.Protocol().WorkerRegister(ctx, workerapi.WorkerRegisterRequestObject{Body: &workerapi.RegisterRequest{Registration: proof}})
		if err != nil {
			t.Fatal(err)
		}
		registration := registered.(workerapi.WorkerRegister200JSONResponse).Body
		bearer, err := w.d.Access.Tokens().VerifyWorker(ctx, registration.SessionToken)
		if err != nil {
			t.Fatal(err)
		}
		w.workers = append(w.workers, bearer)
	}
	for _, owner := range w.owners {
		for i := range 6 {
			id := kernel.NewULID()
			w.exec(t, `INSERT INTO runs (account_id,organization_id,id,stack_id,type,state,commit,triggered_by,priority,priority_tier,created_at)
    VALUES($1,$2,$3,$4,'PROPOSED','READY','{"hash":"abc"}','alice','normal',$5,$6)`, owner.account, owner.organization, id, owner.stack, kernel.RunTier(kernel.RunPriorityNormal, kernel.RunTypeProposed, false), time.Now().Add(time.Duration(i)*time.Second))
			w.runs = append(w.runs, id)
		}
	}
	return w
}

func (w *publicCapacityWorld) pollPublic(ctx context.Context, replica int, b accessapi.Bearer) polled {
	wait := int32(5)
	response, err := w.replicas[replica%len(w.replicas)].Workers.Protocol().
		WorkerPoll(accessapi.WithBearer(ctx, b), workerapi.WorkerPollRequestObject{Body: &workerapi.PollRequest{Wait: &wait}})
	if err != nil {
		return polled{err: err}
	}
	ok, valid := response.(workerapi.WorkerPoll200JSONResponse)
	if !valid {
		return polled{err: fmt.Errorf("unexpected public poll response %T", response)}
	}
	return polled{a: ok.Body.Assignment}
}

func TestAccPublicCapacityConcurrentAccountsAndOrganizations(t *testing.T) {
	for _, billing := range []bool{true, false} {
		t.Run(map[bool]string{true: "pooled grants and lower account ceilings", false: "billing off"}[billing], func(t *testing.T) {
			w := newPublicCapacityWorld(t, billing, 50)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			out := make([]polled, len(w.workers))
			start := make(chan struct{})
			var group sync.WaitGroup
			for i, b := range w.workers {
				group.Go(func() { <-start; out[i] = w.pollPublic(ctx, i, b) })
			}
			close(start)
			group.Wait()
			seen := map[string]bool{}
			for _, result := range out {
				if result.err != nil {
					t.Fatal(result.err)
				}
				if result.a == nil {
					continue
				}
				if seen[result.a.Run] {
					t.Fatal("a public run was assigned twice")
				}
				seen[result.a.Run] = true
			}
			expected := 5
			if !billing {
				expected = len(w.runs)
			}
			if len(seen) != expected {
				t.Fatalf("public assignments=%d, want %d", len(seen), expected)
			}
			for i, owner := range w.owners {
				var assigned int
				if err := w.q.QueryRow(ctx, `SELECT count(*) FROM runs WHERE account_id=$1 AND state='PREPARING' AND public_worker_pool_id=$2 AND worker_id IS NOT NULL`, owner.account, w.pool).
					Scan(&assigned); err != nil {
					t.Fatal(err)
				}
				want := []int{1, 2, 2}[i]
				if !billing {
					want = 6
				}
				if assigned != want {
					t.Fatalf("account %d public use=%d, want %d", i, assigned, want)
				}
			}
		})
	}
}

func (w *publicCapacityWorld) oneSlot(t *testing.T) {
	t.Helper()
	operator := kernel.WithPrincipal(t.Context(), kernel.Principal{Kind: kernel.PrincipalOperator})
	for _, grant := range []struct {
		slug  string
		value int64
	}{{"hold", 1}, {"other", 0}} {
		value := grant.value
		if _, err := w.d.Features.OperatorAPI(w.d.Access.OrganizationID).OrganizationLimitGrant(operator, opapi.OrganizationLimitGrantRequestObject{
			Organization: grant.slug, Limit: featuresapi.PublicWorkerSlots, Body: &opapi.LimitGrantInput{Value: &value},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func (w *publicCapacityWorld) active(t *testing.T) int {
	t.Helper()
	var states []string
	for _, state := range kernel.RunStates() {
		if state.OnWorker() {
			states = append(states, string(state))
		}
	}
	var count int
	if err := w.q.QueryRow(t.Context(), `SELECT count(*) FROM runs WHERE public_worker_pool_id=$1 AND state=ANY($2)`, w.pool, states).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAccPublicAssignmentFailureConsumesNoCapacity(t *testing.T) {
	w := newPublicCapacityWorld(t, true, 2)
	w.exec(t, `ALTER TABLE runs ADD CONSTRAINT refuse_assignment CHECK(worker_id IS NULL)`)
	if got := w.pollPublic(t.Context(), 0, w.workers[0]); got.err == nil || got.a != nil {
		t.Fatal("failed public assignment was returned as successful")
	}
	var changed, busy int
	if err := w.q.QueryRow(t.Context(), `SELECT count(*) FROM runs WHERE state!='READY' OR worker_id IS NOT NULL OR public_worker_pool_id IS NOT NULL`).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if err := w.q.QueryRow(t.Context(), `SELECT count(*) FROM workers WHERE busy`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if changed != 0 || busy != 0 || w.active(t) != 0 {
		t.Fatalf("failed assignment committed partial capacity: changed=%d busy=%d", changed, busy)
	}
	w.exec(t, `ALTER TABLE runs DROP CONSTRAINT refuse_assignment`)
	if got := w.pollPublic(t.Context(), 1, w.workers[0]); got.err != nil || got.a == nil {
		t.Fatalf("rolled-back assignment was not retryable: %v", got.err)
	}
	if got := w.active(t); got != 1 {
		t.Fatalf("retry used %d slots", got)
	}
}

func TestAccPublicSelectionExcludesIneligibleReadyRows(t *testing.T) {
	w := newPublicCapacityWorld(t, true, 2)
	// Existing rows remain unclaimable even if a legacy writer bypasses creation
	// admission. A public worker cannot fall back to a private execution pool.
	private := kernel.NewULID()
	w.exec(t, `UPDATE runs SET worker_pool_id=$1`, private)
	for _, id := range w.runs[:4] {
		w.exec(t, `UPDATE runs SET worker_pool_id=NULL WHERE id=$1`, id)
	}
	w.exec(t, `UPDATE runs SET priority='high' WHERE id=$1`, w.runs[0])
	w.exec(t, `UPDATE runs SET drift_detection=true WHERE id=$1`, w.runs[1])
	w.exec(t, `UPDATE runs SET public_worker_pool_id=$1 WHERE id=$2`, kernel.NewULID(), w.runs[2])
	got := w.pollPublic(t.Context(), 0, w.workers[0])
	if got.err != nil || got.a == nil || got.a.Run != w.runs[3].String() {
		t.Fatalf("public poll did not select the eligible normal run: %v", got.err)
	}
	if next := w.pollPublic(t.Context(), 1, w.workers[1]); next.err != nil || next.a != nil {
		t.Fatalf("public poll admitted an ineligible run: %v", next.err)
	}
	var assigned int
	if err := w.q.QueryRow(t.Context(), `SELECT count(*) FROM runs WHERE worker_id IS NOT NULL`).Scan(&assigned); err != nil || assigned != 1 {
		t.Fatalf("assigned=%d, want only one eligible run: %v", assigned, err)
	}
}

func TestAccPublicTerminalAndWorkerLossReleaseCapacityAcrossReplicas(t *testing.T) {
	w := newPublicCapacityWorld(t, true, 2)
	w.oneSlot(t)
	first := w.pollPublic(t.Context(), 0, w.workers[0])
	if first.err != nil || first.a == nil {
		t.Fatalf("initial assignment: %v", first.err)
	}
	if got := w.pollPublic(t.Context(), 1, w.workers[1]); got.err != nil || got.a != nil {
		t.Fatalf("saturated pool admitted work: %v", got.err)
	}
	bearer, err := w.d.Access.Tokens().VerifyRun(t.Context(), first.a.RunToken)
	if err != nil {
		t.Fatal(err)
	}
	request := workerapi.WorkerRunReportRequestObject{
		Run:  first.a.Run,
		Body: &workerapi.RunReport{State: workerapi.WorkerState(first.a.State), StateVersion: first.a.StateVersion, Event: "failed"},
	}
	reportContext := accessapi.WithBearer(t.Context(), bearer)
	if _, err := w.replicas[1].Workers.Protocol().WorkerRunReport(reportContext, request); err != nil {
		t.Fatal(err)
	}
	if got := w.active(t); got != 0 {
		t.Fatalf("terminal report retained %d slots", got)
	}
	if _, err := w.replicas[0].Workers.Protocol().WorkerRunReport(reportContext, request); err == nil {
		t.Fatal("an ended execution accepted another failed report")
	}
	second := w.pollPublic(t.Context(), 1, w.workers[1])
	if second.err != nil || second.a == nil || second.a.Run == first.a.Run {
		t.Fatalf("terminal release did not admit waiting work: %v", second.err)
	}
	// An old report cannot release the replacement run's slot.
	_, _ = w.replicas[1].Workers.Protocol().WorkerRunReport(reportContext, request)
	if got := w.active(t); got != 1 {
		t.Fatalf("duplicate terminal report changed capacity to %d", got)
	}
	w.exec(t, `UPDATE workers SET last_seen_at=now()-interval '2 hours' WHERE id=$1`, w.workers[1].ID)
	var group sync.WaitGroup
	failures := make(chan error, 2)
	for _, replica := range w.replicas {
		group.Go(func() { failures <- replica.Workers.Cron()[0].Handler(t.Context(), queue.Job{}) })
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := w.active(t); got != 0 {
		t.Fatalf("worker loss retained %d slots", got)
	}
	var failed int
	lostRun, err := kernel.ParseULID(second.a.Run)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.q.QueryRow(t.Context(), `SELECT count(*) FROM run_state_transitions WHERE run_id=$1 AND state='FAILED'`, lostRun).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("worker loss terminal transitions=%d error=%v", failed, err)
	}
	third := w.pollPublic(t.Context(), 0, w.workers[0])
	if third.err != nil || third.a == nil {
		t.Fatalf("worker loss did not release waiting work: %v", third.err)
	}
	if got := w.active(t); got != 1 {
		t.Fatalf("post-loss assignment used %d slots", got)
	}
}

func TestAccPublicProposedPlanReplayPreservesCommittedRelease(t *testing.T) {
	w := newPublicCapacityWorld(t, true, 2)
	w.oneSlot(t)
	first := w.pollPublic(t.Context(), 0, w.workers[0])
	if first.err != nil || first.a == nil {
		t.Fatalf("initial assignment: %v", first.err)
	}
	bearer, err := w.d.Access.Tokens().VerifyRun(t.Context(), first.a.RunToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx := accessapi.WithBearer(t.Context(), bearer)
	version := first.a.StateVersion
	for _, phase := range []struct {
		state workerapi.WorkerState
		event workerapi.RunReportEvent
	}{{"INITIALIZING", "started"}, {"INITIALIZING", "succeeded"}} {
		response, err := w.replicas[0].Workers.Protocol().
			WorkerRunReport(ctx, workerapi.WorkerRunReportRequestObject{Run: first.a.Run, Body: &workerapi.RunReport{State: phase.state, StateVersion: version, Event: phase.event}})
		if err != nil {
			t.Fatal(err)
		}
		version = response.(workerapi.WorkerRunReport200JSONResponse).Body.StateVersion
	}
	request := workerapi.WorkerRunReportRequestObject{
		Run:  first.a.Run,
		Body: &workerapi.RunReport{State: "PLANNING", StateVersion: version, Event: "succeeded", Delta: &workerapi.PlanDelta{}},
	}
	response, err := w.replicas[0].Workers.Protocol().WorkerRunReport(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.(workerapi.WorkerRunReport200JSONResponse).Body.Action != "exit" || w.active(t) != 0 {
		t.Fatal("proposed plan did not release its public slot")
	}
	// This is the same committed report, retried after its answer was lost.
	if _, err := w.replicas[1].Workers.Protocol().WorkerRunReport(ctx, request); err != nil {
		t.Fatalf("public committed plan replay refused: %v", err)
	}
	if next := w.pollPublic(t.Context(), 1, w.workers[1]); next.err != nil || next.a == nil {
		t.Fatalf("plan completion did not admit waiting public work: %v", next.err)
	}
	if _, err := w.replicas[0].Workers.Protocol().WorkerRunReport(ctx, request); err != nil {
		t.Fatal(err)
	}
	if got := w.active(t); got != 1 {
		t.Fatalf("plan replay changed replacement capacity to %d", got)
	}
}
