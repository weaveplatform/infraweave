package deps

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func workersFixture(owner, batch string, providers ...string) map[string]any {
	hosting := make([]any, 0, len(providers))
	for _, provider := range providers {
		hosting = append(hosting, map[string]any{
			"owner": owner, "batch": batch, "cloud": map[string]any{"provider": provider, "identifiers": map[string]any{}},
			"cluster":   map[string]any{"id": provider + "-cluster", "cluster_id": "lab"},
			"namespace": map[string]any{"name": "iw-workers", "uid": "uid-" + provider, "owned": true},
			"pools":     []any{map[string]any{"resource_name": "stable", "capacity": 1}},
		})
	}
	return map[string]any{"schema": 1, "owner": owner, "batch": batch, "hosting": hosting}
}

func TestUnitWorkersManifestBoundary(t *testing.T) {
	encode := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	hosting, err := readWorkers(encode(workersFixture("iw-lab-a", "workers-multicloud", "aws", "azure", "google")), "iw-lab-a", "workers-multicloud")
	if err != nil || len(hosting) != 3 || hosting[2] != (WorkerHosting{Provider: "google", ClusterID: "google-cluster", Namespace: "iw-workers", Pools: 1}) {
		t.Fatalf("readWorkers = %+v, %v", hosting, err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"schema":        func(m map[string]any) { m["schema"] = 2 },
		"owner":         func(m map[string]any) { m["owner"] = "iw-lab-b" },
		"batch":         func(m map[string]any) { m["batch"] = "workers-google" },
		"extra":         func(m map[string]any) { m["kubeconfig"] = "secret" },
		"missing":       func(m map[string]any) { delete(m, "hosting"); m["other"] = 1 },
		"empty":         func(m map[string]any) { m["hosting"] = []any{} },
		"foreign entry": func(m map[string]any) { m["hosting"].([]any)[0].(map[string]any)["owner"] = "iw-lab-b" },
		"unknown cloud": func(m map[string]any) {
			m["hosting"].([]any)[0].(map[string]any)["cloud"] = map[string]any{"provider": "other"}
		},
		"duplicate":  func(m map[string]any) { m["hosting"] = append(m["hosting"].([]any), m["hosting"].([]any)[0]) },
		"no pools":   func(m map[string]any) { m["hosting"].([]any)[0].(map[string]any)["pools"] = []any{} },
		"no cluster": func(m map[string]any) { m["hosting"].([]any)[0].(map[string]any)["cluster"] = map[string]any{} },
		"no namespace": func(m map[string]any) {
			m["hosting"].([]any)[0].(map[string]any)["namespace"] = map[string]any{"name": "x"}
		},
		"wrong hosting":  func(m map[string]any) { m["hosting"] = "aws" },
		"wrong envelope": func(m map[string]any) { m["schema"] = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			m := workersFixture("iw-lab-a", "workers-aws", "aws")
			mutate(m)
			if _, err := readWorkers(encode(m), "iw-lab-a", "workers-aws"); !errors.Is(err, ErrLab) {
				t.Fatalf("accepted %s: %v", name, err)
			}
		})
	}
	for _, raw := range []string{"", "[]", `{"schema":1,"owner":"iw-lab-a","batch":"workers-aws","hosting":[]} {}`} {
		if _, err := readWorkers([]byte(raw), "iw-lab-a", "workers-aws"); !errors.Is(err, ErrLab) {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
}

func TestUnitWorkersManifestBelongsToTheBorrowedLab(t *testing.T) {
	d := &Dependencies{}
	t.Setenv(EnvLabWorkers, "")
	if _, err := d.WorkerHosting(); !errors.Is(err, ErrLab) {
		t.Fatalf("no lab: %v", err)
	}
	d.lab = fixtureLab(t)
	d.lab.Batch = "workers-aws"
	file := filepath.Join(t.TempDir(), "workers.json")
	t.Setenv(EnvLabWorkers, file)
	if _, err := d.WorkerHosting(); !errors.Is(err, ErrLab) {
		t.Fatalf("missing file: %v", err)
	}
	raw, err := json.Marshal(workersFixture(d.lab.Owner, "workers-aws", "aws"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	hosting, err := d.WorkerHosting()
	if err != nil || len(hosting) != 1 || hosting[0].Provider != "aws" {
		t.Fatalf("WorkerHosting = %+v, %v", hosting, err)
	}
	d.lab.Owner = "iw-lab-other"
	if _, err := d.WorkerHosting(); !errors.Is(err, ErrLab) {
		t.Fatalf("foreign owner: %v", err)
	}
}
