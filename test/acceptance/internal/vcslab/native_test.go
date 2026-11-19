package vcslab

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestUnitNativeLabPullRequestAndCommentWireContracts(t *testing.T) {
	for _, provider := range []string{"github", "gitlab", "bitbucket-cloud", "bitbucket-dc", "azure-devops"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			r := chi.NewRouter()
			r.Post("/*", func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body map[string]any
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Error("native body is not JSON")
					w.WriteHeader(400)
					return
				}
				if calls == 1 {
					if body["title"] != "VCS lab verification" {
						t.Error("native request title missing")
					}
					switch provider {
					case "github":
						if body["head"] != "lab-proof" || body["base"] != "main" {
							t.Error("GitHub request ref mismatch")
						}
					case "gitlab":
						if body["source_branch"] != "lab-proof" || body["target_branch"] != "main" {
							t.Error("GitLab request ref mismatch")
						}
					case "azure-devops":
						if body["sourceRefName"] != "refs/heads/lab-proof" || body["targetRefName"] != "refs/heads/main" {
							t.Error("Azure request ref mismatch")
						}
					default:
						if body["source"] == nil && body["fromRef"] == nil {
							t.Error("native source ref missing")
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]int{"id": 7, "number": 7, "iid": 7, "pullRequestId": 7})
					return
				}
				encoded, _ := json.Marshal(body)
				if !strings.Contains(string(encoded), "ordinary lab delivery proof") || !strings.Contains(req.URL.Path, "/7/") {
					t.Error("ordinary native comment is not scoped to the created request")
				}
				w.WriteHeader(201)
			})
			srv := httptest.NewServer(r)
			defer srv.Close()
			n := &Native{Provider: provider, Host: srv.URL, Namespace: "group", Repository: "repository", User: "42", token: "native-private-test-token", client: srv.Client()}
			id, err := n.OpenPullRequest(t.Context(), "lab-proof")
			if err != nil || id != 7 {
				t.Fatal("native PR creation failed")
			}
			if err := n.Comment(t.Context(), id, "ordinary lab delivery proof"); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal("native verification made unexpected writes")
			}
		})
	}
}

func TestUnitNativeLabConfiguredFailureAndRedirectRemainFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			entered := false
			r := chi.NewRouter()
			r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
				entered = true
				if req.Header.Get("PRIVATE-TOKEN") != "long-native-private-test-token" {
					t.Error("configured lab credential was not used")
				}
				w.Header().Set("Location", "https://must-not-be-contacted.example.com/")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-provider-body-must-not-escape"))
			})
			srv := httptest.NewTLSServer(r)
			defer srv.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			n, err := NewNative(NativeConfig{Provider: "gitlab", Host: srv.URL, Namespace: "group", Repository: "repository", Token: "long-native-private-test-token", CAFile: ca})
			if err != nil {
				t.Fatal(err)
			}
			err = n.send(t.Context(), http.MethodGet, n.repoPath(), nil, nil)
			if !entered || !errors.Is(err, ErrNative) || strings.Contains(err.Error(), "private-provider-body") || strings.Contains(err.Error(), n.token) {
				t.Fatal("configured failure was skipped, followed or leaked response material")
			}
			for _, v := range []string{fmt.Sprint(n), fmt.Sprintf("%#v", n)} {
				if strings.Contains(v, n.token) {
					t.Fatal("formatted native client reveals credential")
				}
			}
		})
	}
}

func TestUnitNativeGitHubCheckReadbackFiltersAppAndPages(t *testing.T) {
	var pages int
	r := chi.NewRouter()
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		pages++
		if req.URL.Query().Get("app_id") != "42" {
			t.Error("native check lookup did not scope App identity")
		}
		var checks []map[string]any
		if pages == 1 {
			for i := 0; i < 100; i++ {
				checks = append(checks, map[string]any{"id": i + 1, "name": "infraweave/lab", "head_sha": "head", "status": "queued", "app": map[string]int{"id": 99}})
			}
		} else {
			checks = []map[string]any{
				{
					"id":          101,
					"name":        "infraweave/lab",
					"head_sha":    "head",
					"status":      "completed",
					"conclusion":  "success",
					"details_url": "https://lab.example.com/api/v1/runs/run",
					"external_id": "stack",
					"app":         map[string]int{"id": 42},
				},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": checks})
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	n := &Native{Provider: "github", Host: srv.URL, Namespace: "group", Repository: "repository", User: "42", token: "private-test-token", client: srv.Client()}
	checks, err := n.Checks(t.Context(), "head", "infraweave/lab")
	if err != nil || pages != 2 || len(checks) != 1 || checks[0].ID != "101" || checks[0].State != "success" || checks[0].ExternalID != "stack" {
		t.Fatal("native check readback lost completeness, identity or terminal facts")
	}
}

func TestUnitNativeCurrentChecksRefuseDuplicateOrHistoricalSuccess(t *testing.T) {
	const runURL = "https://lab.example.com/api/v1/runs/run"
	first := NativeCheck{ID: "1", Name: "infraweave/lab", State: "success", URL: runURL, ExternalID: "stack"}
	second := first
	second.ID = "2"
	if _, _, err := CurrentCheck("github", []NativeCheck{first, second}, runURL, "stack"); !errors.Is(err, ErrNative) {
		t.Fatal("duplicate GitHub run resources were accepted")
	}
	for _, provider := range []string{"gitlab", "azure-devops"} {
		second.URL = runURL
		second.State = "failed"
		got, found, err := CurrentCheck(provider, []NativeCheck{first, second}, runURL, "stack")
		if err != nil || !found || got.State != "failed" {
			t.Fatal("historical success hid a current failed context")
		}
		second.URL = "https://lab.example.com/api/v1/runs/newer-owner"
		if _, found, err := CurrentCheck(provider, []NativeCheck{first, second}, runURL, "stack"); err != nil || found {
			t.Fatal("historical run ownership hid the newer native context owner")
		}
	}
}
