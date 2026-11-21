package deps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureLab(t *testing.T) *labManifest {
	t.Helper()
	var m labManifest
	m.Schema = 1
	m.Owner = "iw-lab-test"
	m.Batch = "policies"
	m.Postgres.Container = strings.Repeat("a", 64)
	m.Postgres.DSN = "postgres://infraweave:infraweave@127.0.0.1:5432/infraweave?sslmode=disable"
	m.ObjectStore.Container = strings.Repeat("b", 64)
	m.ObjectStore.Endpoint = "http://127.0.0.1:9000"
	m.Gitea.Container = strings.Repeat("c", 64)
	m.Gitea.URL = "https://127.0.0.1:3000"
	m.Gitea.CAFile = filepath.Join(t.TempDir(), "ca.pem")
	return &m
}

func labInspect(m *labManifest) labCommand {
	return func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 2 || args[0] != "inspect" {
			return nil, fmt.Errorf("unexpected mutation: %v", args)
		}
		service := map[string]string{m.Postgres.Container: "postgres", m.ObjectStore.Container: "objectstore", m.Gitea.Container: "gitea"}[args[1]]
		if service == "" {
			return nil, errors.New("unknown container")
		}
		port := map[string]string{"postgres": "5432", "objectstore": "9000", "gitea": "3000"}[service]
		return json.Marshal([]any{map[string]any{
			"State": map[string]bool{"Running": true},
			"Config": map[string]any{"Labels": map[string]string{
				"io.infraweave.lab.owner": m.Owner, "io.infraweave.lab.batch": m.Batch, "io.infraweave.lab.managed-by": "terraform", "io.infraweave.lab.service": service,
			}},
			"NetworkSettings": map[string]any{"Ports": map[string]any{port + "/tcp": []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": port}}}},
		}})
	}
}

func TestUnitLabManifestBoundary(t *testing.T) {
	valid := fixtureLab(t)
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readLab(raw); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*labManifest){
		"schema": func(m *labManifest) { m.Schema = 2 }, "owner": func(m *labManifest) { m.Owner = "other-user" }, "batch": func(m *labManifest) { m.Batch = "" },
		"container":             func(m *labManifest) { m.Postgres.Container = "--privileged" },
		"remote database":       func(m *labManifest) { m.Postgres.DSN = "postgres://secret@example.com:5432/prod" },
		"overlapping endpoints": func(m *labManifest) { m.Postgres.DSN = m.ObjectStore.Endpoint },
		"remote store":          func(m *labManifest) { m.ObjectStore.Endpoint = "http://example.com:9000" },
		"malformed URL":         func(m *labManifest) { m.Gitea.URL = "https://127.0.0.1:%/" },
		"missing port":          func(m *labManifest) { m.Gitea.URL = "https://127.0.0.1" },
		"insecure Gitea":        func(m *labManifest) { m.Gitea.URL = "http://127.0.0.1:3000" },
		"relative CA":           func(m *labManifest) { m.Gitea.CAFile = "ca.pem" },
	} {
		t.Run(name, func(t *testing.T) {
			m := *valid
			mutate(&m)
			b, _ := json.Marshal(m)
			if _, err := readLab(b); !errors.Is(err, ErrLab) {
				t.Fatalf("invalid manifest passed: %v", err)
			}
		})
	}
	for name, raw := range map[string]string{"malformed": "{secret", "extra": strings.TrimSuffix(string(raw), "}") + `,"unknown":"secret"}`, "trailing": string(raw) + ` {}`} {
		t.Run(name, func(t *testing.T) {
			_, err := readLab([]byte(raw))
			if !errors.Is(err, ErrLab) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid data accepted or echoed: %v", err)
			}
		})
	}
}

