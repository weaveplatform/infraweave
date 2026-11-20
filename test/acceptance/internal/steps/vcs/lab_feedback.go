package vcs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	sourcelab "github.com/weaveplatform/infraweave/internal/vcs/vcslab"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	lab "github.com/weaveplatform/infraweave/test/acceptance/internal/vcslab"
	"github.com/weaveplatform/infraweave/test/deps"
)

type liveLabSteps struct {
	w                                                     *harness.World
	ingress                                               *lab.Ingress
	native                                                *lab.Native
	lease                                                 *lab.HookLease
	hooks                                                 *lab.TerraformHooks
	secret                                                crypt.Value
	provider, integration, stack, branch, tracked, public string
	request                                               int
	pending                                               map[string]string
}

func (s *liveLabSteps) register(sc *godog.ScenarioContext) {
	s.pending = map[string]string{}
	sc.Before(s.before)
	sc.After(s.after)
	sc.Step(`^a live "(github|gitlab|bitbucket-cloud|bitbucket-dc|azure-devops)" lab integration and stack with plan comments "(enabled|disabled)"$`, s.setup)
	sc.Step(`^the live lab pushes a "(TRACKED|PROPOSED)" commit$`, s.push)
	sc.Step(`^the live signed push creates exactly one "(TRACKED|PROPOSED)" run kept as "(\w+)"$`, s.run)
	sc.Step(`^the live native check for run "(\w+)" is pending before its worker reports$`, s.pendingCheck)
	sc.Step(`^the live native check for run "(\w+)" reaches success in its original resource$`, s.successCheck)
	sc.Step(`^the live lab opens its pull request without another run$`, s.open)
	sc.Step(`^the live request has the current pushed head before planning completes$`, s.requestHead)
	sc.Step(`^the live request has exactly one native plan comment for run "(\w+)"$`, s.planComment)
	sc.Step(`^the live request keeps both runs "(\w+)" and "(\w+)" with its earlier GitHub plan minimized$`, s.previousComment)
	sc.Step(`^an ordinary live pull request comment is stored and starts no run$`, s.ordinaryComment)
	s.registerPolicies(sc)
}

func (s *liveLabSteps) before(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
	if !slices.Contains(s.w.Tags, "@vcs-lab") {
		return ctx, nil
	}
	s.public = os.Getenv("INFRAWEAVE_LAB_PUBLIC_URL")
	if s.public == "" {
		return ctx, nil
	}
	address := os.Getenv("INFRAWEAVE_LAB_WEBHOOK_LISTEN")
	if address == "" {
		return ctx, fmt.Errorf("%w: lab ingress is not configured", harness.ErrStep)
	}
	in, err := lab.StartIngress(ctx, address)
	if err != nil {
		return ctx, err
	}
	s.ingress = in
	s.w.Settings["server"] = map[string]any{"listen": "127.0.0.1:0", "admin_listen": "127.0.0.1:0", "public_url": s.public, "shutdown": map[string]any{"ready_delay": "0s"}}
	return ctx, nil
}

