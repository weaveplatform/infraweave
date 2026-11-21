package github_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitAcknowledgedCheckSurvivesStaleListing(t *testing.T) {
	l := newLab(t)
	lists := 0
	router := chi.NewRouter()
	router.Get("/api/v3/repos/acme/infra/commits/{sha}/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		lists++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "check_runs": []any{}})
	})
	router.Handle("/*", l.fake.Handler())
	p := github.NewOver(memory{router})
	report := port.CheckReport{
		Name: "infraweave/infra", State: port.CheckQueued, Title: "Queued", Summary: "Waiting for a worker.",
		URL: "https://infra.example/runs/acknowledged", ExternalID: kernel.NewULID().String(), RecoveryID: "infraweave/run:acknowledged",
	}
	id, err := p.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || id == "" {
		t.Fatal("initial check was not acknowledged", err)
	}
	report.NativeID = id
	report.State, report.Title = port.CheckInProgress, "Planning"
	updated, err := p.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
	if err != nil || updated != id || len(l.fake.Checks("acme", "infra")) != 1 || lists != 1 {
		t.Fatalf("acknowledged ID was replaced after a stale list: sameID=%t resources=%d listRequests=%d error=%v", updated == id, len(l.fake.Checks("acme", "infra")), lists, err)
	}
}

func TestUnitKnownCheckRefusesForeignOrMissingIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*githubfake.CheckRecord)
	}{
		{"id", func(r *githubfake.CheckRecord) { r.ID++ }},
		{"app", func(r *githubfake.CheckRecord) { r.App.ID++ }},
		{"commit", func(r *githubfake.CheckRecord) { r.SHA = strings.Repeat("e", 40) }},
		{"name", func(r *githubfake.CheckRecord) { r.Name = "another check" }},
		{"stack", func(r *githubfake.CheckRecord) { r.ExternalID = kernel.NewULID().String() }},
		{"marker", func(r *githubfake.CheckRecord) { r.Output.Text = "another run" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			router := chi.NewRouter()
			router.Get("/api/v3/repos/acme/infra/check-runs/{check}", func(w http.ResponseWriter, _ *http.Request) {
				record := l.fake.Checks("acme", "infra")[0]
				tc.mutate(&record)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(record)
			})
			router.Handle("/*", l.fake.Handler())
			p := github.NewOver(memory{router})
			report := acknowledgedReport()
			id, err := p.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
			if err != nil {
				t.Fatal(err)
			}
			report.NativeID, report.State = id, port.CheckInProgress
			if _, err := p.Set(t.Context(), l.repo.Repo, l.repo.Head, report); !errors.Is(err, port.ErrCheckInput) {
				t.Fatal("known check did not refuse mismatched identity", err)
			}
			if len(l.fake.CheckWrites("acme", "infra")) != 1 {
				t.Fatal("foreign check was mutated or replaced")
			}
		})
	}
}

func TestUnitKnownCheckLookupFailureNeverCreatesReplacement(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			l := newLab(t)
			router := chi.NewRouter()
			router.Get("/api/v3/repos/acme/infra/check-runs/{check}", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"lookup refused"}`))
			})
			router.Handle("/*", l.fake.Handler())
			p := github.NewOver(memory{router})
			report := acknowledgedReport()
			id, err := p.Set(t.Context(), l.repo.Repo, l.repo.Head, report)
			if err != nil {
				t.Fatal(err)
			}
			report.NativeID, report.State = id, port.CheckInProgress
			if _, err := p.Set(t.Context(), l.repo.Repo, l.repo.Head, report); err == nil {
				t.Fatal("known check lookup failure was hidden")
			} else if status == http.StatusTooManyRequests {
				if _, ok := errors.AsType[*port.RateLimited](err); !ok {
					t.Fatal("known check rate limit did not reach the queue boundary", err)
				}
			}
			if len(l.fake.CheckWrites("acme", "infra")) != 1 {
				t.Fatal("failed known lookup mutated or replaced the acknowledged resource")
			}
		})
	}
	l := newLab(t)
	for _, id := range []string{"0", "-1", "bad", "9223372036854775808"} {
		report := acknowledgedReport()
		report.NativeID = id
		if _, err := l.gh.Set(t.Context(), l.repo.Repo, l.repo.Head, report); !errors.Is(err, port.ErrCheckInput) {
			t.Fatal("malformed native identity was accepted", err)
		}
	}
	if len(l.fake.CheckWrites("acme", "infra")) != 0 {
		t.Fatal("malformed identity reached a native write")
	}
}

func acknowledgedReport() port.CheckReport {
	return port.CheckReport{
		Name: "infraweave/infra", State: port.CheckQueued, Title: "Queued", Summary: "Waiting for a worker.",
		URL: "https://infra.example/runs/acknowledged", ExternalID: kernel.NewULID().String(), RecoveryID: "infraweave/run:acknowledged",
	}
}
