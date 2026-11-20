package github_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
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
