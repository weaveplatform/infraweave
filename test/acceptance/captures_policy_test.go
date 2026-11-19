package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitPolicyCaptureEvidence(t *testing.T) {
	if _, err := harness.LoadCaptures(capturesRoot, captureEntries(t)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"C49", "C54", "C57", "C192"} {
		t.Run(id, func(t *testing.T) { checkPolicyCapture(t, id) })
	}
}

func captureJSON(t *testing.T, id, name string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(capturesRoot, id, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func checkPolicyCapture(t *testing.T, id string) {
	t.Helper()
	switch id {
	case "C49":
		data, err := os.ReadFile(filepath.Join(capturesRoot, id, harness.RequestFile))
		if err != nil {
			t.Fatal(err)
		}
		var variables struct {
			Input map[string]any `json:"input"`
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if value, ok := strings.CutPrefix(line, "# variables: "); ok {
				if err := json.Unmarshal([]byte(value), &variables); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if variables.Input == nil || variables.Input["engineType"] != nil {
			t.Fatal("the create must omit engineType")
		}
		// The loader also checks the V0 result, successful deletion and empty
		// final policy list against expect.yaml.
	case "C54":
		checkPolicyCatalogue(t)
	case "C57":
		var response struct {
			Data struct {
				Runtime struct {
					Version string `json:"openPolicyAgentVersion"`
				} `json:"policyRuntime"`
			} `json:"data"`
		}
		captureJSON(t, id, harness.ResponseFile, &response)
		if response.Data.Runtime.Version != "v1.17.1" {
			t.Fatalf("recorded runtime = %q", response.Data.Runtime.Version)
		}
	case "C192":
		var response struct {
			Data struct {
				Simulation string `json:"policySimulatev2"`
			} `json:"data"`
		}
		captureJSON(t, id, harness.ResponseFile, &response)
		var result struct {
			Info []string `json:"info"`
		}
		if err := json.Unmarshal([]byte(response.Data.Simulation), &result); err != nil {
			t.Fatal(err)
		}
		want := make([]string, 0, 2)
		for _, value := range []string{"hello", "infraweave-capture-test"} {
			digest := sha256.Sum256([]byte(value))
			want = append(want, hex.EncodeToString(digest[len(digest)-4:]))
		}
		slices.Sort(want)
		slices.Sort(result.Info)
		if !reflect.DeepEqual(result.Info, want) {
			t.Fatalf("sanitized results = %v, want final four SHA-256 bytes %v", result.Info, want)
		}
	default:
		t.Fatalf("no recorded policy check for %s", id)
	}
}

func checkPolicyCatalogue(t *testing.T) {
	t.Helper()
	var manifest struct {
		Revision  string `json:"revision"`
		Templates []struct {
			Path   string `json:"path"`
			Type   string `json:"type"`
			SHA256 string `json:"sha256"`
		} `json:"templates"`
		Tests []string `json:"tests"`
	}
	captureJSON(t, "C54", "library.json", &manifest)
	if manifest.Revision != "8e8894fbd4cefffa3c261746bcdd70ef9fbb53a8" || len(manifest.Templates) != 68 || len(manifest.Tests) != 62 {
		t.Fatalf("library inventory: %s, %d templates, %d test files", manifest.Revision, len(manifest.Templates), len(manifest.Tests))
	}
	want := map[string]string{}
	login := 0
	for _, template := range manifest.Templates {
		if template.Type == "login" {
			login++
			continue
		}
		if _, duplicate := want[template.SHA256]; duplicate {
			t.Fatalf("duplicate library body: %s", template.Path)
		}
		want[template.SHA256] = template.Type
	}
	var response struct {
		Pages []struct {
			Status   int `json:"status"`
			Response struct {
				Data struct {
					Templates struct {
						Edges []struct {
							Node struct {
								ID   string `json:"id"`
								Type string `json:"type"`
								Body string `json:"body"`
							} `json:"node"`
						} `json:"edges"`
						PageInfo struct {
							HasNext bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"searchPolicyTemplates"`
				} `json:"data"`
			} `json:"response"`
		} `json:"pages"`
	}
	captureJSON(t, "C54", harness.ResponseFile, &response)
	seen := map[string]bool{}
	counts := map[string]int{}
	for i, page := range response.Pages {
		if page.Status != 200 || page.Response.Data.Templates.PageInfo.HasNext != (i < len(response.Pages)-1) {
			t.Fatalf("page %d: unsuccessful or incomplete pagination", i)
		}
		for _, edge := range page.Response.Data.Templates.Edges {
			node := edge.Node
			digest := sha256.Sum256([]byte(node.Body))
			key := hex.EncodeToString(digest[:])
			kind := strings.ToLower(node.Type)
			if kind == "git_push" {
				kind = "push"
			}
			if seen[node.ID] || want[key] != kind || kind == "login" {
				t.Fatalf("template %s: duplicate or no matching pinned %s body", node.ID, kind)
			}
			seen[node.ID] = true
			counts[kind]++
			delete(want, key)
		}
	}
	if login != 6 || len(seen) != 62 || len(want) != 0 || !reflect.DeepEqual(counts, map[string]int{"approval": 8, "notification": 7, "plan": 28, "push": 14, "trigger": 5}) {
		t.Fatalf("catalogue: %d login exclusions, %d live, %d missing, types %v", login, len(seen), len(want), counts)
	}
}
