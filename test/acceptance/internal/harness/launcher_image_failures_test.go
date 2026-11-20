package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

// These fixtures inject native HTTP failures, rather than simulate ownership.
// The independent-owner lifecycle is separately checked against real Docker.
func rootImageTestClient(t *testing.T, handler http.Handler) *testcontainers.DockerClient {
	t.Helper()
	router := chi.NewRouter()
	router.Handle("/*", handler)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	cli, err := client.New(client.WithHost(srv.URL), client.WithAPIVersion("1.55"), client.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return &testcontainers.DockerClient{Client: cli}
}

func TestUnitRootImageImportFailureStillOwnsCleanup(t *testing.T) {
	for _, mode := range []string{"HTTP failure", "stream failure", "truncated stream", "inspection failure"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var imported, removed string
			var inspected int
			h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_, _ = io.Copy(io.Discard, req.Body)
				w.Header().Set("Content-Type", "application/json")
				mu.Lock()
				defer mu.Unlock()
				switch req.Method {
				case http.MethodPost:
					imported = req.URL.Query().Get("repo")
					switch mode {
					case "HTTP failure":
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"message":"fixture import failure"}`)
					case "stream failure":
						_, _ = io.WriteString(w, `{"errorDetail":{"message":"fixture stream failure"},"error":"fixture stream failure"}`)
					case "truncated stream":
						_, _ = io.WriteString(w, `{"status":`)
					default:
						_, _ = io.WriteString(w, `{"status":"sha256:fixture"}`)
					}
				case http.MethodGet:
					inspected++
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"message":"fixture inspect failure"}`)
				case http.MethodDelete:
					removed = strings.TrimPrefix(req.URL.Path, "/v1.55/images/")
					if req.URL.Query().Get("force") == "true" || req.URL.Query().Get("force") == "1" {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"message":"cleanup forced image deletion"}`)
						return
					}
					_, _ = io.WriteString(w, `[]`)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})
			owner := &Launchers{cli: rootImageTestClient(t, h)}
			if err := owner.importRunnerImage(t.Context()); err == nil {
				t.Fatal("partial or refused import became ready", mode)
			}
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal("failed import lost its cleanup", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !strings.HasPrefix(owner.Image, "infraweave-runner:acceptance-") || imported != owner.Image || removed != owner.Image ||
				(mode == "inspection failure") != (inspected == 1) {
				t.Fatal("failure crossed the owned image reference or hid its stage", owner.Image, imported, removed, inspected)
			}
		})
	}
}

func TestUnitRootImageCancellationAndRepeatedClose(t *testing.T) {
	var mu sync.Mutex
	var removed []string
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if req.Method != http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"canceled import reached the daemon"}`)
			return
		}
		removed = append(removed, strings.TrimPrefix(req.URL.Path, "/v1.55/images/"))
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"image already absent"}`)
	})
	owner := &Launchers{cli: rootImageTestClient(t, h)}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal("cleanup before import should only close its client", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := owner.importRunnerImage(ctx); !errors.Is(err, context.Canceled) || owner.Image == "" {
		t.Fatal("canceled import lost its cause or allocated cleanup reference", err, owner.Image)
	}
	if err := owner.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cleanup concealed a real failure", err)
	}
	for range 2 {
		if err := owner.Close(t.Context()); err != nil {
			t.Fatal("fresh-context cleanup could not retry or accept absence", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(removed) != 2 || removed[0] != owner.Image || removed[1] != owner.Image {
		t.Fatal("cleanup retried another reference", removed, owner.Image)
	}
	if err := (&Launchers{}).Close(t.Context()); err != nil {
		t.Fatal("cleanup before startup failed", err)
	}
}

func TestUnitRootImageUsedByForwarderAndFallbackOnly(t *testing.T) {
	var mu sync.Mutex
	var created []container.Config
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(req.URL.Path, "/containers/create") {
			var config container.Config
			if err := json.NewDecoder(req.Body).Decode(&config); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			created = append(created, config)
			mu.Unlock()
			_, _ = fmt.Fprint(w, `{"Id":"fixture-container"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	owner := &Launchers{cli: rootImageTestClient(t, h), Image: "infraweave-runner:acceptance-owned", Forward: "/fixture/forward"}
	network, err := owner.Network(t.Context(), "owned-scenario", "1234")
	if err != nil || runtime.GOOS == "linux" && network != "host" || runtime.GOOS != "linux" && network != "container:fixture-container" {
		t.Fatal("platform network changed", network, err)
	}
	for _, image := range []string{"", "registry.example/custom:fixed"} {
		if _, err := owner.RunTool(t.Context(), ToolRun{Scenario: "owned-scenario", Bin: "/fixture/tool", Dir: "/fixture/workspace", Image: image}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	tools := created
	if runtime.GOOS != "linux" {
		if len(created) != 3 || created[0].Image != owner.Image || created[0].Labels[LabelScenario] != "owned-scenario" {
			t.Fatal("forwarder used a shared image or changed its labels", created)
		}
		tools = created[1:]
	}
	if len(tools) != 2 || tools[0].Image != owner.Image || tools[1].Image != "registry.example/custom:fixed" {
		t.Fatal("fallback crossed owners or custom image was overwritten", tools)
	}
}