func TestUnitLabBorrowingRequiresTerraformOwnership(t *testing.T) {
	m := fixtureLab(t)
	if err := verifyLab(t.Context(), m, labInspect(m)); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"io.infraweave.lab.owner", "io.infraweave.lab.batch", "io.infraweave.lab.managed-by", "io.infraweave.lab.service", "stopped", "malformed", "wrong count", "Docker failure", "foreign endpoint", "missing state", "missing config", "missing network"} {
		t.Run(label, func(t *testing.T) {
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				if label == "Docker failure" {
					return nil, ErrLab
				}
				if label == "malformed" {
					return []byte("not JSON"), nil
				}
				if label == "wrong count" {
					return []byte("[]"), nil
				}
				raw, err := labInspect(m)(ctx, args...)
				if err != nil {
					return nil, err
				}
				var inspected []map[string]any
				if err := json.Unmarshal(raw, &inspected); err != nil {
					return nil, err
				}
				switch label {
				case "missing state":
					inspected[0]["State"] = nil
				case "missing config":
					inspected[0]["Config"] = nil
				case "missing network":
					inspected[0]["NetworkSettings"] = nil
				case "foreign endpoint":
					inspected[0]["NetworkSettings"] = map[string]any{"Ports": map[string]any{}}
				case "stopped":
					inspected[0]["State"].(map[string]any)["Running"] = false
				default:
					inspected[0]["Config"].(map[string]any)["Labels"].(map[string]any)[label] = "different-owner"
				}
				return json.Marshal(inspected)
			}
			if err := verifyLab(t.Context(), m, run); !errors.Is(err, ErrLab) {
				t.Fatalf("foreign or unavailable dependency was accepted: %v", err)
			}
		})
	}
}

func TestUnitLabNamespacesAndPKI(t *testing.T) {
	root := t.TempDir()
	pki := filepath.Join(root, "pki")
	if err := WriteLabPKI(pki); err != nil {
		t.Fatal(err)
	}
	if err := WriteLabPKI(pki); err == nil {
		t.Fatal("overwrote existing PKI")
	}
	m := fixtureLab(t)
	m.Gitea.CAFile = filepath.Join(pki, "gitea-ca.pem")
	raw, _ := json.Marshal(m)
	file := filepath.Join(root, "deployment.json")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a, b := &Dependencies{}, &Dependencies{}
	for _, d := range []*Dependencies{a, b} {
		if err := d.useLab(t.Context(), file, labInspect(m)); err != nil {
			t.Fatal(err)
		}
		if len(d.labNamespace) != 16 || d.Serial() <= 0 {
			t.Fatal("missing suite namespace")
		}
		g, err := d.labGitea()
		if err != nil || g.URL != m.Gitea.URL || g.CAFile != m.Gitea.CAFile {
			t.Fatalf("borrowed Gitea TLS: %v", err)
		}
		store, err := d.ObjectStore(t.Context())
		if err != nil || store.Endpoint != m.ObjectStore.Endpoint || store.c != nil {
			t.Fatal("started a replacement object store")
		}
		g, err = d.Gitea(t.Context())
		if err != nil || g.URL != m.Gitea.URL {
			t.Fatalf("borrowed Gitea: %v", err)
		}
		if err := d.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if a.labNamespace == b.labNamespace || a.Serial() == b.Serial() {
		t.Fatal("independent processes can share fixture names")
	}
	if err := a.useLab(t.Context(), file+"missing", labInspect(m)); !errors.Is(err, ErrLab) {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.useLab(t.Context(), file, labInspect(m)); !errors.Is(err, ErrLab) {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.useLab(t.Context(), file, func(context.Context, ...string) ([]byte, error) { return nil, ErrLab }); !errors.Is(err, ErrLab) {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Gitea.CAFile, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.labGitea(); !errors.Is(err, ErrLab) {
		t.Fatal("invalid CA passed")
	}
	if err := os.Remove(m.Gitea.CAFile); err != nil {
		t.Fatal(err)
	}
	if _, err := a.labGitea(); !errors.Is(err, ErrLab) {
		t.Fatal("missing CA passed")
	}
}
