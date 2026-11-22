package catalogue

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/tester"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

func TestUnitCatalogueRefusesIncompleteOrCorruptEmbeddedPin(t *testing.T) {
	// Packaging/refresh mistakes must fail closed instead of publishing an
	// empty catalogue or silently accepting a replacement identity inventory.
	if _, err := load(fstest.MapFS{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing identity inventory accepted: %v", err)
	}
	if _, err := load(fstest.MapFS{"upstream/CATALOGUE.json": {Data: []byte("{")}}); err == nil {
		t.Fatal("corrupt identity inventory accepted")
	}
	if _, err := load(fstest.MapFS{"upstream/CATALOGUE.json": {Data: []byte("[]")}}); !errors.Is(err, ErrPin) {
		t.Fatalf("empty replacement pin accepted: %v", err)
	}
}

func TestUnitCatalogueMatchesEveryCapturedMetadataAndProductionCompiler(t *testing.T) {
	rows, err := All()
	if err != nil || len(rows) != 62 {
		t.Fatalf("complete catalogue: %d %v", len(rows), err)
	}
	var capture struct {
		Pages []struct {
			Response struct {
				Data struct {
					Templates struct {
						Edges []struct {
							Node struct {
								ID          string   `json:"id"`
								Name        string   `json:"name"`
								Slug        string   `json:"slug"`
								Description string   `json:"description"`
								Body        string   `json:"body"`
								Type        string   `json:"type"`
								Labels      []string `json:"labels"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"searchPolicyTemplates"`
				} `json:"data"`
			} `json:"response"`
		} `json:"pages"`
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "fixtures", "captures", "C54", "response.json"))
	if err != nil || json.Unmarshal(raw, &capture) != nil {
		t.Fatal("read recorded catalogue", err)
	}
	byID := map[string]Template{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	engine, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, page := range capture.Pages {
		for _, edge := range page.Response.Data.Templates.Edges {
			n := edge.Node
			row, ok := byID[n.ID]
			if !ok || row.Name != n.Name || row.Slug != n.Slug || row.Description != n.Description || string(row.Type) != n.Type ||
				!reflect.DeepEqual(slices.Sorted(slices.Values(row.Labels)), slices.Sorted(slices.Values(n.Labels))) {
				t.Fatalf("captured metadata differs: %s", n.Name)
			}
			if _, err := kernel.ParseULID(row.ID); err != nil {
				t.Fatal(err)
			}
			want, rewrites, err := Rewrite(row.Source, n.Body)
			if err != nil || want != row.Body || rewrites != row.Rewrites {
				t.Fatalf("source/live namespace adaptation differs: %s %v", row.Slug, err)
			}
			if row.Type == rego.Login || row.Type == rego.Access {
				t.Fatalf("excluded type served: %s", row.Type)
			}
			if err := engine.Check(t.Context(), rego.Policy{Type: row.Type, Engine: row.Engine, Body: row.Body}); err != nil {
				t.Errorf("production compile %s %s: %v", row.Slug, row.Engine, err)
			}
			seen++
		}
	}
	if seen != 62 {
		t.Fatalf("captured membership incomplete: %d", seen)
	}
	license, err := upstream.ReadFile("upstream/LICENSE")
	notice, noticeErr := upstream.ReadFile("upstream/NOTICE")
	if err != nil || noticeErr != nil || !bytes.Contains(notice, license) || !bytes.Contains(license, []byte("Copyright (c) 2023 Spacelift")) {
		t.Fatal("MIT notice not preserved")
	}
	rows[0].Labels[0] = "mutated"
	unchanged, err := Get(rows[0].ID)
	if err != nil || unchanged.Labels[0] == "mutated" {
		t.Fatal("caller mutated pinned catalogue")
	}
	if _, err := Get("01KACME0000000000000000002"); !errors.Is(err, ErrMissing) {
		t.Fatal("unknown template accepted", err)
	}
}

func TestUnitCompatibilityCorpusCompilesEveryPinnedProgram(t *testing.T) {
	engine, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	count, login, legacy := 0, 0, 0
	err = fs.WalkDir(upstream, "upstream/examples", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".rego") || strings.HasSuffix(name, "_test.rego") {
			return err
		}
		kind := rego.Type(strings.ToUpper(path.Base(path.Dir(name))))
		if kind == "PUSH" {
			kind = rego.Push
		}
		raw, err := upstream.ReadFile(name)
		if err != nil {
			return err
		}
		body, changes, err := Rewrite(name, string(raw))
		if err != nil {
			return err
		}
		version := rego.V1
		if _, err := ast.ParseModuleWithOpts(name, body, ast.ParserOptions{RegoVersion: ast.RegoV1}); err != nil {
			version = rego.V0
		}
		if err := engine.Check(t.Context(), rego.Policy{Type: kind, Engine: version, Body: body}); err != nil {
			t.Errorf("compatibility compile %s %s: %v", name, version, err)
		}
		count++
		if kind == rego.Login {
			login++
		}
		if kind == rego.Access {
			legacy++
		}
		t.Logf("%s: %d namespace rewrites; dialect %s", name, changes, version)
		return nil
	})
	if err != nil || count != 74 || login != 6 || legacy != 6 {
		t.Fatalf("complete compatibility programs: %d/%d LOGIN/%d ACCESS: %v", count, login, legacy, err)
	}
}

func TestUnitPinnedNotificationWildcardsAreUnsafeBeforeAdaptation(t *testing.T) {
	for _, name := range []string{"discord-drift-detection-with-changes", "ms-teams-drift-detection-with-changes"} {
		t.Run(name, func(t *testing.T) {
			modules := map[string]*ast.Module{}
			for _, suffix := range []string{".rego", "_test.rego"} {
				file := "upstream/examples/notification/" + name + suffix
				raw, err := upstream.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				module, err := ast.ParseModuleWithOpts(file, string(raw), ast.ParserOptions{RegoVersion: ast.RegoV0})
				if err != nil {
					t.Fatal(err)
				}
				modules[file] = module
			}
			_, err := tester.NewRunner().SetDefaultRegoVersion(ast.RegoV0).SetModules(modules).RunTests(t.Context(), nil)
			if err == nil || !strings.Contains(err.Error(), "rego_unsafe_var_error") {
				t.Fatalf("unsafe upstream baseline differs: %v", err)
			}
		})
	}
}