func (s *liveLabSteps) setup(ctx context.Context, provider, comments string) error {
	if s.ingress == nil || s.w.Relay == nil {
		return fmt.Errorf("%w: live lab requires its ingress and genuine launcher", harness.ErrStep)
	}
	if err := s.ingress.Attach(s.w.Base("A")); err != nil {
		return err
	}
	l, err := sourcelab.FromEnv(sourcelab.EnvNamespace, sourcelab.EnvRepository, sourcelab.EnvToken)
	if err != nil {
		return err
	}
	s.provider = provider
	body := map[string]any{"customInput": map[string]any{"name": "Live lab", "spaceID": "acme", "isDefault": true, "vcsChecks": "INDIVIDUAL"}}
	path, kind := "", ""
	var key lab.AppKey
	switch provider {
	case "github":
		key, err = lab.DeriveAppKey(l.User, []byte(l.Token))
		if err != nil {
			return err
		}
		path, kind = "/api/v1/github-enterprise-integrations", "GITHUB"
		body["apiHost"], body["appID"], body["privateKey"], body["webhookSecret"] = "https://api.github.com", l.User, l.Token, key.Secret()
		body["userFacingHost"] = "https://github.com"
	case "gitlab":
		path, kind = "/api/v1/gitlab-integrations", "GITLAB"
		body["apiHost"], body["privateToken"] = l.Host, l.Token
	case "bitbucket-cloud":
		path, kind = "/api/v1/bitbucket-cloud-integrations", "BITBUCKET_CLOUD"
		body["email"], body["apiToken"] = l.User, l.Token
	case "bitbucket-dc":
		path, kind = "/api/v1/bitbucket-datacenter-integrations", "BITBUCKET_DATACENTER"
		body["apiHost"], body["userFacingHost"], body["username"], body["accessToken"] = l.Host, l.Host, l.User, l.Token
	case "azure-devops":
		path, kind = "/api/v1/azure-dev-ops-repo-integrations", "AZURE_DEVOPS"
		body["organizationURL"], body["personalAccessToken"] = l.Host, l.Token
	}
	response, err := s.w.Do(ctx, "A", "h", http.MethodPost, path, body)
	if err != nil {
		return fmt.Errorf("%w: create live integration status%d", harness.ErrStep, response.Status)
	}
	var integration struct {
		ID       string `json:"id"`
		URL      string `json:"webhookUrl"`
		Secret   string `json:"webhookSecret"`
		Password string `json:"webhookPassword"`
	}
	if response.Status != 201 || json.Unmarshal(response.Body, &integration) != nil || integration.ID == "" || integration.URL == "" {
		return fmt.Errorf("%w: live integration did not expose its admin hook configuration", harness.ErrStep)
	}
	secret := integration.Secret
	if provider == "azure-devops" {
		secret = integration.Password
	}
	if secret == "" {
		return fmt.Errorf("%w: live integration has no signing material", harness.ErrStep)
	}
	s.integration, s.secret = integration.ID, crypt.NewValue([]byte(secret))
	s.w.IDs["liveIntegration"] = integration.ID
	s.w.IDs["liveProvider"] = kind
	s.w.IDs["liveNamespace"] = l.Namespace
	s.w.IDs["liveOther"] = l.Other
	directory := os.Getenv("INFRAWEAVE_LAB_HOOK_JOURNALS")
	if directory == "" {
		return fmt.Errorf("%w: private lab hook journal is not configured", harness.ErrStep)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("%w: create lab journal directory", harness.ErrStep)
	}
	if provider == "github" {
		client, err := lab.NewAppHooks(l.User, []byte(l.Token))
		if err != nil {
			return err
		}
		parked, err := lab.ParkingConfig(os.Getenv("INFRAWEAVE_LAB_GITHUB_PARKED_URL"))
		if err != nil {
			return err
		}
		s.lease, err = lab.LeaseHook(
			ctx,
			client,
			key,
			os.Getenv("INFRAWEAVE_LAB_GITHUB_READY"),
			parked,
			integration.URL,
			filepath.Join(directory, "app-hook-"+kernel.NewULID().String()+".json"),
		)
		if err != nil {
			return err
		}
	} else {
		s.hooks = &lab.TerraformHooks{Root: os.Getenv("INFRAWEAVE_LAB_TERRAFORM_ROOT"), Directory: directory}
		if s.hooks.Root == "" {
			return fmt.Errorf("%w: lab Terraform root is missing", harness.ErrStep)
		}
		if err := s.hooks.Set(ctx, integration.URL, secret); err != nil {
			return err
		}
	}
	nativeToken := l.Token
	if provider == "github" {
		nativeToken = os.Getenv("SEED_TOKEN")
	}
	s.native, err = lab.NewNative(
		lab.NativeConfig{Provider: provider, Host: l.Host, Namespace: l.Namespace, Repository: l.Repository, User: l.User, Token: nativeToken, CAFile: l.CAFile},
	)
	if err != nil {
		return err
	}
	labels := []string{"ghenv:-", "glenv:-"}
	if comments == "enabled" {
		labels = append(labels, "feature:add_plan_pr_comment")
	}
	response, err = s.w.Do(
		ctx,
		"B",
		"h",
		http.MethodPost,
		"/api/v1/stacks",
		map[string]any{
			"input": map[string]any{
				"name":             "Lab",
				"namespace":        l.Namespace,
				"repository":       l.Repository,
				"branch":           "main",
				"provider":         kind,
				"space":            "acme",
				"vcsIntegrationId": s.integration,
				"workerPool":       s.w.IDs["pool"],
				"autodeploy":       true,
				"labels":           labels,
				"vendorConfig":     map[string]any{"terraform": map[string]any{"version": deps.Terraform.Version}},
			},
			"manageState": true,
		},
	)
	if err != nil || response.Status != 201 {
		return fmt.Errorf("%w: create live stack status%d", harness.ErrStep, response.Status)
	}
	var id kernel.ULID
	if err := s.w.Row(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND slug='lab'`, []any{harness.UUIDOf(harness.Home.Account)}, &id); err != nil {
		return err
	}
	s.stack = id.String()
	return deps.Eventually(ctx, "live stack's initial tracked head", func(ctx context.Context) (bool, error) {
		head, err := s.trackedHead(ctx)
		if err != nil {
			return false, err
		}
		s.tracked = head
		return len(head) == 40, nil
	})
}

func (s *liveLabSteps) trackedHead(ctx context.Context) (string, error) {
	r, err := s.w.Do(ctx, "A", "h", http.MethodGet, "/api/v1/stacks/lab", nil)
	if err != nil || r.Status != 200 {
		return "", fmt.Errorf("%w: read live stack status%d", harness.ErrStep, r.Status)
	}
	var v struct {
		Commit struct {
			Hash string `json:"hash"`
		} `json:"trackedCommit"`
	}
	if json.Unmarshal(r.Body, &v) != nil {
		return "", harness.ErrStep
	}
	return v.Commit.Hash, nil
}

func (s *liveLabSteps) push(ctx context.Context, kind string) error {
	if s.native == nil {
		return harness.ErrStep
	}
	branch := "main"
	if kind == "PROPOSED" {
		if s.branch == "" {
			s.branch = "lab-" + strings.ToLower(kernel.NewULID().String())
		}
		branch = s.branch
	}
	s.w.Relay.Hold()
	head, err := s.native.Push(
		ctx,
		s.w.Dir,
		branch,
		kernel.NewULID().String(),
		lab.GitCredentials{User: os.Getenv("SEED_USER"), Token: os.Getenv("SEED_TOKEN"), CAFile: os.Getenv(sourcelab.EnvCAFile)},
	)
	if err != nil {
		return err
	}
	s.w.IDs["liveHead"] = head
	return nil
}

func (s *liveLabSteps) run(ctx context.Context, kind, name string) error {
	head := s.w.IDs["liveHead"]
	err := deps.Eventually(ctx, "one run from the native signed push", func(ctx context.Context) (bool, error) {
		var count int
		var id string
		err := s.w.Row(
			ctx,
			`SELECT count(*),coalesce(min(id::text),'') FROM runs WHERE account_id=$1 AND stack_id=$2 AND type=$3 AND commit->>'hash'=$4`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.stack), kind, head},
			&count,
			&id,
		)
		if err != nil {
			return false, err
		}
		if count > 1 {
			return false, fmt.Errorf("%w: native push created duplicate runs", deps.ErrPermanent)
		}
		if count == 1 {
			var parsed kernel.ULID
			if err := parsed.Scan(id); err != nil {
				return false, err
			}
			s.w.IDs[name] = parsed.String()
		}
		return count == 1, nil
	})
	if err != nil {
		return err
	}
	if kind == "TRACKED" {
		s.tracked = head
	}
	return deps.Eventually(
		ctx,
		"tracked commit remains correct for native push type",
		func(ctx context.Context) (bool, error) { got, err := s.trackedHead(ctx); return got == s.tracked, err },
	)
}

func (s *liveLabSteps) checkName(ctx context.Context, run string) (string, error) {
	var kind string
	if err := s.w.Row(ctx, `SELECT type FROM runs WHERE account_id=$1 AND id=$2`, []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.w.IDs[run])}, &kind); err != nil {
		return "", err
	}
	name := "infraweave/lab"
	if kind == "TRACKED" {
		name += "/tracked"
	}
	return name, nil
}

func (s *liveLabSteps) pendingCheck(ctx context.Context, run string) error {
	name, err := s.checkName(ctx, run)
	if err != nil {
		return err
	}
	err = deps.Eventually(ctx, "single native check before releasing genuine worker report", func(ctx context.Context) (bool, error) {
		items, err := s.native.Checks(ctx, s.w.IDs["liveHead"], name)
		if err != nil {
			return false, err
		}
		current, found, err := lab.CurrentCheck(s.provider, items, s.public+"/api/v1/runs/"+s.w.IDs[run], s.stack)
		if err != nil {
			return false, fmt.Errorf("%w: current native check: %w", deps.ErrPermanent, err)
		}
		if !found {
			return false, nil
		}
		if slices.Contains([]string{"queued", "in_progress", "pending", "running", "INPROGRESS"}, current.State) {
			s.pending[run] = current.ID
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	s.w.Relay.Release()
	return nil
}

func (s *liveLabSteps) successCheck(ctx context.Context, run string) error {
	if err := s.settledFeedback(ctx, run); err != nil {
		return err
	}
	name, err := s.checkName(ctx, run)
	if err != nil {
		return err
	}
	return deps.Eventually(ctx, "single successful current native check resource", func(ctx context.Context) (bool, error) {
		items, err := s.native.Checks(ctx, s.w.IDs["liveHead"], name)
		if err != nil {
			return false, err
		}
		current, found, err := lab.CurrentCheck(s.provider, items, s.public+"/api/v1/runs/"+s.w.IDs[run], s.stack)
		if err != nil {
			return false, fmt.Errorf("%w: current native check: %w", deps.ErrPermanent, err)
		}
		if !found || !slices.Contains([]string{"success", "SUCCESSFUL", "succeeded"}, current.State) {
			return false, nil
		}
		if s.pending[run] == "" {
			return false, fmt.Errorf("%w: no observed initial native resource", deps.ErrPermanent)
		}
		if (s.provider == "github" || strings.HasPrefix(s.provider, "bitbucket-")) && current.ID != s.pending[run] {
			return false, fmt.Errorf("%w: native check resource was replaced", deps.ErrPermanent)
		}
		return true, nil
	})
}

func (s *liveLabSteps) settledFeedback(ctx context.Context, run string) error {
	return deps.Eventually(ctx, "live run event fanout and native feedback complete", func(ctx context.Context) (bool, error) {
		var events, pending int
		var consumed bool
		err := s.w.Row(
			ctx,
			`SELECT count(*),coalesce(bool_and(e.fanned_out_at IS NOT NULL AND EXISTS(SELECT 1 FROM event_consumptions c WHERE c.account_id=e.account_id AND c.event_id=e.id AND c.consumer='events.sinks')),false),(SELECT count(*) FROM jobs j WHERE j.account_id=$1 AND j.state<>'succeeded' AND ((j.kind='events.consume' AND j.payload->>'consumer'='events.sinks') OR j.kind LIKE 'vcs.report_%')) FROM events e WHERE e.account_id=$1 AND e.kind='run_updated' AND e.subject_kind='run' AND e.subject_id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs[run]},
			&events,
			&consumed,
			&pending,
		)
		return events > 0 && consumed && pending == 0, err
	})
}

