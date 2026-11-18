package harness

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
)

type cleanupHTTP struct {
	mu             sync.Mutex
	goneAtCreate   bool
	createStatus   int
	retryStatus    int
	missingBinds   bool
	helperExit     int64
	parentGone     bool
	creates        []container.CreateRequest
	started        int
	waited         int
	removed        []string
	protocolFailed bool
}

var cleanupBinds = []string{
	"/owned/launcher/binary:" + docker.BinaryPath + ":ro",
	"/owned/scenario/workspace:" + docker.Workspace,
	"/owned/tool/terraform:/bin/terraform:ro",
}

func (d *cleanupHTTP) handler() http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d.mu.Lock()
			defer d.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			next.ServeHTTP(w, r)
		})
	})
	r.Route("/v1.55", func(r chi.Router) {
		r.Post("/containers/run/kill", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		r.Post("/containers/run/wait", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(container.WaitResponse{StatusCode: 137})
		})
		r.Get("/containers/run/json", func(w http.ResponseWriter, _ *http.Request) {
			if d.parentGone {
				cleanupHTTPError(w, http.StatusNotFound, "No such container: run")
				return
			}
			binds := slices.Clone(cleanupBinds)
			if d.missingBinds {
				binds = nil
			}
			_ = json.NewEncoder(w).Encode(container.InspectResponse{
				ID: "run", Image: "sha256:owned-runner",
				HostConfig: &container.HostConfig{Binds: binds, Runtime: "owned-runtime"},
			})
		})
		r.Post("/containers/create", func(w http.ResponseWriter, r *http.Request) {
			var request container.CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Config == nil || request.HostConfig == nil {
				d.protocolFailed = true
				cleanupHTTPError(w, http.StatusBadRequest, "malformed companion create")
				return
			}
			d.creates = append(d.creates, request)
			if len(d.creates) == 1 {
				if d.goneAtCreate {
					d.parentGone = true
					// Moby wraps missing VolumesFrom containers as an invalid
					// parameter, so the SDK classifies this HTTP400 differently
					// from an inspect's ordinary HTTP404.
					cleanupHTTPError(w, http.StatusBadRequest, "No such container: run")
					return
				}
				if d.createStatus != 0 {
					cleanupHTTPError(w, d.createStatus, "companion creation refused")
					return
				}
			} else if d.retryStatus != 0 {
				cleanupHTTPError(w, d.retryStatus, "retained bind creation refused")
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "hand-back"})
		})
		r.Post("/containers/hand-back/start", func(w http.ResponseWriter, _ *http.Request) {
			d.started++
			w.WriteHeader(http.StatusNoContent)
		})
		r.Post("/containers/hand-back/wait", func(w http.ResponseWriter, _ *http.Request) {
			d.waited++
			_ = json.NewEncoder(w).Encode(container.WaitResponse{StatusCode: d.helperExit})
		})
		r.Delete("/containers/hand-back", func(w http.ResponseWriter, _ *http.Request) {
			d.removed = append(d.removed, "hand-back")
			w.WriteHeader(http.StatusNoContent)
		})
	})
	return r
}

func cleanupHTTPError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func TestUnitScenarioHandBackUsesNativeDockerCreateContract(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		daemon *cleanupHTTP
		want   error
		retry  bool
	}{
		{name: "normal parent", daemon: &cleanupHTTP{}},
		{name: "source disappears during create", daemon: &cleanupHTTP{goneAtCreate: true}, retry: true},
		{name: "genuine invalid create", daemon: &cleanupHTTP{createStatus: http.StatusBadRequest}, want: cerrdefs.ErrInvalidArgument},
		{name: "genuine missing image", daemon: &cleanupHTTP{createStatus: http.StatusNotFound}, want: cerrdefs.ErrNotFound},
		{name: "genuine daemon failure", daemon: &cleanupHTTP{createStatus: http.StatusInternalServerError}, want: cerrdefs.ErrInternal},
		{name: "lost source has no retained binds", daemon: &cleanupHTTP{goneAtCreate: true, missingBinds: true}, want: cerrdefs.ErrInvalidArgument},
		{name: "retained binds cannot create", daemon: &cleanupHTTP{goneAtCreate: true, retryStatus: http.StatusForbidden}, want: cerrdefs.ErrPermissionDenied, retry: true},
		{name: "recovered hand-back fails", daemon: &cleanupHTTP{goneAtCreate: true, helperExit: 9}, retry: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := scenario.daemon
			srv := httptest.NewServer(d.handler())
			defer srv.Close()
			cli, err := client.New(client.WithHost(srv.URL), client.WithHTTPClient(srv.Client()), client.WithAPIVersion("1.55"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			err = handBackContainer(t.Context(), cli, "run")
			d.mu.Lock()
			defer d.mu.Unlock()
			switch {
			case scenario.want != nil:
				if !errors.Is(err, scenario.want) {
					t.Fatal("genuine native failure was hidden", err)
				}
			case d.helperExit != 0:
				if err == nil {
					t.Fatal("unsuccessful recovered hand-back was accepted")
				}
			case err != nil:
				t.Fatal("owned hand-back could not complete", err)
			}
			if d.protocolFailed {
				t.Fatal("malformed Docker create request")
			}
			wantCreates := 1
			if scenario.retry {
				wantCreates++
			}
			if len(d.creates) != wantCreates {
				t.Fatal("lost-source recovery was skipped or an unrelated failure retried", len(d.creates), wantCreates)
			}
			for i, request := range d.creates {
				if request.Image != "sha256:owned-runner" || request.User != docker.User || request.WorkingDir != docker.Workspace ||
					!slices.Equal(request.Entrypoint, []string{docker.BinaryPath}) || !slices.Equal(request.Cmd, []string{"runner", "hand-back"}) ||
					request.HostConfig.NetworkMode != "none" || request.HostConfig.Runtime != "owned-runtime" {
					t.Fatal("recovery changed the owned image, user, runtime or network", request)
				}
				if i == 0 && (!slices.Equal(request.HostConfig.VolumesFrom, []string{"run"}) || len(request.HostConfig.Binds) != 0) {
					t.Fatal("normal hand-back stopped using its owned source", request.HostConfig)
				}
				if i > 0 && (len(request.HostConfig.VolumesFrom) != 0 || !reflect.DeepEqual(request.HostConfig.Binds, cleanupBinds)) {
					t.Fatal("recovery did not retain only the inspected owned binds", request.HostConfig)
				}
			}
			if scenario.want == nil && (d.started != 1 || d.waited != 1 || !slices.Equal(d.removed, []string{"hand-back"})) {
				t.Fatal("success skipped actual hand-back or companion cleanup", d.started, d.waited, d.removed)
			}
			if d.retryStatus != 0 && !errors.Is(err, cerrdefs.ErrInvalidArgument) {
				t.Fatal("failed recovery discarded the original source error", err)
			}
		})
	}
}