func (s *liveLabSteps) runCount(ctx context.Context) (int, error) {
	var count int
	err := s.w.Row(ctx, `SELECT count(*) FROM runs WHERE account_id=$1 AND stack_id=$2`, []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.stack)}, &count)
	return count, err
}

func (s *liveLabSteps) open(ctx context.Context) error {
	before, err := s.runCount(ctx)
	if err != nil {
		return err
	}
	s.request, err = s.native.OpenPullRequest(ctx, s.branch)
	if err != nil {
		return err
	}
	if err := s.requestHead(ctx); err != nil {
		return err
	}
	if err := s.settledDeliveries(ctx); err != nil {
		return err
	}
	after, err := s.runCount(ctx)
	if err != nil || before != after {
		return fmt.Errorf("%w: opening the native request created another run", harness.ErrStep)
	}
	return nil
}

func (s *liveLabSteps) requestHead(ctx context.Context) error {
	return deps.Eventually(ctx, "native request head retained before planning", func(ctx context.Context) (bool, error) {
		var found bool
		err := s.w.Row(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM pull_requests WHERE account_id=$1 AND integration_id=$2 AND provider_id=$3 AND state='open' AND head_sha=$4)`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.integration), strconv.Itoa(s.request), s.w.IDs["liveHead"]},
			&found,
		)
		return found, err
	})
}

func (s *liveLabSteps) settledDeliveries(ctx context.Context) error {
	return deps.Eventually(ctx, "native delivery parent and child jobs completed", func(ctx context.Context) (bool, error) {
		var pending int
		err := s.w.Row(
			ctx,
			`SELECT count(*) FROM jobs WHERE account_id=$1 AND kind IN ('vcs.process_delivery','vcs.evaluate_event') AND state<>'succeeded'`,
			[]any{harness.UUIDOf(harness.Home.Account)},
			&pending,
		)
		return pending == 0, err
	})
}

func (s *liveLabSteps) planComment(ctx context.Context, run string) error {
	return deps.Eventually(ctx, "one persisted native plan comment per run", func(ctx context.Context) (bool, error) {
		items, err := s.native.Comments(ctx, s.request)
		if err != nil {
			return false, err
		}
		count := 0
		for _, v := range items {
			if strings.Contains(v.Body, "<!-- infraweave:plan:"+s.w.IDs[run]+" -->") {
				count++
				if !strings.Contains(v.Body, "Plan:") && !strings.Contains(v.Body, "No changes") {
					return false, fmt.Errorf("%w: native comment omits genuine planning log", deps.ErrPermanent)
				}
			}
		}
		if count > 1 {
			return false, fmt.Errorf("%w: duplicate native plan comments", deps.ErrPermanent)
		}
		return count == 1, nil
	})
}

func (s *liveLabSteps) previousComment(ctx context.Context, first, second string) error {
	if err := s.planComment(ctx, first); err != nil {
		return err
	}
	if err := s.planComment(ctx, second); err != nil {
		return err
	}
	if s.provider != "github" {
		return nil
	}
	return deps.Eventually(ctx, "earlier native GitHub plan comment minimized", func(ctx context.Context) (bool, error) {
		items, err := s.native.Comments(ctx, s.request)
		if err != nil {
			return false, err
		}
		for _, v := range items {
			if strings.Contains(v.Body, "<!-- infraweave:plan:"+s.w.IDs[first]+" -->") {
				return s.native.Minimized(ctx, v.NodeID)
			}
		}
		return false, nil
	})
}

func (s *liveLabSteps) ordinaryComment(ctx context.Context) error {
	before, err := s.runCount(ctx)
	if err != nil {
		return err
	}
	marker := "ordinary lab delivery proof " + kernel.NewULID().String()
	if err := s.native.Comment(ctx, s.request, marker); err != nil {
		return err
	}
	err = deps.Eventually(ctx, "verified native PR comment stored and dispatched", func(ctx context.Context) (bool, error) {
		var body, signature []byte
		err := s.w.Row(
			ctx,
			`SELECT body,signature FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND state='dispatched' AND position(convert_to($3,'UTF8') IN body)>0 ORDER BY received_at DESC LIMIT 1`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.integration), marker},
			&body,
			&signature,
		)
		if err != nil {
			return false, err
		}
		h := hmac.New(sha256.New, s.secret.Reveal())
		_, _ = h.Write(body)
		wanted := hex.EncodeToString(h.Sum(nil))
		if s.provider == "github" || strings.HasPrefix(s.provider, "bitbucket-") {
			wanted = "sha256=" + wanted
		}
		return hmac.Equal(signature, []byte(wanted)), nil
	})
	if err != nil {
		return err
	}
	if err := s.settledDeliveries(ctx); err != nil {
		return err
	}
	after, err := s.runCount(ctx)
	if err != nil || after != before {
		return fmt.Errorf("%w: ordinary native comment started a run", harness.ErrStep)
	}
	return nil
}

func (s *liveLabSteps) after(ctx context.Context, _ *godog.Scenario, failed error) (context.Context, error) {
	if s.w.Relay != nil {
		s.w.Relay.Release()
	}
	var cleanup error
	if s.lease != nil {
		cleanup = errors.Join(cleanup, s.lease.Restore(ctx))
	}
	if s.hooks != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		cleanup = errors.Join(cleanup, s.hooks.Set(cleanupCtx, "", ""))
		cancel()
	}
	if s.ingress != nil {
		cleanup = errors.Join(cleanup, s.ingress.Close(ctx))
	}
	return ctx, errors.Join(failed, cleanup)
}
